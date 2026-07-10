// Package exec_test (external, same rationale as core_test.go and
// core_lifecycle_test.go: these tests construct broker/sim brokers, and sim
// imports exec, so a "package exec" test file here would be a real import
// cycle). Every exec-package identifier below is qualified with exec.
//
// This file is the plan's deliverable capstone: two SimBroker venues under one
// Core, exercising the aggregate gate across venues, master-vs-venue arming,
// cross-venue day-loss auto-disarm, and the headline invariant —
// replay(log) == state.
package exec_test

import (
	"context"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// buildMultiCore wires a Core over two SimBroker venues on a caller-owned store
// so the test can read exec_events back.
func buildMultiCore(t *testing.T, st *store.Store, clk clock.Clock, global exec.GlobalLimits, per exec.VenueLimits) (*exec.Core, map[exec.VenueID]*sim.Broker) {
	t.Helper()
	venues := []exec.VenueID{"sim-1", "sim-2"}
	brokers := map[exec.VenueID]exec.Broker{}
	sims := map[exec.VenueID]*sim.Broker{}
	for _, v := range venues {
		b := sim.New(v, clk, 100_000)
		seedMarketableBook(b, "AAPL", 100)
		brokers[v] = b
		sims[v] = b
	}
	cfg := exec.CoreConfig{
		Venues: venues,
		Gate:   exec.GateConfig{Global: global, Venue: map[exec.VenueID]exec.VenueLimits{"sim-1": per, "sim-2": per}},
		Store:  st, Brokers: brokers, Clock: clk,
		IDGen: exec.NewOrderIDGen(clk, rand.New(rand.NewSource(3))),
	}
	return exec.NewCore(cfg), sims
}

// TestCapstoneAggregateGateBlocksCrossVenue: a submit within per-venue caps but
// over the global symbol cap must be blocked by the aggregate layer. Positions
// are driven through the broker (real fills), never poked into State directly
// — Core.state is Core-owned once Run is active, so a direct
// state.ReconcilePositions call here would race with the writer goroutine.
func TestCapstoneAggregateGateBlocksCrossVenue(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "agg.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	// Per-venue shares cap 200 (generous), global symbol cap 250.
	c, sims := buildMultiCore(t, st, clk,
		exec.GlobalLimits{MaxDayLoss: 100000, MaxSymbolPositionShares: 250, MaxSymbolPositionValue: 1_000_000},
		exec.VenueLimits{MaxOrderValue: 1_000_000, MaxPositionValue: 1_000_000, MaxPositionShares: 200, MaxOpenOrders: 10})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = c.Run(ctx) }()
	c.Do(exec.Arm{})
	// Buy-fill 150 on sim-1 and 80 on sim-2 (marks=100, limits=100 → marketable).
	a1 := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 150, LimitPrice: 100})
	a2 := c.Do(exec.SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 80, LimitPrice: 100})
	if !a1.Accepted || !a2.Accepted {
		t.Fatalf("seed submits: %+v %+v", a1, a2)
	}
	// Wait until both venues report their positions (net 230).
	got := map[exec.VenueID]float64{}
	for got["sim-1"] != 150 || got["sim-2"] != 80 {
		pu := waitFor(t, c, func(u exec.Update) bool { p, ok := u.(exec.PositionUpdate); return ok && p.Position.Symbol == "AAPL" }).(exec.PositionUpdate)
		got[pu.Position.Venue] = pu.Position.Qty
	}
	_ = sims
	// A further 40 on sim-1: per-venue result 150+40=190<=200 (ok), but global
	// 230+40=270 > 250 → blocked by the global layer.
	ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 40, LimitPrice: 100})
	if ack.Accepted || ack.Reason != "resulting symbol position exceeds global share cap" {
		t.Fatalf("cross-venue global cap should block, got %+v", ack)
	}
}

// TestCapstoneMasterArmingCoversEveryVenue verifies the master-only arm model:
// with the master off, submits to every registered venue block ("master
// disarmed"); one master arm click then unblocks ALL of them at once — there
// is no separate per-venue switch left to flip.
func TestCapstoneMasterArmingCoversEveryVenue(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	st, _ := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "arm.db"), Clock: clk})
	defer func() { _ = st.Close() }()
	c, _ := buildMultiCore(t, st, clk,
		exec.GlobalLimits{MaxDayLoss: 100000, MaxSymbolPositionShares: 100000, MaxSymbolPositionValue: 1e12},
		exec.VenueLimits{MaxOrderValue: 1e12, MaxPositionValue: 1e12, MaxPositionShares: 100000, MaxOpenOrders: 100})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = c.Recover(ctx)
	go func() { _ = c.Run(ctx) }()

	// Master off: both venues block.
	if ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100}); ack.Accepted || ack.Reason != "master disarmed" {
		t.Fatalf("sim-1 disarmed should block: %+v", ack)
	}
	if ack := c.Do(exec.SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100}); ack.Accepted || ack.Reason != "master disarmed" {
		t.Fatalf("sim-2 disarmed should block: %+v", ack)
	}

	c.Do(exec.Arm{}) // one click arms every registered venue

	ack1 := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100})
	if !ack1.Accepted {
		t.Fatalf("sim-1 should accept once master is armed: %+v", ack1)
	}
	ack2 := c.Do(exec.SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100})
	if !ack2.Accepted {
		t.Fatalf("sim-2 should accept once master is armed: %+v", ack2)
	}
	// Both orders are marketable (mark=limit=100) and fill asynchronously via
	// the broker->pump->Run pipeline; wait for both fills before the test
	// returns, otherwise the deferred cancel()+st.Close() can race with Run
	// still appending fills to the store.
	waitFor(t, c, func(u exec.Update) bool { f, ok := u.(exec.FillUpdate); return ok && f.Fill.OrderID == ack1.OrderID })
	waitFor(t, c, func(u exec.Update) bool { f, ok := u.(exec.FillUpdate); return ok && f.Fill.OrderID == ack2.OrderID })
}

func TestCapstoneDayLossAutoDisarm(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	st, _ := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "dl.db"), Clock: clk})
	defer func() { _ = st.Close() }()
	c, sims := buildMultiCore(t, st, clk,
		exec.GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 100000, MaxSymbolPositionValue: 1e12},
		exec.VenueLimits{MaxOrderValue: 1e12, MaxPositionValue: 1e12, MaxPositionShares: 100000, MaxOpenOrders: 100})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = c.Recover(ctx)
	go func() { _ = c.Run(ctx) }()
	c.Do(exec.Arm{})
	// Push day P&Ls summing past -1000 → auto-disarm.
	sims["sim-1"].SetAccount(exec.AccountSnapshot{Venue: "sim-1", DayPnL: -600})
	sims["sim-2"].SetAccount(exec.AccountSnapshot{Venue: "sim-2", DayPnL: -500})
	waitFor(t, c, func(u exec.Update) bool { s, ok := u.(exec.StatusUpdate); return ok && !s.MasterArmed })
	if ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 1, LimitPrice: 100}); ack.Accepted || ack.Reason != "master disarmed" {
		t.Fatalf("after day-loss breach submit should block, got %+v", ack)
	}
}

// TestCapstoneReplayLogEqualsState is the headline invariant: the persisted
// log, read back and folded, equals the live Core's order + fill state.
// Account/positions are broker-reconciled, not event-sourced, so they are
// excluded from the comparison — a pure log-replay can never reconstruct them
// since ReconcileAccount/ReconcilePositions are never persisted (reconcile.go).
//
// The order index itself is unexported (State.orderIndex), so from this
// external test package the comparison goes through the exported accessors
// State.Venue(v).Orders and .Fills for each venue — which is exactly what
// determines order/fill state and is equivalent to comparing the index (every
// order's venue is recoverable from which venue's Orders map contains it).
func TestCapstoneReplayLogEqualsState(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "replay.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	venues := []exec.VenueID{"sim-1", "sim-2"}
	c, sims := buildMultiCore(t, st, clk,
		exec.GlobalLimits{MaxDayLoss: 1e9, MaxSymbolPositionShares: 1e9, MaxSymbolPositionValue: 1e12},
		exec.VenueLimits{MaxOrderValue: 1e12, MaxPositionValue: 1e12, MaxPositionShares: 1e9, MaxOpenOrders: 100})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	_ = c.Recover(ctx)
	go func() { _ = c.Run(ctx); close(done) }()
	c.Do(exec.Arm{})

	// Drive a mixed session: two fills on sim-1, a rest+cancel on sim-2, one fill on sim-2.
	f1 := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	waitFor(t, c, func(u exec.Update) bool { f, ok := u.(exec.FillUpdate); return ok && f.Fill.OrderID == f1.OrderID })
	// MSFT has no mark/book yet — SimBroker rests a limit order on an unpriced
	// symbol rather than guessing marketability. Seed both so this second
	// sim-1 fill actually happens.
	seedMarketableBook(sims["sim-1"], "MSFT", 100)
	f2 := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 5, LimitPrice: 100})
	waitFor(t, c, func(u exec.Update) bool { f, ok := u.(exec.FillUpdate); return ok && f.Fill.OrderID == f2.OrderID })
	sims["sim-2"].SetMark("AAPL", 100)
	r1 := c.Do(exec.SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 7, LimitPrice: 90}) // rests
	waitFor(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == r1.OrderID && o.Order.Status == exec.StatusAccepted
	})
	c.Do(exec.CancelOrder{Venue: "sim-2", OrderID: r1.OrderID})
	waitFor(t, c, func(u exec.Update) bool {
		o, ok := u.(exec.OrderUpdate)
		return ok && o.Order.ID == r1.OrderID && o.Order.Status == exec.StatusCanceled
	})
	f3 := c.Do(exec.SubmitOrder{Venue: "sim-2", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 3, LimitPrice: 100})
	waitFor(t, c, func(u exec.Update) bool { f, ok := u.(exec.FillUpdate); return ok && f.Fill.OrderID == f3.OrderID })

	// Quiesce: stop Run so reading live state via StateForTest is race-free,
	// then flush the store.
	cancel()
	<-done
	st.Flush()

	// Read the persisted log back and fold it.
	envs, err := st.ReadExecEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	replayed := exec.NewState(venues)
	for _, env := range envs {
		ev, err := exec.DecodeEvent(env.Kind, env.Payload)
		if err != nil {
			t.Fatalf("decode %s: %v", env.Kind, err)
		}
		replayed.Apply(ev)
	}

	live := c.StateForTest()
	// Compare the log-derived view (orders + fills per venue) — NOT
	// positions/account, which are broker-reconciled and never persisted.
	for _, v := range venues {
		if !reflect.DeepEqual(replayed.Venue(v).Orders, live.Venue(v).Orders) {
			t.Fatalf("venue %s orders differ:\n replay=%#v\n live=%#v", v, replayed.Venue(v).Orders, live.Venue(v).Orders)
		}
		if !reflect.DeepEqual(replayed.Venue(v).Fills, live.Venue(v).Fills) {
			t.Fatalf("venue %s fills differ:\n replay=%#v\n live=%#v", v, replayed.Venue(v).Fills, live.Venue(v).Fills)
		}
	}
}
