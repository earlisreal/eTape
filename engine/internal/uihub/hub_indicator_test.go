package uihub

import (
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/md"
)

// indicatorCall records one Ensure/Release invocation the spy observed,
// including the connID so tests can assert exactly which connection an
// instance was attributed to.
type indicatorCall struct {
	connID uint64
	id     string
}

// spyIndicators is the Indicators-interface test double for hub-level tests,
// mirroring spyHubFeed's mutex-guarded recording style.
type spyIndicators struct {
	mu       sync.Mutex
	ensured  []indicatorCall
	released []indicatorCall
}

func (s *spyIndicators) EnsureIndicator(connID uint64, id string, _ md.IndicatorSpec) {
	s.mu.Lock()
	s.ensured = append(s.ensured, indicatorCall{connID: connID, id: id})
	s.mu.Unlock()
}

func (s *spyIndicators) ReleaseIndicator(connID uint64, id string) {
	s.mu.Lock()
	s.released = append(s.released, indicatorCall{connID: connID, id: id})
	s.mu.Unlock()
}

func (s *spyIndicators) snapshotReleased() []indicatorCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]indicatorCall(nil), s.released...)
}

func (s *spyIndicators) snapshotEnsured() []indicatorCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]indicatorCall(nil), s.ensured...)
}

// TestHubIndicator_UnregisterReleasesAll is the disconnect-leak regression
// test: a connection that ensures two indicator instances and then drops
// without ever sending UnsubscribeIndicator must have both released on its
// behalf by handleUnregister's sweep, mirroring
// TestHubDemand_UnregisterReleasesAll for market-data demands.
func TestHubIndicator_UnregisterReleasesAll(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	si := &spyIndicators{}
	h.SetIndicators(si)
	c := &fakeClient{nid: 11}
	h.Register(c)
	h.EnsureIndicator(11, "ind-a", md.IndicatorSpec{Symbol: "US.AAPL", Type: md.IndVWAP})
	h.EnsureIndicator(11, "ind-b", md.IndicatorSpec{Symbol: "US.AAPL", Type: md.IndEMA})
	h.sync()

	h.Unregister(c)
	h.sync()

	rel := si.snapshotReleased()
	sort.Slice(rel, func(i, j int) bool { return rel[i].id < rel[j].id })
	want := []indicatorCall{{connID: 11, id: "ind-a"}, {connID: 11, id: "ind-b"}}
	if !reflect.DeepEqual(rel, want) {
		t.Fatalf("released = %v, want %v", rel, want)
	}
	if _, ok := h.indicators[11]; ok {
		t.Fatalf("h.indicators[11] should be gone after unregister")
	}
}

// TestHubIndicator_EnsureAfterUnregisterDropped mirrors
// TestHubDemand_EnsureAfterUnregisterDropped: a late EnsureIndicator for a
// connection that already unregistered must be dropped by the demandLive
// guard, never forwarded to md.Core, and never recorded in h.indicators.
func TestHubIndicator_EnsureAfterUnregisterDropped(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	si := &spyIndicators{}
	h.SetIndicators(si)
	c := &fakeClient{nid: 12}
	h.Register(c)
	h.Unregister(c)
	h.sync()

	h.EnsureIndicator(12, "ind-x", md.IndicatorSpec{Symbol: "US.AAPL", Type: md.IndVWAP})
	h.sync()

	if n := len(si.snapshotEnsured()); n != 0 {
		t.Fatalf("EnsureIndicator forwarded for a dead conn: %d calls", n)
	}
	if _, ok := h.indicators[12]; ok {
		t.Fatalf("h.indicators[12] should not exist after a dropped late ensure")
	}
}

// TestHubIndicator_NilIndicatorsNoPanic confirms the nil-guard only skips the
// forwarding call to md.Core -- the Hub's own bookkeeping (h.indicators) must
// still update normally when SetIndicators is never called, including across
// an Unregister sweep.
func TestHubIndicator_NilIndicatorsNoPanic(t *testing.T) {
	h, cancel := runHub(t)
	defer cancel()
	c := &fakeClient{nid: 13}
	h.Register(c)

	h.EnsureIndicator(13, "ind-y", md.IndicatorSpec{Symbol: "US.AAPL", Type: md.IndVWAP})
	h.sync()
	if _, ok := h.indicators[13]["ind-y"]; !ok {
		t.Fatalf("bookkeeping should record ind-y even with nil Indicators")
	}

	h.ReleaseIndicator(13, "ind-y")
	h.sync()
	if _, ok := h.indicators[13]["ind-y"]; ok {
		t.Fatalf("release should have removed the bookkeeping entry")
	}

	h.EnsureIndicator(13, "ind-z", md.IndicatorSpec{Symbol: "US.AAPL", Type: md.IndVWAP})
	h.sync()
	h.Unregister(c) // must not panic sweeping ind-z with h.ind still nil
	h.sync()
}
