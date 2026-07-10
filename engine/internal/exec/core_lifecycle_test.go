// Package exec_test (external, same rationale as core_test.go: these tests
// construct broker/sim brokers, and sim imports exec, so an internal
// "package exec" test file here would be a real import cycle).
//
// The boot-recovery test needs to read Core's internal *State right after
// Recover (single goroutine, before Run starts) — the brief's original
// package-exec design read c.state directly. Since this file is
// package exec_test, that unexported field isn't reachable directly; instead
// it goes through StateForTest(), an exported test-only accessor defined in
// export_test.go (package exec, compiled only for `go test`, never shipped).
package exec_test

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// armBoth arms the master switch (the only arm switch there is now). The name
// and the unused venue parameter are kept so every call site below (which
// still names the venue it's about to submit to) doesn't need touching.
func armBoth(t *testing.T, c *exec.Core, _ exec.VenueID) {
	t.Helper()
	if ack := c.Do(exec.Arm{}); !ack.Accepted {
		t.Fatalf("master arm: %+v", ack)
	}
}

func TestCoreCancelRestingOrder(t *testing.T) {
	c, sims, _ := newTestCore(t, "sim-1")
	sims["sim-1"].SetMark("AAPL", 100)
	armBoth(t, c, "sim-1")
	// Buy limit 90 with mark 100 → rests.
	ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 90})
	if !ack.Accepted {
		t.Fatalf("submit: %+v", ack)
	}
	// Wait for the accepted (working) order update.
	waitFor(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == ack.OrderID && o.Order.Status == exec.StatusAccepted
	})
	if cack := c.Do(exec.CancelOrder{Venue: "sim-1", OrderID: ack.OrderID}); !cack.Accepted {
		t.Fatalf("cancel: %+v", cack)
	}
	u := waitFor(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == ack.OrderID && o.Order.Status == exec.StatusCanceled
	}).(exec.OrderUpdate)
	if u.Order.Working() {
		t.Fatalf("canceled order still working: %+v", u.Order)
	}
}

func TestCoreReplaceRestingOrder(t *testing.T) {
	c, sims, _ := newTestCore(t, "sim-1")
	sims["sim-1"].SetMark("AAPL", 100)
	armBoth(t, c, "sim-1")
	ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 90})
	waitFor(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.Status == exec.StatusAccepted
	})
	if rack := c.Do(exec.ReplaceOrder{Venue: "sim-1", OrderID: ack.OrderID, Qty: 20, LimitPrice: 91}); !rack.Accepted {
		t.Fatalf("replace: %+v", rack)
	}
	u := waitFor(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == ack.OrderID && o.Order.Qty == 20
	}).(exec.OrderUpdate)
	if u.Order.LimitPrice != 91 {
		t.Fatalf("replace didn't apply limit: %+v", u.Order)
	}
}

func TestCoreKillSwitchDisarmsAndCancels(t *testing.T) {
	c, sims, _ := newTestCore(t, "sim-1")
	sims["sim-1"].SetMark("AAPL", 100)
	armBoth(t, c, "sim-1")
	a1 := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 90})
	a2 := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 90})
	if !a1.Accepted || !a2.Accepted {
		t.Fatalf("submits: %+v %+v", a1, a2)
	}
	if kack := c.Do(exec.KillSwitch{}); !kack.Accepted {
		t.Fatalf("kill: %+v", kack)
	}
	canceled := map[string]bool{}
	for len(canceled) < 2 {
		u := waitFor(t, c, func(u exec.Update) bool {
			o, ok := u.(exec.OrderUpdate)
			return ok && o.Order.Status == exec.StatusCanceled
		}).(exec.OrderUpdate)
		canceled[u.Order.ID] = true
	}
	if !canceled[a1.OrderID] || !canceled[a2.OrderID] {
		t.Fatalf("kill did not cancel both: %v", canceled)
	}
	// Master is disarmed after kill: a new submit is blocked.
	if ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 90}); ack.Accepted || ack.Reason != "master disarmed" {
		t.Fatalf("post-kill submit should be blocked, got %+v", ack)
	}
}

// A fresh Core on the same store replays today's persisted events into order
// state (crash-recovery). Reads state via StateForTest (see file header)
// directly after Recover, before Run — single goroutine, race-free.
func TestCoreBootRecoveryReplaysLog(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	dbPath := filepath.Join(t.TempDir(), "recover.db")
	st, err := store.Open(store.Options{Path: dbPath, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	mkCore := func() (*exec.Core, *sim.Broker) {
		b := sim.New("sim-1", clk, 100_000)
		seedMarketableBook(b, "AAPL", 100)
		cfg := exec.CoreConfig{
			Venues: []exec.VenueID{"sim-1"},
			Gate: exec.GateConfig{
				Global: exec.GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
				Venue:  map[exec.VenueID]exec.VenueLimits{"sim-1": {MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
			},
			Store: st, Brokers: map[exec.VenueID]exec.Broker{"sim-1": b}, Clock: clk,
			IDGen: exec.NewOrderIDGen(clk, rand.New(rand.NewSource(2))),
		}
		return exec.NewCore(cfg), b
	}

	// Core A: arm, submit → fill; wait for the fill, then stop.
	ctxA, cancelA := context.WithCancel(context.Background())
	cA, _ := mkCore()
	if err := cA.Recover(ctxA); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cA.Run(ctxA) }()
	armBoth(t, cA, "sim-1")
	ack := cA.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	waitFor(t, cA, func(u exec.Update) bool { f, ok := u.(exec.FillUpdate); return ok && f.Fill.OrderID == ack.OrderID })
	cancelA()
	st.Flush() // ensure all exec appends are durable

	// Core B: fresh state on the same store. Recover replays the log.
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	cB, _ := mkCore()
	if err := cB.Recover(ctxB); err != nil {
		t.Fatal(err)
	}
	stateB := cB.StateForTest()
	o, ok := stateB.Venue("sim-1").Orders[ack.OrderID]
	if !ok {
		t.Fatalf("recovered state missing order %s", ack.OrderID)
	}
	if o.Status != exec.StatusFilled || o.ExecutedQty != 10 {
		t.Fatalf("recovered order state wrong: %+v", o)
	}
	if len(stateB.Venue("sim-1").Fills) != 1 {
		t.Fatalf("recovered fills = %d, want 1", len(stateB.Venue("sim-1").Fills))
	}
	// Boot is always disarmed regardless of the log.
	if stateB.MasterArmed {
		t.Fatal("recovered state should boot disarmed")
	}
}
