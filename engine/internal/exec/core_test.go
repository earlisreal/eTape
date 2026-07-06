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
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/broker/sim"
	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
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
