package opend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
	"google.golang.org/protobuf/proto"
)

type bookRejectRPC struct {
	base     fakeRPC
	rejected chan struct{}
}

type blockedOptionalBookRPC struct {
	base                       fakeRPC
	entered, canceled, release chan struct{}
}

func (r *blockedOptionalBookRPC) Request(ctx context.Context, id uint32, msg proto.Message) (Frame, error) {
	d := msg.(*qotsub.Request).GetC2S()
	if d.GetIsSubOrUnSub() && len(d.GetSubTypeList()) == 1 && d.GetSubTypeList()[0] == pbSubType(feed.SubBook) {
		close(r.entered)
		<-ctx.Done()
		close(r.canceled)
		<-r.release
		return Frame{}, errors.New("fixture uncertain BOOK outcome")
	}
	return r.base.Request(ctx, id, msg)
}

func TestOptionalBookRPCYieldsToNewOrdinaryDemand(t *testing.T) {
	clk := clock.NewFake(time.Unix(1782000000, 0))
	rpc := &blockedOptionalBookRPC{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	m := newSubManager(rpc, clk, subOptions{Budget: 8, OptionalBook: true})
	m.Ensure(feed.Demand{ID: "one", Symbol: "US.ONE", Subs: []feed.SubType{feed.SubTicker}})
	m.pass(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.optionalBookPass(context.Background()) }()
	defer func() { close(rpc.release); <-done }()
	select {
	case <-rpc.entered:
	case <-time.After(time.Second):
		t.Fatal("optional BOOK did not start")
	}
	m.Ensure(feed.Demand{ID: "two", Symbol: "US.TWO", Subs: []feed.SubType{feed.SubTicker}})
	select {
	case <-rpc.canceled:
	case <-time.After(time.Second):
		t.Fatal("new ordinary work did not cancel optional BOOK")
	}
	m.pass(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.WaitActive(ctx, subKey{"US.TWO", feed.SubTicker}); err != nil {
		t.Fatalf("ordinary admission waited for optional RPC: %v", err)
	}
}

func TestOptionalBookSeedQueueCannotFillOrdinaryQueues(t *testing.T) {
	cli := New(Options{Recorder: &captureCounter{}})
	f := NewOpenDFeed(cli, FeedOptions{})
	for range cap(f.optionalBookSeedq) + 1 {
		f.enqueueOptionalBook("US.AIXI")
	}
	if len(f.optionalBookSeedq) != cap(f.optionalBookSeedq) || len(f.foregroundSeedq) != 0 || len(f.backgroundSeedq) != 0 {
		t.Fatal("optional seed spilled into an ordinary queue")
	}
	disabled := NewOpenDFeed(nil, FeedOptions{})
	if disabled.sub.opt.OptionalBook {
		t.Fatal("disabled client requested recorder BOOK")
	}
}

func (r *bookRejectRPC) Request(ctx context.Context, id uint32, msg proto.Message) (Frame, error) {
	request := msg.(*qotsub.Request).GetC2S()
	if request.GetIsSubOrUnSub() && len(request.GetSubTypeList()) == 1 && request.GetSubTypeList()[0] == pbSubType(feed.SubBook) {
		select {
		case r.rejected <- struct{}{}:
		default:
		}
		body, err := proto.Marshal(&qotsub.Response{RetType: proto.Int32(1), RetMsg: proto.String("US book entitlement unavailable")})
		return Frame{ProtoID: id, Body: body}, err
	}
	return r.base.Request(ctx, id, msg)
}

func TestOptionalBookFailureLeavesOrdinaryTickerAdmitted(t *testing.T) {
	clk := clock.NewFake(time.Unix(1782000000, 0))
	rpc := &bookRejectRPC{rejected: make(chan struct{}, 1)}
	m := newSubManager(rpc, clk, subOptions{Budget: 8, OptionalBook: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	m.Ensure(feed.Demand{ID: "tape", Symbol: "US.AIXI", Subs: []feed.SubType{feed.SubTicker}})
	wait, cancelWait := context.WithTimeout(ctx, time.Second)
	defer cancelWait()
	if err := m.WaitActive(wait, subKey{"US.AIXI", feed.SubTicker}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rpc.rejected:
	case <-time.After(time.Second):
		t.Fatal("optional BOOK was never attempted")
	}
	if got := m.DesiredSymbols()["US.AIXI"]; len(got) != 1 || got[0] != feed.SubTicker {
		t.Fatalf("ordinary profile grew: %v", got)
	}
	if got := m.Quarantined(); len(got) != 0 {
		t.Fatalf("book rejection quarantined ordinary demand: %v", got)
	}
}

func TestOptionalBookQuotaPressureRespectsHoldAndSharedOwnership(t *testing.T) {
	clk := clock.NewFake(time.Unix(1782000000, 0))
	rpc := &fakeRPC{}
	m := newSubManager(rpc, clk, subOptions{Budget: 4, RequireQuota: true, QuotaHeadroom: 1, OptionalBook: true})
	ctx := context.Background()
	m.SetSubscriptionQuota(4, clk.Now())
	m.Ensure(feed.Demand{ID: "tape", Symbol: "AIXI", Subs: []feed.SubType{feed.SubTicker}})
	m.pass(ctx)
	m.optionalBookPass(ctx)
	if got := m.ActiveSymbols()["AIXI"]; len(got) != 1 {
		t.Fatalf("optional ownership leaked into ordinary seed: %v", got)
	}
	m.mu.Lock()
	_, book := m.active[subKey{"AIXI", feed.SubBook}]
	m.mu.Unlock()
	if !book {
		t.Fatal("US bare symbol did not receive optional book")
	}
	m.SetSubscriptionQuota(0, clk.Now())
	m.optionalBookPass(ctx)
	m.mu.Lock()
	_, book = m.active[subKey{"AIXI", feed.SubBook}]
	m.mu.Unlock()
	if !book {
		t.Fatal("pressure released BOOK before minimum hold")
	}
	clk.Advance(time.Minute)
	m.optionalBookPass(ctx)
	m.mu.Lock()
	_, book = m.active[subKey{"AIXI", feed.SubBook}]
	remain := m.quotaRemain
	m.mu.Unlock()
	if book || remain != 0 {
		t.Fatalf("release book=%v fabricated remaining quota=%d", book, remain)
	}
	m.SetSubscriptionQuota(4, clk.Now())
	m.optionalBookPass(ctx)
	m.Ensure(feed.Demand{ID: "dom", Symbol: "AIXI", Subs: []feed.SubType{feed.SubBook}})
	m.optionalBookPass(ctx)
	m.SetSubscriptionQuota(0, clk.Now())
	clk.Advance(time.Minute)
	m.optionalBookPass(ctx)
	m.mu.Lock()
	_, book = m.active[subKey{"AIXI", feed.SubBook}]
	owned := m.optional[subKey{"AIXI", feed.SubBook}]
	m.mu.Unlock()
	if !book || owned {
		t.Fatalf("shared ordinary book=%v recorder ownership=%v", book, owned)
	}
	m.ConnectionDown()
	m.optionalBookPass(ctx)
	m.mu.Lock()
	owned = m.optional[subKey{"AIXI", feed.SubBook}]
	m.mu.Unlock()
	if owned {
		t.Fatal("optional admission before reconnect quota refresh")
	}
}

func TestOptionalBookNeverSpendsStaleQuotaOrEvictsOrdinaryLinger(t *testing.T) {
	clk := clock.NewFake(time.Unix(1782000000, 0))
	rpc := &fakeRPC{}
	m := newSubManager(rpc, clk, subOptions{Budget: 2, RequireQuota: true, OptionalBook: true, QuotaHeadroom: 1})
	ctx := context.Background()
	m.SetSubscriptionQuota(10, clk.Now())
	m.Ensure(feed.Demand{ID: "tape", Symbol: "US.AIXI", Subs: []feed.SubType{feed.SubTicker, feed.SubQuote}})
	m.pass(ctx)
	m.optionalBookPass(ctx)
	m.mu.Lock()
	n := len(m.active)
	_, book := m.active[subKey{"US.AIXI", feed.SubBook}]
	m.mu.Unlock()
	if n != 2 || book {
		t.Fatalf("optional book evicted existing ordinary slots: %d %v", n, book)
	}
	m.opt.Budget = 3
	clk.Advance(76 * time.Second)
	m.optionalBookPass(ctx)
	m.mu.Lock()
	_, book = m.active[subKey{"US.AIXI", feed.SubBook}]
	m.mu.Unlock()
	if book {
		t.Fatal("optional book admitted with stale quota")
	}
}
