// Package exec_test is an external test package: it exercises Core through the
// exported API only. It must be external (not "package exec") because it
// imports broker/sim, and sim imports exec — an internal ("package exec") test
// file importing a package that imports exec back is a real import cycle to
// the Go toolchain, even though this file only touches exec's exported surface.
package exec_test

import (
	"context"
	"errors"
	"math/rand"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// failingAppendStore forces every AppendExecEvent call to fail, to verify the
// append-blocks-submit safety property empirically rather than by inspection.
type failingAppendStore struct{ exec.EventStore }

func (failingAppendStore) AppendExecEvent(env exec.EventEnvelope, fill *exec.FillRow) (int64, error) {
	return 0, errors.New("simulated append failure")
}

func newTestCore(t *testing.T, venues ...exec.VenueID) (*exec.Core, map[exec.VenueID]*sim.Broker, context.CancelFunc) {
	t.Helper()
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	brokers := map[exec.VenueID]exec.Broker{}
	sims := map[exec.VenueID]*sim.Broker{}
	for _, v := range venues {
		b := sim.New(v, clk)
		b.SetMark("AAPL", 100)
		brokers[v] = b
		sims[v] = b
	}
	cfg := exec.CoreConfig{
		Venues: venues,
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
			Venue:  map[exec.VenueID]exec.VenueLimits{},
		},
		Store: st, Brokers: brokers, Clock: clk,
		IDGen: exec.NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
	}
	for _, v := range venues {
		cfg.Gate.Venue[v] = exec.VenueLimits{MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}
	}
	c := exec.NewCore(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Recover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { _ = c.Run(ctx) }()
	t.Cleanup(cancel)
	return c, sims, cancel
}

// waitFor reads Updates until pred returns true or a timeout elapses.
func waitFor(t *testing.T, c *exec.Core, pred func(exec.Update) bool) exec.Update {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case u := <-c.Updates():
			if pred(u) {
				return u
			}
		case <-deadline:
			t.Fatal("timed out waiting for expected update")
			return nil
		}
	}
}

func TestCoreDisarmedBlocks(t *testing.T) {
	c, _, _ := newTestCore(t, "sim-1")
	ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	if ack.Accepted || ack.Reason != "master disarmed" {
		t.Fatalf("disarmed submit should block, got %+v", ack)
	}
	// A blocked order still emits an OrderUpdate with StatusBlocked.
	u := waitFor(t, c, func(u exec.Update) bool { _, ok := u.(exec.OrderUpdate); return ok })
	if u.(exec.OrderUpdate).Order.Status != exec.StatusBlocked {
		t.Fatalf("expected blocked order update, got %+v", u)
	}
}

func TestCoreArmSubmitFill(t *testing.T) {
	c, _, _ := newTestCore(t, "sim-1")
	if ack := c.Do(exec.Arm{}); !ack.Accepted { // master
		t.Fatalf("master arm: %+v", ack)
	}
	if ack := c.Do(exec.Arm{Venue: "sim-1"}); !ack.Accepted {
		t.Fatalf("venue arm: %+v", ack)
	}
	ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	if !ack.Accepted {
		t.Fatalf("armed submit should be accepted, got %+v", ack)
	}
	// SimBroker fills the marketable limit (mark=100, limit=100).
	fu := waitFor(t, c, func(u exec.Update) bool { _, ok := u.(exec.FillUpdate); return ok }).(exec.FillUpdate)
	if fu.Fill.Qty != 10 || fu.Fill.Price != 100 || fu.Fill.OrderID != ack.OrderID {
		t.Fatalf("fill wrong: %+v", fu.Fill)
	}
	// The broker's position snapshot lands as a PositionUpdate.
	pu := waitFor(t, c, func(u exec.Update) bool {
		p, ok := u.(exec.PositionUpdate)
		return ok && p.Position.Symbol == "AAPL"
	}).(exec.PositionUpdate)
	if pu.Position.Qty != 10 {
		t.Fatalf("position qty = %v, want 10", pu.Position.Qty)
	}
}

// TestCoreAppendFailureBlocksSubmit verifies the append-blocks-submit safety
// property empirically: when the event store fails to persist OrderSubmitted,
// handleSubmit must return a blocked ack and must not dispatch the broker POST.
func TestCoreAppendFailureBlocksSubmit(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	realStore, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "fail.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = realStore.Close() }()
	b := sim.New("sim-1", clk)
	b.SetMark("AAPL", 100)
	cfg := exec.CoreConfig{
		Venues: []exec.VenueID{"sim-1"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
			Venue:  map[exec.VenueID]exec.VenueLimits{"sim-1": {MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
		},
		Store:   failingAppendStore{EventStore: realStore},
		Brokers: map[exec.VenueID]exec.Broker{"sim-1": b},
		Clock:   clk,
		IDGen:   exec.NewOrderIDGen(clk, rand.New(rand.NewSource(4))),
	}
	c := exec.NewCore(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = c.Run(ctx) }()
	c.Do(exec.Arm{})
	c.Do(exec.Arm{Venue: "sim-1"})
	ack := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	if ack.Accepted {
		t.Fatalf("submit with a failing append should be blocked, got %+v", ack)
	}
	if ack.Reason == "" {
		t.Fatalf("blocked ack should carry a reason, got %+v", ack)
	}
}

// capStub is a minimal fake exec.Broker used only to exercise the
// Capabilities.FlattenAll-gated reject branch of Core.handleFlatten: SimBroker
// always reports FlattenAll:true, so it can never take that branch. capStub
// implements every exec.Broker method faithfully (the compiler enforces all
// of them); only Capabilities and Flatten do anything interesting.
type capStub struct {
	flatten      bool // value Capabilities().FlattenAll reports
	resetBalance bool // value Capabilities().ResetBalance reports

	mu          sync.Mutex
	called      bool
	resetCalled bool
	resetAmount float64
	ev          chan exec.BrokerEvent
}

var _ exec.Broker = (*capStub)(nil)

func (c *capStub) Capabilities() exec.Capabilities {
	return exec.Capabilities{FlattenAll: c.flatten, ResetBalance: c.resetBalance}
}

func (c *capStub) SubmitOrder(context.Context, exec.OrderRequest) (exec.OrderAck, error) {
	return exec.OrderAck{}, errors.New("capStub: not implemented")
}

func (c *capStub) ReplaceOrder(context.Context, string, exec.ReplaceRequest) error {
	return errors.New("capStub: not implemented")
}

func (c *capStub) CancelOrder(context.Context, string) error {
	return errors.New("capStub: not implemented")
}

func (c *capStub) CancelAll(context.Context, string) error {
	return errors.New("capStub: not implemented")
}

func (c *capStub) Snapshot(context.Context) (exec.AccountSnapshot, []exec.Position, []exec.Order, error) {
	return exec.AccountSnapshot{}, nil, nil, nil
}

func (c *capStub) Events() <-chan exec.BrokerEvent { return c.ev }

func (c *capStub) Flatten(context.Context) error {
	c.mu.Lock()
	c.called = true
	c.mu.Unlock()
	return nil
}

func (c *capStub) ResetBalance(_ context.Context, amount float64) error {
	c.mu.Lock()
	c.resetCalled = true
	c.resetAmount = amount
	c.mu.Unlock()
	return nil
}

// flattenCalled polls briefly for Flatten to have been invoked: handleFlatten
// dispatches it from a goroutine, so the caller can't assume it has already
// run the instant Do returns.
func (c *capStub) flattenCalled() bool {
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		called := c.called
		c.mu.Unlock()
		if called {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// resetBalanceCalled polls briefly for ResetBalance to have been invoked:
// handleResetBalance dispatches it from a goroutine, same as flattenCalled.
func (c *capStub) resetBalanceCalled() bool {
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		called := c.resetCalled
		c.mu.Unlock()
		if called {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// buildCoreWith wires a single-venue Core ("v") around cs, a capStub broker,
// for tests that need to control Capabilities directly rather than through a
// real sim.Broker (which always reports FlattenAll:true). startingBalance may
// be nil (tests that don't exercise ResetBalance amounts).
func buildCoreWith(t *testing.T, b *capStub, startingBalance map[exec.VenueID]float64) (*exec.Core, *capStub) {
	t.Helper()
	b.ev = make(chan exec.BrokerEvent)
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	st, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e.db"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := exec.CoreConfig{
		Venues: []exec.VenueID{"v"},
		Gate: exec.GateConfig{
			Global: exec.GlobalLimits{MaxDayLoss: 1000, MaxSymbolPositionShares: 1000, MaxSymbolPositionValue: 1_000_000},
			Venue:  map[exec.VenueID]exec.VenueLimits{"v": {MaxOrderValue: 100000, MaxPositionValue: 1_000_000, MaxPositionShares: 1000, MaxOpenOrders: 10}},
		},
		Store:           st,
		Brokers:         map[exec.VenueID]exec.Broker{"v": b},
		Clock:           clk,
		IDGen:           exec.NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
		StartingBalance: startingBalance,
	}
	c := exec.NewCore(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Recover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { _ = c.Run(ctx) }()
	t.Cleanup(cancel)
	return c, b
}

// drainUntilOrderFilled reads Updates until it sees an OrderUpdate for
// orderID with Status Filled, returning every Update observed along the way
// (including that OrderUpdate). emitForEvent pushes FillUpdate, then any
// TradeUpdate(s), then OrderUpdate for the same fill event in that exact
// sequence on Core's single-writer updates channel, so collecting through
// the OrderUpdate is guaranteed to capture any TradeUpdate the fill produced.
func drainUntilOrderFilled(t *testing.T, c *exec.Core, orderID string) []exec.Update {
	t.Helper()
	var seen []exec.Update
	deadline := time.After(2 * time.Second)
	for {
		select {
		case u := <-c.Updates():
			seen = append(seen, u)
			if ou, ok := u.(exec.OrderUpdate); ok && ou.Order.ID == orderID && ou.Order.Status == exec.StatusFilled {
				return seen
			}
		case <-deadline:
			t.Fatal("timed out waiting for order fill")
			return nil
		}
	}
}

// TestCoreEmitsTradeUpdateOnRoundTripClose exercises the emitForEvent wiring
// (not RoundTripAggregator.Apply directly, which Task 1 already covers): a
// live BUY fill opens a position, no TradeUpdate follows; the SELL fill that
// closes it back to flat must produce exactly one TradeUpdate carrying the
// realized round trip.
func TestCoreEmitsTradeUpdateOnRoundTripClose(t *testing.T) {
	c, _, _ := newTestCore(t, "sim-1")
	if ack := c.Do(exec.Arm{}); !ack.Accepted { // master
		t.Fatalf("master arm: %+v", ack)
	}
	if ack := c.Do(exec.Arm{Venue: "sim-1"}); !ack.Accepted {
		t.Fatalf("venue arm: %+v", ack)
	}

	buyAck := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	if !buyAck.Accepted {
		t.Fatalf("buy submit: %+v", buyAck)
	}
	opening := drainUntilOrderFilled(t, c, buyAck.OrderID)
	for _, u := range opening {
		if _, ok := u.(exec.TradeUpdate); ok {
			t.Fatalf("opening fill from flat must not close a trade, got %+v", u)
		}
	}

	sellAck := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	if !sellAck.Accepted {
		t.Fatalf("sell submit: %+v", sellAck)
	}
	closing := drainUntilOrderFilled(t, c, sellAck.OrderID)
	var trades []exec.ClosedTrade
	for _, u := range closing {
		if tu, ok := u.(exec.TradeUpdate); ok {
			trades = append(trades, tu.Trade)
		}
	}
	if len(trades) != 1 {
		t.Fatalf("expected exactly one TradeUpdate on full close, got %d: %+v", len(trades), trades)
	}
	tr := trades[0]
	if !tr.IsLong || tr.Qty != 10 || tr.EntryPrice != 100 || tr.ExitPrice != 100 || tr.Realized != 0 {
		t.Fatalf("closed trade wrong: %+v", tr)
	}
}

// TestCoreNoTradeUpdateOnPartialClose verifies a fill that only partially
// unwinds a position (trip stays open) does not emit a TradeUpdate.
func TestCoreNoTradeUpdateOnPartialClose(t *testing.T) {
	c, _, _ := newTestCore(t, "sim-1")
	if ack := c.Do(exec.Arm{}); !ack.Accepted { // master
		t.Fatalf("master arm: %+v", ack)
	}
	if ack := c.Do(exec.Arm{Venue: "sim-1"}); !ack.Accepted {
		t.Fatalf("venue arm: %+v", ack)
	}

	buyAck := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 10, LimitPrice: 100})
	if !buyAck.Accepted {
		t.Fatalf("buy submit: %+v", buyAck)
	}
	drainUntilOrderFilled(t, c, buyAck.OrderID)

	sellAck := c.Do(exec.SubmitOrder{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeLimit, TIF: exec.TIFDay, Qty: 5, LimitPrice: 100})
	if !sellAck.Accepted {
		t.Fatalf("sell submit: %+v", sellAck)
	}
	partial := drainUntilOrderFilled(t, c, sellAck.OrderID)
	for _, u := range partial {
		if tu, ok := u.(exec.TradeUpdate); ok {
			t.Fatalf("partial close must not emit a TradeUpdate, got %+v", tu)
		}
	}
}

// fakeEventStore is a configurable exec.EventStore double for the
// seedTrades/Recover tests below: QueryFillsSince returns canned fills (or a
// canned error) and records every fromMs it was called with, with no real DB
// involved. AppendExecEvent/ReadExecEventsSince are not exercised by these
// tests (Recover's event-replay path only needs ReadExecEventsSince to
// return successfully) so they're trivial stubs.
type fakeEventStore struct {
	fills    []exec.FillRow
	fillsErr error

	sinceCalls []int64
}

func (f *fakeEventStore) AppendExecEvent(exec.EventEnvelope, *exec.FillRow) (int64, error) {
	return 0, nil
}

func (f *fakeEventStore) ReadExecEventsSince(int64) ([]exec.EventEnvelope, error) {
	return nil, nil
}

func (f *fakeEventStore) QueryFillsSince(_ context.Context, fromMs int64) ([]exec.FillRow, error) {
	f.sinceCalls = append(f.sinceCalls, fromMs)
	if f.fillsErr != nil {
		return nil, f.fillsErr
	}
	return f.fills, nil
}

var _ exec.EventStore = (*fakeEventStore)(nil)

// newSeedTestCore builds a brokerless Core (Recover's per-venue snapshot loop
// is a no-op when a venue has no configured Broker) around fs, so Recover
// exercises only the event-replay + seedTrades paths these tests care about.
func newSeedTestCore(t *testing.T, clk clock.Clock, fs *fakeEventStore) *exec.Core {
	t.Helper()
	cfg := exec.CoreConfig{
		Venues: []exec.VenueID{"sim-1"},
		Store:  fs,
		Clock:  clk,
	}
	return exec.NewCore(cfg)
}

// TestCoreSeedTradesRebuildsTodayRoundTrips exercises Recover's boot-time
// seedTrades call end to end: a fake EventStore returns fills that fully
// close a round trip, and Recover must feed them through the
// RoundTripAggregator and emit the resulting TradeUpdate — the same path a
// restart mid-day relies on to not lose Trade History.
func TestCoreSeedTradesRebuildsTodayRoundTrips(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	open := clk.Now().UnixMilli()
	fs := &fakeEventStore{fills: []exec.FillRow{
		{OrderID: "o1", Symbol: "AAPL", Side: "BUY", Qty: 10, Price: 100, TsMs: open, Venue: "sim-1"},
		{OrderID: "o2", Symbol: "AAPL", Side: "SELL", Qty: 10, Price: 105, TsMs: open + 1000, Venue: "sim-1"},
	}}
	c := newSeedTestCore(t, clk, fs)
	if err := c.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	select {
	case u := <-c.Updates():
		tu, ok := u.(exec.TradeUpdate)
		if !ok {
			t.Fatalf("expected TradeUpdate, got %T: %+v", u, u)
		}
		if tu.Trade.Symbol != "AAPL" || tu.Trade.Venue != "sim-1" || !tu.Trade.IsLong ||
			tu.Trade.Qty != 10 || tu.Trade.EntryPrice != 100 || tu.Trade.ExitPrice != 105 || tu.Trade.Realized != 50 {
			t.Fatalf("seeded trade wrong: %+v", tu.Trade)
		}
	default:
		t.Fatal("expected seedTrades to emit a TradeUpdate during Recover")
	}
}

// TestCoreSeedTradesUsesPoolDayBoundary asserts seedTrades queries fills from
// the 20:00-ET pool-day anchor (session.PoolDay), not the ET-midnight
// boundary Recover's event replay uses for orders — the two windows are
// deliberately different, and this is the boundary computation itself, not a
// tautology: it fails if seedTrades regresses to session.DayMs or any other
// anchor.
func TestCoreSeedTradesUsesPoolDayBoundary(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	fs := &fakeEventStore{}
	c := newSeedTestCore(t, clk, fs)
	if err := c.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(fs.sinceCalls) != 1 {
		t.Fatalf("expected exactly one QueryFillsSince call, got %d", len(fs.sinceCalls))
	}
	want := session.PoolDay(clk.Now()) * 1000
	if fs.sinceCalls[0] != want {
		t.Fatalf("QueryFillsSince fromMs = %d, want session.PoolDay-anchored %d", fs.sinceCalls[0], want)
	}
}

// TestCoreSeedTradesAbortsCleanlyOnCtxCancel verifies the ctx-cancel guard:
// Recover called with an already-canceled context must not call
// QueryFillsSince at all (the ctx-unaware seed shutdown deadlock this design
// avoids) and must still return nil rather than propagating the cancellation
// as a boot error.
func TestCoreSeedTradesAbortsCleanlyOnCtxCancel(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	fs := &fakeEventStore{fills: []exec.FillRow{
		{OrderID: "o1", Symbol: "AAPL", Side: "BUY", Qty: 10, Price: 100, TsMs: clk.Now().UnixMilli(), Venue: "sim-1"},
	}}
	c := newSeedTestCore(t, clk, fs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Recover(ctx); err != nil {
		t.Fatalf("Recover with canceled ctx should still return nil, got %v", err)
	}
	if len(fs.sinceCalls) != 0 {
		t.Fatalf("expected seedTrades to skip QueryFillsSince on canceled ctx, got calls %+v", fs.sinceCalls)
	}
	select {
	case u := <-c.Updates():
		t.Fatalf("expected no TradeUpdate when seedTrades aborts on ctx-cancel, got %+v", u)
	default:
	}
}

// TestCoreSeedTradesLogsUnparseableSide covers the should-never-happen skip
// path in seedTrades: a fill whose Side string doesn't parse (data
// corruption, since Side strings are only ever written by the engine's own
// Side.String()) must still be skipped, but must no longer vanish silently —
// it must surface via c.syslog with enough detail (the raw Side value plus
// symbol/venue) to diagnose. Without this, a skipped opening fill makes a
// later closing fill look like it opens a position from flat, producing a
// plausible-but-wrong closed trade with zero diagnostic trail (whole-branch
// review finding).
func TestCoreSeedTradesLogsUnparseableSide(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	fs := &fakeEventStore{fills: []exec.FillRow{
		{OrderID: "o1", Symbol: "AAPL", Side: "GARBAGE", Qty: 10, Price: 100, TsMs: clk.Now().UnixMilli(), Venue: "sim-1"},
	}}
	var kinds, details []string
	cfg := exec.CoreConfig{
		Venues: []exec.VenueID{"sim-1"},
		Store:  fs,
		Clock:  clk,
		SysLog: func(kind, detail string) {
			kinds = append(kinds, kind)
			details = append(details, detail)
		},
	}
	c := exec.NewCore(cfg)
	if err := c.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	found := false
	for i, k := range kinds {
		if k == "exec.recover" && strings.Contains(details[i], "GARBAGE") && strings.Contains(details[i], "AAPL") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a syslog call with kind=exec.recover naming the unparseable Side and symbol, got kinds=%v details=%v", kinds, details)
	}
	select {
	case u := <-c.Updates():
		t.Fatalf("expected no TradeUpdate for an unparseable-Side fill, got %+v", u)
	default:
	}
}

func TestCore_Flatten_RequiresFlattenCapability(t *testing.T) {
	// A venue whose broker advertises FlattenAll=false must reject Flatten.
	c, _ := buildCoreWith(t, &capStub{flatten: false}, nil)
	if ack := c.Do(exec.Flatten{Venue: "v"}); ack.Accepted {
		t.Fatal("Flatten must be rejected when FlattenAll is false")
	}
	// FlattenAll=true is accepted.
	c2, b := buildCoreWith(t, &capStub{flatten: true}, nil)
	if ack := c2.Do(exec.Flatten{Venue: "v"}); !ack.Accepted {
		t.Fatalf("Flatten should be accepted: %q", ack.Reason)
	}
	if !b.flattenCalled() {
		t.Fatal("Core should have invoked Broker.Flatten")
	}
}

func TestCore_ResetBalance_RequiresResetBalanceCapability(t *testing.T) {
	// A venue whose broker advertises ResetBalance=false must reject it.
	c, _ := buildCoreWith(t, &capStub{resetBalance: false}, nil)
	if ack := c.Do(exec.ResetBalance{Venue: "v"}); ack.Accepted {
		t.Fatal("ResetBalance must be rejected when ResetBalance capability is false")
	}
	// ResetBalance=true is accepted, and Core passes the venue's configured
	// starting balance (baked in at boot), not a value from the command.
	c2, b := buildCoreWith(t, &capStub{resetBalance: true}, map[exec.VenueID]float64{"v": 50_000})
	if ack := c2.Do(exec.ResetBalance{Venue: "v"}); !ack.Accepted {
		t.Fatalf("ResetBalance should be accepted: %q", ack.Reason)
	}
	if !b.resetBalanceCalled() {
		t.Fatal("Core should have invoked Broker.ResetBalance")
	}
	if b.resetAmount != 50_000 {
		t.Fatalf("Core should pass the venue's configured starting balance, got %v", b.resetAmount)
	}
}

func TestCore_ResetBalance_UnknownVenue(t *testing.T) {
	c, _ := buildCoreWith(t, &capStub{resetBalance: true}, nil)
	if ack := c.Do(exec.ResetBalance{Venue: "ghost"}); ack.Accepted {
		t.Fatal("ResetBalance on an unknown venue must be rejected")
	}
}
