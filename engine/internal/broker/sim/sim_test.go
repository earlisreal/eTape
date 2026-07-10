package sim

import (
	"context"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/exec"
)

func newSim(t *testing.T) *Broker {
	t.Helper()
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000)
	b.SetMark("AAPL", 100)
	return b
}

// drain reads the next event within a timeout (events are emitted synchronously
// into a buffered channel, so this returns promptly).
func drain(t *testing.T, b *Broker) exec.BrokerEvent {
	t.Helper()
	select {
	case ev := <-b.Events():
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker event")
		return nil
	}
}

func TestSimMarketableLimitFills(t *testing.T) {
	b := newSim(t)
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 100, ClientOrderID: "ET1"}
	ack, err := b.SubmitOrder(context.Background(), req)
	if err != nil || !ack.Accepted {
		t.Fatalf("submit: ack=%+v err=%v", ack, err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("first event should be OrderAccepted")
	}
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Qty != 10 || f.F.Price != 100 || f.LeavesQty != 0 {
		t.Fatalf("expected full fill at 100, got %+v ok=%v", f, ok)
	}
	if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
		t.Fatal("fill should be followed by a BrokerPositions snapshot")
	}
}

func TestSimNonMarketableRestsThenCancel(t *testing.T) {
	b := newSim(t)
	// Buy limit 90 with mark 100 → not marketable → rests.
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("rested order should emit OrderAccepted only")
	}
	if err := b.CancelOrder(context.Background(), "ET1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderCanceled); !ok {
		t.Fatal("cancel should emit OrderCanceled")
	}
	// Canceling an unknown/terminal order errors.
	if err := b.CancelOrder(context.Background(), "ET1"); err == nil {
		t.Fatal("second cancel should error (order gone)")
	}
}

func TestSimSetMarkCrossesRestingOrder(t *testing.T) {
	b := newSim(t)
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 95, ClientOrderID: "ET1"}
	_, _ = b.SubmitOrder(context.Background(), req)
	_ = drain(t, b)       // OrderAccepted
	b.SetMark("AAPL", 94) // mark drops to/through 95 → buy limit 95 now marketable
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 95 {
		t.Fatalf("crossing should fill at limit 95, got %+v ok=%v", f, ok)
	}
	_ = drain(t, b) // BrokerPositions
}

// TestSimSubmitOrder_CarriesSessionIntoInternalOrder guards against a
// dropped-field regression: the internal *exec.Order sim tracks for its own
// bookkeeping (returned verbatim by Snapshot) must carry Session forward from
// the request, same as Core's own OrderSubmitted Order.
func TestSimSubmitOrder_CarriesSessionIntoInternalOrder(t *testing.T) {
	b := newSim(t)
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit,
		Session: exec.SessionExtended, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1",
	})
	_ = drain(t, b) // OrderAccepted
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Session != exec.SessionExtended {
		t.Fatalf("snapshot order dropped Session: %+v", orders)
	}
}

func TestSimReplaceAndSnapshot(t *testing.T) {
	b := newSim(t)
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 10, LimitPrice: 90, ClientOrderID: "ET1"})
	_ = drain(t, b) // OrderAccepted
	if err := b.ReplaceOrder(context.Background(), "ET1", exec.ReplaceRequest{Qty: 20, LimitPrice: 91}); err != nil {
		t.Fatal(err)
	}
	if r, ok := drain(t, b).(exec.OrderReplaced); !ok || r.NewQty != 20 || r.NewLimit != 91 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Qty != 20 || orders[0].LimitPrice != 91 {
		t.Fatalf("snapshot orders wrong: %+v", orders)
	}
	if !b.Capabilities().NativeReplace || !b.Capabilities().FlattenAll {
		t.Fatal("SimBroker should advertise native replace + flatten")
	}
}

// TestNew_SeedsAccountFromStartingCash guards against the boot-balance
// regression: New used to always zero-value acct, so a freshly booted engine
// (Core.Recover -> Broker.Snapshot) showed $0 equity/buying power regardless
// of the configured starting_balance, and only a manual "Reset balance" click
// ever funded the account.
func TestNew_SeedsAccountFromStartingCash(t *testing.T) {
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 75_000)
	acct, _, _, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct.Equity != 75_000 || acct.BuyingPower != 75_000 || acct.AvailableCash != 75_000 || acct.SodEquity != 75_000 {
		t.Fatalf("New should seed the account with startingCash, got %+v", acct)
	}
}

func TestSimMarketOrderNoMarkRejected(t *testing.T) {
	b := New("sim-1", clock.NewFake(time.UnixMilli(1000)), 100_000) // no SetMark call — "MSFT" has no mark
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	ack, err := b.SubmitOrder(context.Background(), req)
	if err != nil || !ack.Accepted {
		t.Fatalf("submit: ack=%+v err=%v", ack, err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("first event should be OrderAccepted")
	}
	r, ok := drain(t, b).(exec.OrderRejected)
	if !ok || r.OID != "ET1" {
		t.Fatalf("market order with no mark should be rejected, got %+v ok=%v", r, ok)
	}
}

func TestSimMarketOrderFillsAtMark(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	ack, err := b.SubmitOrder(context.Background(), req)
	if err != nil || !ack.Accepted {
		t.Fatalf("submit: ack=%+v err=%v", ack, err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("first event should be OrderAccepted")
	}
	f, ok := drain(t, b).(exec.OrderFilled)
	if !ok || f.F.Price != 100 || f.F.Qty != 10 {
		t.Fatalf("market order should fill at the mark, got %+v ok=%v", f, ok)
	}
	if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
		t.Fatal("fill should be followed by a BrokerPositions snapshot")
	}
}

// drainAll reads all currently-buffered broker events without blocking. Named
// distinctly from the existing single-event drain(t, b) helper above, which
// blocks for exactly one event.
func drainAll(ch <-chan exec.BrokerEvent) []exec.BrokerEvent {
	var out []exec.BrokerEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func filledAt(t *testing.T, evs []exec.BrokerEvent) (exec.OrderFilled, bool) {
	t.Helper()
	for _, e := range evs {
		if f, ok := e.(exec.OrderFilled); ok {
			return f, true
		}
	}
	return exec.OrderFilled{}, false
}

func TestSim_Flatten_ZeroesPositions(t *testing.T) {
	b := newSim(t)
	req := exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET1"}
	if _, err := b.SubmitOrder(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	drain(t, b) // OrderAccepted
	drain(t, b) // OrderFilled
	drain(t, b) // BrokerPositions (from the fill)
	if err := b.Flatten(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.BrokerPositions); !ok {
		t.Fatal("Flatten should emit a BrokerPositions reconcile")
	}
	_, pos, _, _ := b.Snapshot(context.Background())
	for _, p := range pos {
		if p.Qty != 0 {
			t.Fatalf("Flatten should zero %s, got %v", p.Symbol, p.Qty)
		}
	}
}

func TestSim_BuyStop_TriggersOnMarkAtOrAboveStop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000)
	b.SetMark("AAPL", 95)
	drainAll(b.Events())
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-bstop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("buy stop must rest while mark (95) < stop (100)")
	}
	b.SetMark("AAPL", 101) // crosses the stop
	f, ok := filledAt(t, drainAll(b.Events()))
	if !ok {
		t.Fatal("buy stop must fill once mark reaches the stop")
	}
	if f.AvgPrice != 101 {
		t.Fatalf("stop-market fills at the mark: got %v want 101", f.AvgPrice)
	}
}

func TestSim_SellStop_TriggersOnMarkAtOrBelowStop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000)
	b.SetMark("AAPL", 105)
	drainAll(b.Events())
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 10, StopPrice: 100, ClientOrderID: "ET-sstop",
	})
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("sell stop must rest while mark (105) > stop (100)")
	}
	b.SetMark("AAPL", 99)
	if f, ok := filledAt(t, drainAll(b.Events())); !ok || f.AvgPrice != 99 {
		t.Fatalf("sell stop should fill at mark 99; ok=%v px=%v", ok, f.AvgPrice)
	}
}

func TestSim_BuyStopLimit_TriggersThenRestsAsLimit(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	b := New("v", clk, 100_000)
	b.SetMark("AAPL", 95)
	drainAll(b.Events())
	// stop 100, limit 100.5 buy: on trigger it is a limit buy @100.5.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "v", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeStopLimit,
		Qty: 10, StopPrice: 100, LimitPrice: 100.5, ClientOrderID: "ET-bsl",
	})
	b.SetMark("AAPL", 102) // triggers (>=100) but 100.5 limit is NOT marketable at 102 -> rests
	if _, ok := filledAt(t, drainAll(b.Events())); ok {
		t.Fatal("stop-limit must not fill above its limit")
	}
	b.SetMark("AAPL", 100) // now 100.5 >= 100 -> marketable
	if f, ok := filledAt(t, drainAll(b.Events())); !ok || f.AvgPrice != 100.5 {
		t.Fatalf("stop-limit should fill at its limit 100.5; ok=%v px=%v", ok, f.AvgPrice)
	}
}

// TestSim_ReplaceOrder_StopNotTriggered_DoesNotFillAtZero reproduces the
// final whole-branch review finding: ReplaceOrder's post-replace fill
// decision used to call the raw marketable(o.Side, o.LimitPrice, mark) check
// instead of actOnMarkLocked. A bare TypeStop has LimitPrice == 0 (it prices
// off StopPrice, not LimitPrice), so marketable(Sell, 0, mark) evaluated as
// 0 <= mark, which is true for any positive mark -- a resting sell stop that
// got replaced would fill IMMEDIATELY at price $0, regardless of whether the
// stop had actually triggered. This asserts a resting stop that has NOT
// triggered stays resting (and unfilled) across a replace.
func TestSim_ReplaceOrder_StopNotTriggered_DoesNotFillAtZero(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStop,
		Qty: 10, StopPrice: 90, ClientOrderID: "ET-stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("resting stop (mark 100 > stop 90, not triggered) should emit OrderAccepted only")
	}

	if err := b.ReplaceOrder(context.Background(), "ET-stop", exec.ReplaceRequest{Qty: 20}); err != nil {
		t.Fatal(err)
	}
	r, ok := drain(t, b).(exec.OrderReplaced)
	if !ok || r.NewQty != 20 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	select {
	case e := <-b.Events():
		t.Fatalf("stop order that hasn't triggered must not fill on replace, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}

	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Qty != 20 || orders[0].Type != exec.TypeStop {
		t.Fatalf("stop order should remain resting with the new qty and its type: %+v", orders)
	}
}

// TestSim_ReplaceOrder_StopLimitNotTriggered_RawMarketableWouldWronglyFill
// covers the StopLimit half of the same finding: a resting stop-limit whose
// limit price already happens to be "marketable" against the current mark
// must still NOT fill on replace if its stop has not actually triggered.
// The raw marketable(...) check the old code used cannot see the stop at
// all; only actOnMarkLocked's Stop/StopLimit branch evaluates the trigger
// before ever considering the limit.
func TestSim_ReplaceOrder_StopLimitNotTriggered_RawMarketableWouldWronglyFill(t *testing.T) {
	b := newSim(t) // seeds AAPL mark = 100
	_, err := b.SubmitOrder(context.Background(), exec.OrderRequest{
		Venue: "sim-1", Symbol: "AAPL", Side: exec.SideSell, Type: exec.TypeStopLimit,
		Qty: 10, StopPrice: 40, LimitPrice: 50, ClientOrderID: "ET-sl",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := drain(t, b).(exec.OrderAccepted); !ok {
		t.Fatal("resting stop-limit (mark 100 > stop 40, not triggered) should emit OrderAccepted only")
	}

	if err := b.ReplaceOrder(context.Background(), "ET-sl", exec.ReplaceRequest{Qty: 15, LimitPrice: 50}); err != nil {
		t.Fatal(err)
	}
	r, ok := drain(t, b).(exec.OrderReplaced)
	if !ok || r.NewQty != 15 {
		t.Fatalf("replace event wrong: %+v ok=%v", r, ok)
	}
	// Raw marketable(Sell, limit=50, mark=100) is true (50 <= 100) -- exactly
	// the buggy shortcut this fix removes -- but the stop (40) has not
	// actually triggered at mark 100, so the order must stay resting.
	select {
	case e := <-b.Events():
		t.Fatalf("stop-limit whose stop hasn't triggered must not fill just because its limit is marketable, got %+v", e)
	case <-time.After(100 * time.Millisecond):
	}

	_, _, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Type != exec.TypeStopLimit {
		t.Fatalf("stop-limit order should remain resting as StopLimit (not triggered): %+v", orders)
	}
}

func TestSim_ResetBalance_CancelsFlattensAndSetsAccount(t *testing.T) {
	b := newSim(t)
	// A resting order that should be canceled.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 5, LimitPrice: 90, ClientOrderID: "ET1"})
	drain(t, b) // OrderAccepted (rests: mark unset for MSFT, but limit path always accepts+rests without a mark)

	// A filled position that should be flattened.
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeMarket, Qty: 10, ClientOrderID: "ET2"})
	drain(t, b) // OrderAccepted
	drain(t, b) // OrderFilled
	drain(t, b) // BrokerPositions (from the fill)

	if err := b.ResetBalance(context.Background(), 50_000); err != nil {
		t.Fatal(err)
	}

	evs := drainAll(b.Events())
	var sawCancel, sawPositions, sawAccount bool
	for _, e := range evs {
		switch ev := e.(type) {
		case exec.OrderCanceled:
			if ev.OID != "ET1" {
				t.Fatalf("unexpected cancel: %+v", ev)
			}
			sawCancel = true
		case exec.BrokerPositions:
			sawPositions = true
		case exec.BrokerAccount:
			sawAccount = true
			a := ev.Account
			if a.Equity != 50_000 || a.BuyingPower != 50_000 || a.AvailableCash != 50_000 || a.SodEquity != 50_000 {
				t.Fatalf("account not reset to starting cash: %+v", a)
			}
			if a.Realized != 0 || a.DayPnL != 0 {
				t.Fatalf("realized/day-pnl should reset to zero: %+v", a)
			}
		}
	}
	if !sawCancel || !sawPositions || !sawAccount {
		t.Fatalf("expected cancel+positions+account events, got %+v", evs)
	}

	_, pos, orders, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("expected no resting orders after reset, got %+v", orders)
	}
	for _, p := range pos {
		if p.Qty != 0 {
			t.Fatalf("expected flat positions after reset, got %+v", p)
		}
	}
}

func TestSim_ResetBalance_AdvertisedCapability(t *testing.T) {
	b := newSim(t)
	if !b.Capabilities().ResetBalance {
		t.Fatal("SimBroker should advertise ResetBalance capability")
	}
}

func TestSimCancelAll(t *testing.T) {
	b := newSim(t)
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 1, LimitPrice: 90, ClientOrderID: "ET1"})
	_, _ = b.SubmitOrder(context.Background(), exec.OrderRequest{Venue: "sim-1", Symbol: "MSFT", Side: exec.SideBuy, Type: exec.TypeLimit, Qty: 1, LimitPrice: 90, ClientOrderID: "ET2"})
	_, _ = drain(t, b), drain(t, b) // two OrderAccepted
	if err := b.CancelAll(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	got[drain(t, b).(exec.OrderCanceled).OID] = true
	got[drain(t, b).(exec.OrderCanceled).OID] = true
	if !got["ET1"] || !got["ET2"] {
		t.Fatalf("cancel-all should cancel both, got %v", got)
	}
}
