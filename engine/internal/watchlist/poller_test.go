package watchlist

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"

	qotcommon "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	snappb "github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetsecuritysnapshot"
)

// pub's frames slice is written by Run's goroutine (TestPokePublishesMembershipImmediately
// drives Run concurrently) and read by the test goroutine, so it needs a mutex
// — the brief's original version raced under `go test -race` (this repo's
// Makefile/CI run with -race).
type pub struct {
	mu     sync.Mutex
	frames []struct {
		topic wsmsg.Topic
		key   string
		pl    any
	}
}

func (p *pub) Publish(topic wsmsg.Topic, key string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frames = append(p.frames, struct {
		topic wsmsg.Topic
		key   string
		pl    any
	}{topic, key, payload})
}
func (p *pub) last() wsmsg.WatchlistRowsPayload {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frames[len(p.frames)-1].pl.(wsmsg.WatchlistRowsPayload)
}

// snapshot returns a defensive copy of frames so far, safe to read while
// Run's goroutine may still be publishing concurrently.
func (p *pub) snapshot() []struct {
	topic wsmsg.Topic
	key   string
	pl    any
} {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]struct {
		topic wsmsg.Topic
		key   string
		pl    any
	}, len(p.frames))
	copy(out, p.frames)
	return out
}

// fakeReq returns a canned snapshot for the requested codes; retType controls
// a whole-batch application failure to exercise binary split.
type fakeReq struct {
	calls   int
	failAll bool
}

func (r *fakeReq) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	r.calls++
	in := req.(*snappb.Request)
	var resp snappb.Response
	if r.failAll {
		rt := int32(1)
		resp.RetType = &rt
		msg := "batch fail"
		resp.RetMsg = &msg
		b, _ := proto.Marshal(&resp)
		return opend.Frame{Body: b}, nil
	}
	var list []*snappb.Snapshot
	for _, sec := range in.GetC2S().GetSecurityList() {
		code := sec.GetCode()
		cur, last, vol := 10.0, 8.0, int64(1000)
		list = append(list, &snappb.Snapshot{
			Basic: &snappb.SnapshotBasicData{
				// SnapshotBasicData has 15 required proto2 fields (see
				// scan_test.go's snapshotBasic) — every one must be set or
				// proto.Unmarshal fails validation below.
				Security:       &qotcommon.Security{Code: proto.String(code), Market: proto.Int32(int32(qotcommon.QotMarket_QotMarket_US_Security))},
				Type:           proto.Int32(3),
				IsSuspend:      proto.Bool(false),
				ListTime:       proto.String("2020-01-01"),
				LotSize:        proto.Int32(1),
				PriceSpread:    proto.Float64(0.01),
				UpdateTime:     proto.String("2026-07-12 09:30:00"),
				HighPrice:      &cur,
				OpenPrice:      &cur,
				LowPrice:       &last,
				LastClosePrice: &last,
				CurPrice:       &cur,
				Volume:         &vol,
				Turnover:       proto.Float64(0),
				TurnoverRate:   proto.Float64(0),
			},
		})
	}
	resp.RetType = proto.Int32(0)
	resp.S2C = &snappb.S2C{SnapshotList: list}
	b, _ := proto.Marshal(&resp)
	return opend.Frame{Body: b}, nil
}

// Direct-call idiom (no Run/ticker involved), matching scan_test.go's
// p.pollOnce(...) and stockinfo_test.go's p.fetchSnapshots(...) style — fast,
// deterministic, no goroutine/sleep races.

func TestEmptyListPublishesButZeroRequests(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	pb := &pub{}
	r := &fakeReq{}
	fc := clock.NewFake(time.Unix(0, 0))
	p := New(l, r, pb, fc, 3*time.Second)
	p.pollAndPublish(context.Background())
	if r.calls != 0 {
		t.Fatalf("empty list issued %d requests, want 0", r.calls)
	}
	if len(pb.frames) == 0 {
		t.Fatal("empty list published nothing (push-is-the-list broken)")
	}
	if len(pb.last().Symbols) != 0 {
		t.Fatalf("want empty Symbols, got %v", pb.last().Symbols)
	}
}

func TestPollComputesChangePct(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("AAPL")
	pb := &pub{}
	r := &fakeReq{}
	fc := clock.NewFake(time.Unix(0, 0))
	p := New(l, r, pb, fc, 3*time.Second)
	p.pollAndPublish(context.Background())
	got := pb.last()
	if len(got.Rows) != 1 || got.Rows[0].Symbol != "US.AAPL" {
		t.Fatalf("rows=%v", got.Rows)
	}
	// (10-8)/8*100 = 25
	if got.Rows[0].ChangePct == nil || *got.Rows[0].ChangePct != 25 {
		t.Fatalf("changePct=%v want 25", got.Rows[0].ChangePct)
	}
	if got.RefreshedAt == nil {
		t.Fatal("RefreshedAt nil after successful poll")
	}
}

func TestPokePublishesMembershipImmediately(t *testing.T) {
	// Poke's "publish membership, then fresh poll" behavior lives in Run's
	// select loop, so this test drives Run with a REAL clock + a poll-until
	// deadline (stockinfo_test.go's Run-driving idiom), not the fake clock.
	st := newFakeStore()
	l, _ := NewList(st)
	pb := &pub{}
	r := &fakeReq{}
	p := New(l, r, pb, clock.System{}, time.Hour) // long interval: only Poke should drive activity
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(pb.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond) // wait for Run's initial publishMembership
	}
	_, _ = l.Add("MSFT")
	p.Poke()
	found := false
	for time.Now().Before(deadline) && !found {
		for _, f := range pb.snapshot() {
			pl := f.pl.(wsmsg.WatchlistRowsPayload)
			for _, s := range pl.Symbols {
				if s == "US.MSFT" {
					found = true
				}
			}
		}
		if !found {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("Poke did not publish membership including US.MSFT within deadline")
	}
}

func TestBinarySplitOnBatchFailure(t *testing.T) {
	st := newFakeStore()
	l, _ := NewList(st)
	_, _ = l.Add("A")
	_, _ = l.Add("B")
	pb := &pub{}
	r := &fakeReq{failAll: true}
	fc := clock.NewFake(time.Unix(0, 0))
	p := New(l, r, pb, fc, 3*time.Second)
	p.pollAndPublish(context.Background())
	// 2 syms → fail → split into [A],[B] → 1 top + 2 leaves = 3 calls.
	if r.calls != 3 {
		t.Fatalf("binary split calls=%d want 3", r.calls)
	}
	// Symbols still complete even though rows are empty (all bad).
	if len(pb.last().Symbols) != 2 {
		t.Fatalf("Symbols dropped on failure: %v", pb.last().Symbols)
	}
}
