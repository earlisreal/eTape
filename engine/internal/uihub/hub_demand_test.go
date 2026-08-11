package uihub

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/md"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

type spyHubFeed struct {
	mu       sync.Mutex
	ensured  []feed.Demand
	released []string
}

func (s *spyHubFeed) Validate(context.Context, string) error { return nil }
func (s *spyHubFeed) Ensure(d feed.Demand) {
	s.mu.Lock()
	s.ensured = append(s.ensured, d)
	s.mu.Unlock()
}
func (s *spyHubFeed) Release(id string) {
	s.mu.Lock()
	s.released = append(s.released, id)
	s.mu.Unlock()
}

func runHub(t *testing.T) (*Hub, func()) {
	t.Helper()
	h, _ := NewHubForTest(clock.System{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.Run(ctx) }()
	return h, cancel
}

func sysEvents(h *Hub) []wsmsg.SysEvent {
	frames := h.m.snapshotFrames(wsmsg.TopicSysEvents)
	if len(frames) == 0 {
		return nil
	}
	events, _ := frames[0].Payload.([]wsmsg.SysEvent)
	return events
}

func TestHubDemand_TrackReleaseSnapshot(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 7}
	h.Register(c)
	h.EnsureDemand(7, feed.WatchDemand("dyn/7/p1", "US.AAPL"))
	h.EnsureDemand(7, feed.Demand{ID: "dyn/7/p2", Symbol: "US.MSFT"}) // interest (no subs)
	h.sync()

	got := h.ActiveDemandSymbols()
	want := []string{"US.AAPL", "US.MSFT"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActiveDemandSymbols = %v, want %v", got, want)
	}
	if len(sf.ensured) != 2 {
		t.Fatalf("feed.Ensure calls = %d, want 2", len(sf.ensured))
	}

	h.ReleaseDemand(7, "dyn/7/p1")
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 1 || got[0] != "US.MSFT" {
		t.Fatalf("after release = %v, want [US.MSFT]", got)
	}
}

func TestHubDemand_UnregisterReleasesAll(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 3}
	h.Register(c)
	h.EnsureDemand(3, feed.WatchDemand("dyn/3/a", "US.AAPL"))
	h.EnsureDemand(3, feed.WatchDemand("dyn/3/b", "US.NVDA"))
	h.sync()

	h.Unregister(c)
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 0 {
		t.Fatalf("after unregister = %v, want empty", got)
	}
	sf.mu.Lock()
	rel := append([]string(nil), sf.released...)
	sf.mu.Unlock()
	sort.Strings(rel)
	if !reflect.DeepEqual(rel, []string{"dyn/3/a", "dyn/3/b"}) {
		t.Fatalf("released = %v, want both ids", rel)
	}
}

func TestHubDemand_EnsureAfterUnregisterDropped(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 9}
	h.Register(c)
	h.Unregister(c)
	h.sync()
	// A late ensure for a gone conn must NOT re-create state or subscribe.
	h.EnsureDemand(9, feed.WatchDemand("dyn/9/x", "US.AAPL"))
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 0 {
		t.Fatalf("late ensure leaked: %v", got)
	}
	sf.mu.Lock()
	n := len(sf.ensured)
	sf.mu.Unlock()
	if n != 0 {
		t.Fatalf("feed.Ensure called for dead conn: %d", n)
	}
}

func TestHubDemand_NilFeedNoPanic(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 1}
	h.Register(c)
	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL"))
	h.sync()
	if got := h.ActiveDemandSymbols(); len(got) != 1 {
		t.Fatalf("nil-feed should still track demands: %v", got)
	}
}

func TestHubDemand_FeedConfiguredAfterDemandReplaysExactDemand(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 10}
	d := feed.ChartDemand("chart", "US.AAPL")
	d.BackgroundSeed = true
	h.Register(c)
	h.EnsureDemand(c.nid, d)
	h.sync()

	if h.feed() != nil {
		t.Fatal("feed became available before SetFeed")
	}
	sf := &spyHubFeed{}
	if len(sf.ensured) != 0 {
		t.Fatalf("feed.Ensure before configuration = %d, want 0", len(sf.ensured))
	}
	h.SetFeed(sf)
	h.sync()

	sf.mu.Lock()
	got := append([]feed.Demand(nil), sf.ensured...)
	sf.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("feed.Ensure calls = %d, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0], d) {
		t.Fatalf("replayed demand = %+v, want %+v", got[0], d)
	}
	hasTicker := false
	for _, sub := range got[0].Subs {
		if sub == feed.SubTicker {
			hasTicker = true
		}
	}
	if !hasTicker {
		t.Fatalf("replayed demand subscriptions = %v, want SubTicker", got[0].Subs)
	}
}

func TestHubDemand_FeedConfiguredBeforeDemandEnsuresOnce(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	h.sync()
	c := &fakeClient{nid: 11}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.sync()

	sf.mu.Lock()
	n := len(sf.ensured)
	sf.mu.Unlock()
	if n != 1 {
		t.Fatalf("feed.Ensure calls = %d, want 1", n)
	}
}

func TestHubDemand_ReleaseBeforeFeedConfigurationNotReplayed(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 12}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.ReleaseDemand(c.nid, "chart")
	h.sync()

	sf := &spyHubFeed{}
	h.SetFeed(sf)
	h.sync()
	sf.mu.Lock()
	n := len(sf.ensured)
	sf.mu.Unlock()
	if n != 0 {
		t.Fatalf("released demand replayed %d times", n)
	}
}

func TestHubDemand_UnregisterBeforeFeedConfigurationNotReplayed(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 13}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.Unregister(c)
	h.sync()

	sf := &spyHubFeed{}
	h.SetFeed(sf)
	h.sync()
	sf.mu.Lock()
	n := len(sf.ensured)
	sf.mu.Unlock()
	if n != 0 {
		t.Fatalf("unregistered demand replayed %d times", n)
	}
}

func TestHubDemand_LatestDemandReplayedAfterFeedConfiguration(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 14}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("p1", "US.AAPL"))
	h.EnsureDemand(c.nid, feed.ChartDemand("p1", "US.NVDA"))
	h.sync()

	sf := &spyHubFeed{}
	h.SetFeed(sf)
	h.sync()
	sf.mu.Lock()
	got := append([]feed.Demand(nil), sf.ensured...)
	sf.mu.Unlock()
	if len(got) != 1 || got[0].Symbol != "US.NVDA" {
		t.Fatalf("replayed demands = %+v, want only latest NVDA demand", got)
	}
}

// spyBackfill records symbols passed to the injected backfill trigger and
// lets a test report any attempt's outcome on its own schedule (via report/
// reportLast), mirroring the real hubBackfill closure in main.go, which
// reports success/failure asynchronously once orch.Backfill returns.
type spyBackfill struct {
	mu    sync.Mutex
	syms  []string
	dones []func(ok bool)
}

func (s *spyBackfill) trigger(sym string, done func(ok bool)) {
	s.mu.Lock()
	s.syms = append(s.syms, sym)
	s.dones = append(s.dones, done)
	s.mu.Unlock()
}

func (s *spyBackfill) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.syms...)
}

// report invokes the done callback recorded for the idx'th trigger (0-based,
// in trigger order) with ok, as if that spawned backfill had just finished.
func (s *spyBackfill) report(idx int, ok bool) {
	s.mu.Lock()
	done := s.dones[idx]
	s.mu.Unlock()
	done(ok)
}

// reportLast is report for the most recently recorded trigger.
func (s *spyBackfill) reportLast(ok bool) {
	s.mu.Lock()
	idx := len(s.dones) - 1
	s.mu.Unlock()
	s.report(idx, ok)
}

func TestHubDemand_WatchAndFocusedTriggerBackfillOnce(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	bf := &spyBackfill{}
	h.SetBackfill(bf.trigger)
	c := &fakeClient{nid: 1}
	h.Register(c)

	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL"))
	h.EnsureDemand(1, feed.WatchDemand("dyn/1/b", "US.AAPL")) // repeat, different demand id, same symbol
	h.EnsureDemand(1, demandForTest("dyn/1/c", "US.MSFT"))
	h.sync()

	got := bf.snapshot()
	sort.Strings(got)
	want := []string{"US.AAPL", "US.MSFT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backfill triggers = %v, want %v (AAPL must dedup to one spawn)", got, want)
	}
}

func TestHubDemand_WatchThenFocusedUsesSeparateHistoryRoles(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	var mu sync.Mutex
	var roles []string
	h.SetHistoryWarm(func(_ string, _ func(bool)) {
		mu.Lock()
		roles = append(roles, "prepare")
		mu.Unlock()
	}, func(_ string, _ func(bool)) {
		mu.Lock()
		roles = append(roles, "warm")
		mu.Unlock()
	})
	c := &fakeClient{nid: 8}
	h.Register(c)
	h.EnsureDemand(8, feed.WatchDemand("watch", "US.NFLX"))
	h.sync()
	h.EnsureDemand(8, feed.ChartDemand("chart", "US.NFLX"))
	h.sync()
	mu.Lock()
	got := append([]string(nil), roles...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"warm", "prepare"}) {
		t.Fatalf("history roles=%v, want watch warm then focused preparation", got)
	}
}

func TestHubDemand_InterestDoesNotBackfill(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	bf := &spyBackfill{}
	h.SetBackfill(bf.trigger)
	c := &fakeClient{nid: 2}
	h.Register(c)

	h.EnsureDemand(2, feed.Demand{ID: "dyn/2/a", Symbol: "US.TSLA"}) // interest: no subs
	h.sync()

	if got := bf.snapshot(); len(got) != 0 {
		t.Fatalf("interest demand triggered backfill: %v", got)
	}
	if got := h.ActiveDemandSymbols(); len(got) != 1 || got[0] != "US.TSLA" {
		t.Fatalf("interest demand should still be tracked: %v", got)
	}
}

func TestHubDemand_NilBackfillNoPanic(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	sf := &spyHubFeed{}
	h.SetFeed(sf)
	c := &fakeClient{nid: 4}
	h.Register(c)

	h.EnsureDemand(4, feed.WatchDemand("dyn/4/a", "US.AAPL")) // no SetBackfill call
	h.sync()

	sf.mu.Lock()
	n := len(sf.ensured)
	sf.mu.Unlock()
	if n != 1 {
		t.Fatalf("feed.Ensure calls = %d, want 1 (demand path must work without a backfill trigger)", n)
	}
}

func TestHubDemand_HistoryNotConfiguredRetainsFocusedDemand(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 9}
	h.Register(c)
	h.EnsureDemand(9, feed.ChartDemand("chart", "US.AAPL"))
	h.sync()
	if events := sysEvents(h); len(events) != 0 {
		t.Fatalf("sys.events = %+v, want no chart-ready before history configuration", events)
	}
	if got := h.ActiveDemandSymbols(); len(got) != 1 || got[0] != "US.AAPL" {
		t.Fatalf("retained demand symbols = %v, want [US.AAPL]", got)
	}
}

func TestHubDemand_HistoryExplicitlyDisabledEmitsChartReady(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetHistoryWarm(nil, nil)
	c := &fakeClient{nid: 15}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.sync()
	events := sysEvents(h)
	if len(events) != 1 {
		t.Fatalf("sys.events = %+v, want one chart-ready", events)
	}
	if events[0].Kind != "chart-ready" || events[0].Detail != "US.AAPL" {
		t.Fatalf("event=%+v, want chart-ready US.AAPL", events[0])
	}
}

func TestHubDemand_FocusedBeforeHistoryConfigurationReconciles(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 16}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.sync()
	if events := sysEvents(h); len(events) != 0 {
		t.Fatalf("chart-ready before history configuration = %+v, want none", events)
	}

	var mu sync.Mutex
	var prepared []string
	h.SetHistoryWarm(func(sym string, _ func(bool)) {
		mu.Lock()
		prepared = append(prepared, sym)
		mu.Unlock()
	}, nil)
	h.sync()
	mu.Lock()
	gotPrepared := append([]string(nil), prepared...)
	mu.Unlock()
	if !reflect.DeepEqual(gotPrepared, []string{"US.AAPL"}) {
		t.Fatalf("prepared symbols = %v, want [US.AAPL]", gotPrepared)
	}
	if events := sysEvents(h); len(events) != 0 {
		t.Fatalf("chart-ready during focused preparation = %+v, want none", events)
	}

	h.PublishMD(md.HistoryReadyUpdate{Symbol: "US.AAPL", Prepared: true})
	h.sync()
	if events := sysEvents(h); len(events) != 1 || events[0].Kind != "chart-ready" {
		t.Fatalf("sys.events after history-ready = %+v, want one chart-ready", events)
	}
}

func TestHubDemand_ExplicitlyDisabledHistoryAfterEarlyDemandEmitsOnce(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 17}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.sync()
	if events := sysEvents(h); len(events) != 0 {
		t.Fatalf("chart-ready before explicit disable = %+v, want none", events)
	}

	h.SetHistoryWarm(nil, nil)
	h.sync()
	if events := sysEvents(h); len(events) != 1 || events[0].Kind != "chart-ready" {
		t.Fatalf("sys.events after explicit disable = %+v, want one chart-ready", events)
	}
}

func TestHubDemand_EarlyWatchReconcilesWhenHistoryConfigured(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 18}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.WatchDemand("watch", "US.AAPL"))
	h.sync()

	var mu sync.Mutex
	var warmed []string
	h.SetHistoryWarm(nil, func(sym string, _ func(bool)) {
		mu.Lock()
		warmed = append(warmed, sym)
		mu.Unlock()
	})
	h.sync()
	mu.Lock()
	got := append([]string(nil), warmed...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"US.AAPL"}) {
		t.Fatalf("warmed symbols = %v, want [US.AAPL]", got)
	}
}

func TestHubDemand_FocusedWinsDuringHistoryReconciliation(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 19}
	h.Register(c)
	h.EnsureDemand(c.nid, feed.WatchDemand("watch", "US.AAPL"))
	h.EnsureDemand(c.nid, feed.ChartDemand("chart", "US.AAPL"))
	h.sync()

	var mu sync.Mutex
	var roles []string
	h.SetHistoryWarm(func(_ string, _ func(bool)) {
		mu.Lock()
		roles = append(roles, "prepare")
		mu.Unlock()
	}, func(_ string, _ func(bool)) {
		mu.Lock()
		roles = append(roles, "warm")
		mu.Unlock()
	})
	h.sync()
	mu.Lock()
	got := append([]string(nil), roles...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"prepare"}) {
		t.Fatalf("history roles = %v, want focused preparation only", got)
	}
}

func TestHubDemand_DeadConnNoBackfill(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	bf := &spyBackfill{}
	h.SetBackfill(bf.trigger)
	c := &fakeClient{nid: 5}
	h.Register(c)
	h.Unregister(c)
	h.sync()

	h.EnsureDemand(5, feed.WatchDemand("dyn/5/a", "US.AAPL")) // late ensure for a gone conn
	h.sync()

	if got := bf.snapshot(); len(got) != 0 {
		t.Fatalf("dead conn triggered backfill: %v", got)
	}
}

// TestHubDemand_BackfillInflightDedup pins that two demands for the same
// symbol arriving before the first spawn's outcome is reported spawn exactly
// one backfill -- backfillInflight, not just backfilled, must gate the spawn.
func TestHubDemand_BackfillInflightDedup(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	bf := &spyBackfill{}
	h.SetBackfill(bf.trigger)
	c := &fakeClient{nid: 1}
	h.Register(c)

	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL"))
	h.EnsureDemand(1, feed.WatchDemand("dyn/1/b", "US.AAPL")) // same symbol, still in flight
	h.sync()

	if got := bf.snapshot(); len(got) != 1 {
		t.Fatalf("triggers while first spawn is in flight = %v, want exactly 1 spawn", got)
	}
}

// TestHubDemand_BackfillFailureAllowsRetry is the regression test for the
// reported bug: a symbol whose daily backfill fails (e.g. OpenD was down)
// must not be marked backfilled forever -- a later demand for the same
// symbol must retry it. Once an attempt succeeds, backfilled sticks and a
// further demand must NOT re-spawn.
func TestHubDemand_BackfillFailureAllowsRetry(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	bf := &spyBackfill{}
	h.SetBackfill(bf.trigger)
	c := &fakeClient{nid: 1}
	h.Register(c)

	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL"))
	h.sync()
	if got := bf.snapshot(); len(got) != 1 {
		t.Fatalf("first ensure triggers = %v, want 1", got)
	}

	bf.reportLast(false) // simulate an OpenD-down daily-fetch failure
	h.sync()

	h.EnsureDemand(1, feed.WatchDemand("dyn/1/b", "US.AAPL")) // same symbol, new demand id
	h.sync()
	if got := bf.snapshot(); len(got) != 2 {
		t.Fatalf("after a failed attempt, a later demand must retry: triggers = %v, want 2", got)
	}

	bf.reportLast(true) // now it succeeds
	h.sync()

	h.EnsureDemand(1, feed.WatchDemand("dyn/1/c", "US.AAPL"))
	h.sync()
	if got := bf.snapshot(); len(got) != 2 {
		t.Fatalf("after a successful attempt, a later demand must NOT retry: triggers = %v, want 2", got)
	}
}

// TestHubResyncRearmsOnlyUnbackfilledChartSymbols is the other half of the
// regression test: an OpenD reconnect (md.ResyncedUpdate) must re-arm chart
// symbols whose daily backfill previously failed, must leave already-
// succeeded chart symbols alone, and must never touch interest-only demands
// (no chart, WantsHistory=false) -- matching InterestDoesNotBackfill above.
func TestHubResyncRearmsOnlyUnbackfilledChartSymbols(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	bf := &spyBackfill{}
	h.SetBackfill(bf.trigger)
	c := &fakeClient{nid: 1}
	h.Register(c)

	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL"))        // chart; will fail
	h.EnsureDemand(1, feed.Demand{ID: "dyn/1/b", Symbol: "US.TSLA"}) // interest only, no history
	h.EnsureDemand(1, demandForTest("dyn/1/c", "US.MSFT"))           // chart; will succeed
	h.sync()

	got := bf.snapshot()
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"US.AAPL", "US.MSFT"}) {
		t.Fatalf("initial triggers = %v, want AAPL+MSFT only (TSLA is interest-only)", got)
	}

	bf.report(0, false) // AAPL's attempt failed (OpenD was down)
	bf.report(1, true)  // MSFT's attempt succeeded
	h.sync()

	h.PublishMD(md.ResyncedUpdate{}) // OpenD reconnected + resubscribed
	h.sync()

	got = bf.snapshot()
	if len(got) != 4 || countStrings(got)["US.AAPL"] != 2 || countStrings(got)["US.MSFT"] != 2 {
		t.Fatalf("resync must refresh each focused chart: triggers = %v", got)
	}
}

func countStrings(values []string) map[string]int {
	out := map[string]int{}
	for _, value := range values {
		out[value]++
	}
	return out
}

// TestHubResyncNoopWithoutBackfill confirms a resync with no backfill fn
// injected (replay / backfill-disabled) never panics.
func TestHubResyncNoopWithoutBackfill(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	h.SetFeed(&spyHubFeed{})
	c := &fakeClient{nid: 1}
	h.Register(c)
	h.EnsureDemand(1, feed.WatchDemand("dyn/1/a", "US.AAPL")) // no SetBackfill call
	h.sync()

	h.PublishMD(md.ResyncedUpdate{})
	h.sync() // must not panic
}

// demandForTest builds a focused-like feed.Demand (the extra subs the
// "focused" profile adds — book+quote — plus WantsHistory set the same way
// uihub/commands.go's demandForProfile sets it for that profile).
func demandForTest(id, symbol string) feed.Demand {
	return feed.Demand{ID: id, Symbol: symbol,
		Subs:    []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m, feed.SubBook},
		Focused: true, WantsHistory: true}
}
