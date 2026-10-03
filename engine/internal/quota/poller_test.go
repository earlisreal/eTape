package quota

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	histquota "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotrequesthistoryklquota"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type capPub struct {
	mu  sync.Mutex
	evs []wsmsg.SysEvent
}

func (c *capPub) Publish(_ wsmsg.Topic, _ string, payload any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := payload.(wsmsg.SysEvent); ok {
		c.evs = append(c.evs, e)
	}
}

func histBody(t *testing.T, used, remain int32) []byte {
	t.Helper()
	b, _ := proto.Marshal(&histquota.Response{RetType: proto.Int32(0),
		S2C: &histquota.S2C{UsedQuota: proto.Int32(used), RemainQuota: proto.Int32(remain)}})
	return b
}

func TestPollerEmitsForeignAfterDebounceAndPublishesLatest(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo:            subInfoBody(t, 62, 38, conn(true, 47), conn(false, 15)),
		opend.ProtoQotRequestHistoryKLQuota: histBody(t, 41, 59),
	}}
	pub := &capPub{}
	clk := clock.NewFake(time.Now())
	p := New(Config{SubWarnHeadroom: 12, HistWarnRemain: 10}, f, pub, clk) // poll() calls clk.Now() when it emits
	ctx := context.Background()

	p.poll(ctx)
	if _, ok := p.Latest(); !ok {
		t.Fatal("Latest must be set after a successful poll")
	}
	if len(pub.evs) != 0 {
		t.Fatalf("no FOREIGN on first poll (debounce): %+v", pub.evs)
	}
	p.poll(ctx)
	q, _ := p.Latest()
	if q.State != "foreign" || q.SubForeign != 15 || q.SubOwn != 47 || q.SubUsed != 62 {
		t.Fatalf("snapshot wrong: %+v", q)
	}
	if len(pub.evs) != 1 || pub.evs[0].Level != "info" || pub.evs[0].Kind != "quota" {
		t.Fatalf("one FOREIGN(info) event expected: %+v", pub.evs)
	}
}

// TestRunPollsImmediatelyThenOnTickThenStopsOnCancel drives Run itself (not
// poll directly) to prove: (1) Run polls immediately on entry, before any
// tick fires; (2) advancing the fake clock by pollInterval fires the ticker
// and triggers a second poll; (3) canceling ctx makes Run return ctx.Err().
//
// A single upfront clk.Advance(pollInterval) races the ticker's registration
// inside the Run goroutine: Run's ticker isn't created until after the
// synchronous first poll returns (poller.go:56-57), and the test goroutine
// cannot know exactly when that happens. This mirrors
// internal/broker/netx/ratelimit_test.go's TestTokenBucket_TakeBlocksThenSucceeds
// and internal/feed/opend/opendfeed_test.go's TestSeedRetriesTransientGetKLFailure:
// instead of one upfront Advance, poll in small increments — a tiny real
// sleep gives the Run goroutine a scheduling point, then a small fake
// advance, repeated until the tick-driven second poll's effect (a new
// FOREIGN event, per the debounce rule covered by
// TestPollerEmitsForeignAfterDebounceAndPublishesLatest) lands.
func TestRunPollsImmediatelyThenOnTickThenStopsOnCancel(t *testing.T) {
	f := &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo:            subInfoBody(t, 62, 38, conn(true, 47), conn(false, 15)),
		opend.ProtoQotRequestHistoryKLQuota: histBody(t, 41, 59),
	}}
	pub := &capPub{}
	clk := clock.NewFake(time.Now())
	p := New(Config{SubWarnHeadroom: 12, HistWarnRemain: 10}, f, pub, clk)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// 1. Run polls immediately on entry — this check happens before any
	// clk.Advance call, so before any ticker could possibly have fired.
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := p.Latest(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Run's immediate first poll")
		}
		time.Sleep(time.Millisecond)
	}
	pub.mu.Lock()
	baseline := len(pub.evs)
	pub.mu.Unlock()
	if baseline != 0 {
		t.Fatalf("no event expected until the 2-poll FOREIGN debounce completes: %+v", pub.evs)
	}

	// 2. Advancing the fake clock must trigger a ticker-driven second poll.
	const (
		stepAdvance   = 10 * time.Second
		maxIterations = 30 // 300s of virtual time = 5x pollInterval headroom
	)
	fired := false
	for i := 0; i < maxIterations; i++ {
		time.Sleep(time.Millisecond) // real preemption point for the Run goroutine
		clk.Advance(stepAdvance)
		pub.mu.Lock()
		n := len(pub.evs)
		pub.mu.Unlock()
		if n > baseline {
			fired = true
			break
		}
	}
	if !fired {
		t.Fatal("advancing the clock by pollInterval did not trigger a second poll")
	}
	pub.mu.Lock()
	if len(pub.evs) != 1 || pub.evs[0].Kind != "quota" || pub.evs[0].Level != "info" {
		t.Fatalf("want exactly one FOREIGN(info) event from the tick-driven poll: %+v", pub.evs)
	}
	pub.mu.Unlock()

	// 3. Canceling ctx must stop Run.
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestPollFailureHoldsStateAndSkips(t *testing.T) {
	pub := &capPub{}
	p := New(Config{SubWarnHeadroom: 12, HistWarnRemain: 10}, &fakeReq{err: opend.ErrNotConnected}, pub, clock.NewFake(time.Now()))
	p.poll(context.Background())
	if _, ok := p.Latest(); ok {
		t.Fatal("failed poll must not set Latest")
	}
	if len(pub.evs) != 0 {
		t.Fatalf("failed poll must emit nothing: %+v", pub.evs)
	}
}

func TestHistoryQuotaFailureDoesNotBlockSubscriptionQuota(t *testing.T) {
	f := &fakeReq{
		bodies: map[uint32][]byte{
			opend.ProtoQotGetSubInfo: subInfoBody(t, 8, 92, conn(true, 8)),
		},
		errs: map[uint32]error{opend.ProtoQotRequestHistoryKLQuota: errors.New("history quota unavailable")},
	}
	p := New(Config{}, f, &capPub{}, clock.NewFake(time.Now()))
	var gotSubRemain int
	refreshBegun, refreshEnded := 0, 0
	p.SetSubscriptionQuotaObserver(func(remain int, _ time.Time) { gotSubRemain = remain })
	p.SetSubscriptionQuotaRefresh(func(context.Context) error { refreshBegun++; return nil }, func() { refreshEnded++ })
	p.poll(context.Background())
	if gotSubRemain != 92 {
		t.Fatalf("subscription quota observer received %d, want 92 despite history read failure", gotSubRemain)
	}
	if refreshBegun != 1 || refreshEnded != 1 {
		t.Fatalf("subscription refresh barrier begin/end = %d/%d, want 1/1", refreshBegun, refreshEnded)
	}
	if _, ok := p.Latest(); ok {
		t.Fatal("failed full quota poll should not publish a partial panel snapshot")
	}
}

type quotaTraceReq struct {
	*fakeReq
	order *[]string
}

func (r quotaTraceReq) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	if protoID == opend.ProtoQotRequestHistoryKLQuota {
		*r.order = append(*r.order, "history-read")
	}
	return r.fakeReq.Request(ctx, protoID, req)
}

func TestHistoryQuotaRefreshBracketsCounterReadAndObserver(t *testing.T) {
	order := []string{}
	r := quotaTraceReq{fakeReq: &fakeReq{bodies: map[uint32][]byte{
		opend.ProtoQotGetSubInfo:            subInfoBody(t, 1, 99, conn(true, 1)),
		opend.ProtoQotRequestHistoryKLQuota: histBody(t, 2, 98),
	}}, order: &order}
	p := New(Config{}, r, &capPub{}, clock.NewFake(time.Now()))
	p.SetHistoryQuotaRefresh(func(context.Context) error {
		order = append(order, "begin")
		return nil
	}, func() { order = append(order, "end") })
	p.SetHistoryQuotaObserver(func(int, time.Time) { order = append(order, "observe") })
	p.poll(context.Background())
	want := []string{"begin", "history-read", "observe", "end"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("history refresh order = %v, want %v", order, want)
	}
}
