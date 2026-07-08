package uihub

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
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

// spyBackfill records symbols passed to the injected backfill trigger.
type spyBackfill struct {
	mu   sync.Mutex
	syms []string
}

func (s *spyBackfill) trigger(sym string) {
	s.mu.Lock()
	s.syms = append(s.syms, sym)
	s.mu.Unlock()
}

func (s *spyBackfill) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.syms...)
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

// demandForTest builds a focused-like feed.Demand (the extra subs the
// "focused" profile adds — book+quote — plus WantsHistory set the same way
// uihub/commands.go's demandForProfile sets it for that profile).
func demandForTest(id, symbol string) feed.Demand {
	return feed.Demand{ID: id, Symbol: symbol,
		Subs:    []feed.SubType{feed.SubQuote, feed.SubTicker, feed.SubKL1m, feed.SubBook},
		Focused: true, WantsHistory: true}
}
