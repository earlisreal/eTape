package watchlist

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/session"
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
	calls     int
	failAll   bool
	onRequest func()
	prepare   func(*snappb.SnapshotBasicData) bool
}

func (r *fakeReq) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	r.calls++
	if r.onRequest != nil {
		r.onRequest()
	}
	in := req.(*snappb.Request)
	var resp snappb.Response
	if r.failAll {
		rt := int32(1)
		resp.RetType = &rt
		msg := "invalid security"
		resp.RetMsg = &msg
		b, _ := proto.Marshal(&resp)
		return opend.Frame{Body: b}, nil
	}
	var list []*snappb.Snapshot
	for _, sec := range in.GetC2S().GetSecurityList() {
		code := sec.GetCode()
		cur, last, vol := 10.0, 8.0, int64(1000)
		snapshot := &snappb.Snapshot{
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
		}
		if r.prepare == nil || r.prepare(snapshot.Basic) {
			list = append(list, snapshot)
		}
	}
	resp.RetType = proto.Int32(0)
	resp.S2C = &snappb.S2C{SnapshotList: list}
	b, _ := proto.Marshal(&resp)
	return opend.Frame{Body: b}, nil
}

// Direct-call idiom (no Run/ticker involved), matching scan_test.go's
// p.pollOnce(...) and stockinfo_test.go's p.fetchSnapshots(...) style — fast,
// deterministic, no goroutine/sleep races.

func TestQuoteCompletionUsesLatestMembershipOrder(t *testing.T) {
	l, _ := NewList(newFakeStore())
	l.Seed([]string{"AAPL", "MSFT"})
	pb := &pub{}
	r := &fakeReq{onRequest: func() {
		if moved, err := l.Move("MSFT", "AAPL", "before"); err != nil || !moved {
			t.Fatalf("Move during request: moved=%v err=%v", moved, err)
		}
	}}
	p := New(l, r, pb, clock.NewFake(time.Unix(0, 0)), time.Second)
	p.pollAndPublish(context.Background())
	if got := pb.last().Symbols; !slices.Equal(got, []string{"US.MSFT", "US.AAPL"}) {
		t.Fatalf("published order: %v", got)
	}
}

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

func TestSessionMetrics(t *testing.T) {
	day := time.Date(2026, time.November, 24, 0, 0, 0, 0, session.Loc())
	for _, tc := range []struct {
		name string
		at   time.Time
		pct  *float64
		vol  *int64
	}{
		{"pre", day.Add(5 * time.Hour), proto.Float64(0), proto.Int64(0)},
		{"regular", day.Add(10 * time.Hour), proto.Float64(25), proto.Int64(1000)},
		{"post", day.Add(17 * time.Hour), proto.Float64(-2.1), nil},
		{"overnight", day.Add(time.Hour), nil, proto.Int64(800)},
		{"closed", day.AddDate(0, 0, 4), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := NewList(newFakeStore())
			l.Seed([]string{"AAPL"})
			pb := &pub{}
			r := &fakeReq{prepare: func(b *snappb.SnapshotBasicData) bool {
				b.UpdateTimestamp = proto.Float64(float64(tc.at.Unix()))
				b.PreMarket = &qotcommon.PreAfterMarketData{ChangeRate: proto.Float64(0), Volume: proto.Int64(0)}
				b.AfterMarket = &qotcommon.PreAfterMarketData{ChangeRate: proto.Float64(-2.1), Volume: proto.Int64(-1)}
				b.Overnight = &qotcommon.PreAfterMarketData{Volume: proto.Int64(800)}
				return true
			}}
			p := New(l, r, pb, clock.NewFake(tc.at), time.Second)
			p.pollAndPublish(context.Background())
			got := pb.last()
			row := got.Rows[0]
			if got.Session != session.PhaseAt(tc.at).String() || r.calls != 1 {
				t.Fatalf("session=%s requests=%d", got.Session, r.calls)
			}
			if !equalOptional(row.SessionChangePct, tc.pct) || !equalOptional(row.SessionVolume, tc.vol) {
				t.Fatalf("metrics: pct=%v volume=%v, want pct=%v volume=%v", row.SessionChangePct, row.SessionVolume, tc.pct, tc.vol)
			}
		})
	}
}

func equalOptional[T comparable](got, want *T) bool {
	return got == nil && want == nil || got != nil && want != nil && *got == *want
}

func TestSessionRolloverClearsCachedAndInFlightRows(t *testing.T) {
	start := time.Date(2026, time.November, 24, 9, 29, 0, 0, session.Loc())
	fc := clock.NewFake(start)
	l, _ := NewList(newFakeStore())
	l.Seed([]string{"AAPL", "MSFT"})
	pb := &pub{}
	omitted := false
	r := &fakeReq{prepare: func(b *snappb.SnapshotBasicData) bool {
		b.UpdateTime = proto.String(start.Format("2006-01-02 15:04:05"))
		b.PreMarket = &qotcommon.PreAfterMarketData{ChangeRate: proto.Float64(12), Volume: proto.Int64(70)}
		return !omitted || b.GetSecurity().GetCode() != "MSFT"
	}}
	p := New(l, r, pb, fc, time.Second)
	p.pollAndPublish(context.Background())
	if got := pb.last().Rows[0].SessionVolume; got == nil || *got != 70 {
		t.Fatalf("pre-market volume=%v", got)
	}
	omitted = true
	r.onRequest = func() { fc.Advance(time.Minute) }
	p.pollAndPublish(context.Background())
	r.onRequest = nil
	assertCleared := func() {
		t.Helper()
		got := pb.last()
		if got.Session != "rth" || len(got.Rows) != 2 {
			t.Fatalf("published session=%s rows=%d", got.Session, len(got.Rows))
		}
		for _, row := range got.Rows {
			if row.SessionChangePct != nil || row.SessionVolume != nil || row.Last == nil {
				t.Fatalf("rollover leaked or cleared the wrong fields: %+v", row)
			}
		}
	}
	assertCleared()
	// The next request starts in regular trading but still returns a
	// snapshot stamped during pre-market.
	p.pollAndPublish(context.Background())
	assertCleared()

	r.prepare = func(b *snappb.SnapshotBasicData) bool {
		b.UpdateTimestamp = proto.Float64(float64(fc.Now().Unix()))
		return true
	}
	p.pollAndPublish(context.Background())
	r.failAll = true
	p.pollAndPublish(context.Background())
	if got := pb.last().Rows[0].SessionVolume; got == nil || *got != 1000 {
		t.Fatalf("same-session failure lost cached volume: %v", got)
	}
	fc.Advance(24 * time.Hour)
	p.publishMembership()
	assertCleared()
}

func TestExtendedSnapshotWithRegularCloseTimestampRequiresCurrentActivity(t *testing.T) {
	now := time.Date(2026, time.November, 24, 8, 30, 0, 0, session.Loc())
	fc := clock.NewFake(now)
	l, _ := NewList(newFakeStore())
	l.Seed([]string{"AAPL"})
	pb := &pub{}
	volume := int64(100)
	stamp := time.Date(2026, time.November, 23, 16, 0, 0, 0, session.Loc())
	r := &fakeReq{prepare: func(b *snappb.SnapshotBasicData) bool {
		b.UpdateTimestamp = proto.Float64(float64(stamp.Unix()))
		b.PreMarket = &qotcommon.PreAfterMarketData{Volume: proto.Int64(volume), ChangeRate: proto.Float64(2.5)}
		b.AfterMarket = &qotcommon.PreAfterMarketData{Volume: proto.Int64(volume), ChangeRate: proto.Float64(2.5)}
		return true
	}}
	p := New(l, r, pb, fc, time.Second)
	for _, step := range []struct {
		volume int64
		show   bool
	}{{100, false}, {100, false}, {101, true}, {101, true}, {40, false}, {41, true}} {
		volume = step.volume
		p.pollAndPublish(context.Background())
		row := pb.last().Rows[0]
		if (row.SessionVolume != nil) != step.show || (row.SessionChangePct != nil) != step.show {
			t.Fatalf("volume=%d: displayed volume=%v change=%v, want displayed=%t", volume, row.SessionVolume, row.SessionChangePct, step.show)
		}
	}
	fc.Advance(8 * time.Hour)
	stamp = time.Date(2026, time.November, 24, 16, 0, 0, 0, session.Loc())
	p.pollAndPublish(context.Background())
	if got := pb.last().Rows[0]; got.SessionVolume != nil || got.SessionChangePct != nil {
		t.Fatalf("regular-close timestamp confirmed the first after-hours block: %+v", got)
	}
	volume++
	p.pollAndPublish(context.Background())
	if got := pb.last().Rows[0].SessionVolume; got == nil || *got != volume {
		t.Fatalf("advancing after-hours volume unavailable: %v", got)
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
