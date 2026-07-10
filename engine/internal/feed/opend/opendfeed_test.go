package opend

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotgetkl"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

// nextEvent pulls one event or fails the test on timeout.
func nextEvent(t *testing.T, ch <-chan feed.Event) feed.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for feed event")
		return nil
	}
}

func TestEnsureSubscribesAndSeeds(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{
		bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)},
		ticks: []*qotcommon.Ticker{{
			Time:     proto.String("2026-07-05 09:30:00"),
			Sequence: proto.Int64(1), Timestamp: proto.Float64(1782146400.5),
			Price: proto.Float64(309), Volume: proto.Int64(100), Turnover: proto.Float64(30900),
			Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Bid)),
		}},
	})
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	f.Ensure(feed.WatchDemand("w", "US.AAPL"))

	// Watch profile seeds bars then ticks (per-subtype order: KL, Ticker).
	if ev, ok := nextEvent(t, f.Events()).(feed.Bars1mEvent); !ok || !ev.Seed || len(ev.Bars) != 1 {
		t.Fatalf("first event = %#v, want seed Bars1mEvent", ev)
	}
	if ev, ok := nextEvent(t, f.Events()).(feed.TicksEvent); !ok || !ev.Seed || ev.Ticks[0].Seq != 1 {
		t.Fatalf("second event = %#v, want seed TicksEvent", ev)
	}

	// A live push now flows through as a non-seed event.
	m.pushToAll(ProtoQotUpdateTicker, 1517, &qotupdateticker.Response{
		RetType: proto.Int32(0),
		S2C: &qotupdateticker.S2C{
			Security: sec(11, "AAPL"),
			TickerList: []*qotcommon.Ticker{{
				Time:     proto.String("2026-07-05 09:30:01"),
				Sequence: proto.Int64(2), Timestamp: proto.Float64(1782146401.0),
				Price: proto.Float64(309.05), Volume: proto.Int64(50), Turnover: proto.Float64(15452.5),
				Dir: proto.Int32(int32(qotcommon.TickerDirection_TickerDirection_Ask)),
			}},
		},
	})
	if ev, ok := nextEvent(t, f.Events()).(feed.TicksEvent); !ok || ev.Seed || ev.Ticks[0].Seq != 2 {
		t.Fatalf("push event = %#v, want live TicksEvent seq=2", ev)
	}
}

// TestSeedRetriesTransientGetKLFailure covers the race Ensure's doc comment
// describes: the KL_1Min subscribe and the seed job fire with no ordering
// between them, so the seed's Qot_GetKL can reach OpenD before the subscribe
// acks and get rejected ("please subscribe to KL_1Min data first."). The mock
// fails the first Qot_GetKL for the symbol, then succeeds; seed must retry
// and still emit the seeded Bars1mEvent.
//
// The retry sleeps via clk.After, and that call happens inside the
// seedWorker goroutine at some point after Ensure returns — the test
// goroutine cannot know exactly when. A single upfront clk.Advance races
// that registration (mirrors internal/broker/netx/ratelimit_test.go's
// TestTokenBucket_TakeBlocksThenSucceeds), so instead this polls: a tiny real
// sleep to give the retry goroutine a scheduling point, then a small fake
// advance, repeated until the seeded event lands.
func TestSeedRetriesTransientGetKLFailure(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)}})

	var mu sync.Mutex
	klCalls := 0
	m.handler = func(mm *mockOpenD, conn net.Conn, f Frame) {
		if f.ProtoID == ProtoQotGetKL {
			mu.Lock()
			klCalls++
			first := klCalls == 1
			mu.Unlock()
			if first {
				mm.reply(conn, f, &qotgetkl.Response{
					RetType: proto.Int32(-1),
					RetMsg:  proto.String("Before calling the Get Real-time Candlestick interface, please subscribe to KL_1Min data first."),
				})
				return
			}
		}
		mm.defaultHandler(mm, conn, f)
	}

	cli := liveClient(t, m)
	clk := clock.NewFake(time.Unix(1_782_000_000, 0))
	f := NewOpenDFeed(cli, FeedOptions{Clock: clk})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	f.Ensure(feed.Demand{ID: "d", Symbol: "US.AAPL", Subs: []feed.SubType{feed.SubKL1m}})

	const (
		maxIterations = 500
		stepAdvance   = 10 * time.Millisecond
	)
	var ev feed.Event
	for i := 0; i < maxIterations; i++ {
		time.Sleep(time.Millisecond) // real preemption point for the seedWorker goroutine
		clk.Advance(stepAdvance)
		select {
		case ev = <-f.Events():
		default:
			continue
		}
		break
	}
	if ev == nil {
		t.Fatalf("no seed event after %d poll iterations (%v of virtual time advanced)", maxIterations, maxIterations*stepAdvance)
	}
	bars, ok := ev.(feed.Bars1mEvent)
	if !ok || !bars.Seed || len(bars.Bars) != 1 {
		t.Fatalf("event = %#v, want seed Bars1mEvent after retry", ev)
	}
	mu.Lock()
	n := klCalls
	mu.Unlock()
	if n < 2 {
		t.Fatalf("Qot_GetKL calls = %d, want >= 2 (must retry after the first failure)", n)
	}
}

func TestReconnectResubscribesReseedsAndEmitsResynced(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)}})
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	f.Ensure(feed.Demand{ID: "d", Symbol: "US.AAPL", Subs: []feed.SubType{feed.SubKL1m}})
	nextEvent(t, f.Events()) // initial seed

	subsBefore := countQotSubs(m)
	m.dropAllConns() // sever: client reconnects via backoff

	// Expect, in order: ConnDown, ConnUp, seed Bars1m (reseed), Resynced.
	var seen []string
	for len(seen) < 4 {
		switch ev := nextEvent(t, f.Events()).(type) {
		case feed.ConnDownEvent:
			seen = append(seen, "down")
		case feed.ConnUpEvent:
			seen = append(seen, "up")
		case feed.Bars1mEvent:
			if !ev.Seed {
				continue
			}
			seen = append(seen, "seed")
		case feed.ResyncedEvent:
			seen = append(seen, "resynced")
		}
	}
	want := []string{"down", "up", "seed", "resynced"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("event order = %v, want %v", seen, want)
		}
	}
	if countQotSubs(m) <= subsBefore {
		t.Fatal("no re-subscribe Qot_Sub after reconnect")
	}
}

func countQotSubs(m *mockOpenD) int {
	n := 0
	for _, f := range m.snapshotRequests() {
		if f.ProtoID == ProtoQotSub {
			n++
		}
	}
	return n
}

func TestHistoryBarsQuotaGuard(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.NEW", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 1, 1)}})
	m.quotaRemain = 0 // make the mock report an exhausted quota
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	_, err := f.HistoryBars(ctx, "US.NEW", feed.Res1m, time.UnixMilli(0), time.UnixMilli(1))
	if !errors.Is(err, ErrHistoryQuotaExhausted) {
		t.Fatalf("err = %v, want ErrHistoryQuotaExhausted", err)
	}
}

// TestHistoryBarsCoalescesConcurrentSameSymbol guards against a real
// double-quota-spend bug: deep backfill can now be triggered by two
// independent producers racing on the same symbol (scanner-pool admission
// and a UI chart-open demand, see uihub.Hub.handleEnsureDemand). Since the
// fetched-dedup map is only updated *after* a fetch completes, two calls
// that both arrive before either finishes would, without coalescing, each
// pass the quota-exhaustion guard and each spend a real history-quota slot
// for what should be one fetch.
func TestHistoryBarsCoalescesConcurrentSameSymbol(t *testing.T) {
	m := newMockOpenD(t)
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)}})
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	bars := make([][]feed.Bar, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			bars[i], errs[i] = f.HistoryBars(ctx, "US.AAPL", feed.ResDay, time.UnixMilli(0), time.UnixMilli(1))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(bars[i]) != 1 {
			t.Fatalf("call %d: got %d bars, want 1", i, len(bars[i]))
		}
	}
	var histReqs int
	for _, fr := range m.snapshotRequests() {
		if fr.ProtoID == ProtoQotRequestHistoryKL {
			histReqs++
		}
	}
	if histReqs != 1 {
		t.Fatalf("history requests sent = %d, want 1 (concurrent same-symbol calls must coalesce)", histReqs)
	}
}

// TestEnsureDoesNotBlockWhenSeedQueueFull covers the seed-queue backpressure
// path in Ensure: the enqueue is a non-blocking select, so a full queue must
// never make Ensure (and therefore the caller) block on I/O — it logs and
// drops instead, trusting the next resync to catch the symbol up.
func TestEnsureDoesNotBlockWhenSeedQueueFull(t *testing.T) {
	m := newMockOpenD(t)
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	// Fill the seed queue to capacity without a seed worker draining it
	// (Run is deliberately not started), then confirm Ensure still returns.
	for i := 0; i < cap(f.seedq); i++ {
		f.seedq <- seedJob{symbol: "US.FILL"}
	}

	done := make(chan struct{})
	go func() {
		f.Ensure(feed.WatchDemand("w", "US.AAPL"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Ensure blocked when the seed queue was full")
	}
}

// TestReconnectSeedFailureLogsAndContinues covers rule 2's "seed failures log
// and continue": one subtype's seed read fails (no quote data preloaded, so
// quoteSnapshot errors on the empty BasicQotList) while another succeeds
// (bars). The reconnect must still reach ResyncedEvent instead of wedging on
// the failed subtype.
func TestReconnectSeedFailureLogsAndContinues(t *testing.T) {
	m := newMockOpenD(t)
	// No `basic` set: quoteSnapshot will fail every time it's read.
	m.setData("US.AAPL", &qotData{bars1m: []*qotcommon.KLine{kl(1782146460, 309.1, 1000)}})
	cli := liveClient(t, m)
	f := NewOpenDFeed(cli, FeedOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.Run(ctx) }()

	f.Ensure(feed.Demand{ID: "d", Symbol: "US.AAPL", Subs: []feed.SubType{feed.SubKL1m, feed.SubQuote}})
	if ev, ok := nextEvent(t, f.Events()).(feed.Bars1mEvent); !ok || !ev.Seed {
		t.Fatalf("initial seed = %#v, want seed Bars1mEvent (quote seed fails and emits nothing)", ev)
	}

	m.dropAllConns() // sever: client reconnects via backoff

	// Expect, in order: ConnDown, ConnUp, seed Bars1m (reseed), Resynced —
	// the failed quote seed must not block ResyncedEvent from firing.
	var seen []string
	for len(seen) < 4 {
		switch ev := nextEvent(t, f.Events()).(type) {
		case feed.ConnDownEvent:
			seen = append(seen, "down")
		case feed.ConnUpEvent:
			seen = append(seen, "up")
		case feed.Bars1mEvent:
			if !ev.Seed {
				continue
			}
			seen = append(seen, "seed")
		case feed.QuoteEvent:
			t.Fatalf("unexpected QuoteEvent %#v: quoteSnapshot should have failed and logged instead", ev)
		case feed.ResyncedEvent:
			seen = append(seen, "resynced")
		}
	}
	want := []string{"down", "up", "seed", "resynced"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("event order = %v, want %v", seen, want)
		}
	}
}

func TestValidate_CachesPositive(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(0, "", 1)}
	f := &OpenDFeed{bf: newBackfill(r), validated: map[string]struct{}{}}
	if err := f.Validate(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("first call: want nil, got %v", err)
	}
	r.err = ErrNotConnected // any later RPC would now fail…
	if err := f.Validate(context.Background(), "US.AAPL"); err != nil {
		t.Fatalf("cached call must not RPC: want nil, got %v", err)
	}
}

func TestValidate_UnknownNotCached(t *testing.T) {
	r := &snapRPC{resp: snapshotResp(-1, "Unknown stock. X", 0)}
	f := &OpenDFeed{bf: newBackfill(r), validated: map[string]struct{}{}}
	if err := f.Validate(context.Background(), "US.X"); !errors.Is(err, feed.ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol, got %v", err)
	}
	// negative must not be cached — a now-valid symbol resolves.
	r.resp = snapshotResp(0, "", 1)
	if err := f.Validate(context.Background(), "US.X"); err != nil {
		t.Fatalf("second call after listing: want nil, got %v", err)
	}
}
