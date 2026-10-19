package opend

import (
	"context"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestBackgroundTickerNeverDisplacesInteractiveDemand(t *testing.T) {
	m, _, _ := newTestManager(t, 1)
	m.Ensure(feed.WatchDemand("watch", "US.AAPL"))
	m.Ensure(feed.Demand{ID: "alert", Symbol: "US.MSFT", Subs: []feed.SubType{feed.SubTicker}, Background: true})
	m.pass(context.Background())
	if active := m.ActiveSymbols(); len(active["US.AAPL"]) != 1 || len(active["US.MSFT"]) != 0 {
		t.Fatalf("active=%v", active)
	}
	active, capacity := m.TickerAvailability("US.MSFT")
	if active || !capacity {
		t.Fatalf("waiting alert: active=%v capacity=%v", active, capacity)
	}
	m.Release("watch")
	// The existing provider minimum hold remains authoritative.
	m.opt.MinHold = 0
	m.pass(context.Background())
	active, capacity = m.TickerAvailability("US.MSFT")
	if !active || capacity {
		t.Fatalf("available alert: active=%v capacity=%v", active, capacity)
	}
	m.ConnectionDown()
	active, _ = m.TickerAvailability("US.MSFT")
	if active {
		t.Fatal("disconnected ticker reported active")
	}
}

func TestInteractiveDemandReclaimsBackgroundSlotAfterMinimumHold(t *testing.T) {
	m, _, clk := newTestManager(t, 1)
	m.Ensure(feed.Demand{ID: "alert", Symbol: "US.MSFT", Subs: []feed.SubType{feed.SubTicker}, Background: true})
	m.pass(context.Background())
	clk.Advance(2 * m.opt.MinHold)
	m.Ensure(feed.WatchDemand("watch", "US.AAPL"))
	m.pass(context.Background())
	if active := m.ActiveSymbols(); len(active["US.AAPL"]) != 1 || len(active["US.MSFT"]) != 0 {
		t.Fatalf("background blocked interactive demand: %v", active)
	}
}
