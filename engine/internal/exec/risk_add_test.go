package exec

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func filledRiskPosition(t *testing.T, protection *TrailConfig) (*Core, *clock.Fake, *heldTestBroker, context.Context, Order, Order) {
	t.Helper()
	c, clk, b, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.5, Mode: "Dollar", Value: 50, MaxQty: 100, Protection: protection})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing original buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 100 })
	return c, clk, b, ctx, entry, stop
}

func riskAddCommand(owner Order) SubmitRiskEntry {
	return SubmitRiskEntry{Venue: owner.Venue, Symbol: owner.Symbol, SharedEntryID: owner.ID, BuyStop: 10.5, SellStop: 10, Mode: "Dollar", Value: 25, MaxQty: 50}
}

func TestRiskAddFirstFillAndPartialSales(t *testing.T) {
	c, clk, b, ctx, owner, stop := filledRiskPosition(t, nil)
	cm := riskAddCommand(owner)
	cm.SellCushion = LimitCushion{Value: 1, Unit: "$"}
	ack := c.Do(cm)
	if !ack.Accepted {
		t.Fatal(ack)
	}
	add := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	if add.Qty != 50 || add.RiskEntry.StopID != stop.ID || add.RiskEntry.SharedEntryID != owner.ID || add.RiskEntry.SellCushion.Value != 0 {
		t.Fatalf("add sizing/ownership: %+v", add)
	}
	unchanged := waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 && o.StopPrice == 9.5 })
	if unchanged.ID != stop.ID {
		t.Fatal("stop replaced")
	}
	if dup := c.Do(cm); dup.Accepted || !strings.Contains(dup.Reason, "one pending") {
		t.Fatalf("second pending add: %+v", dup)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case req := <-b.submits:
		if req.Qty != 50 || req.Side != SideBuy {
			t.Fatal(req)
		}
	case <-time.After(time.Second):
		t.Fatal("missing add buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: add.ID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: add.ID, Symbol: "AAPL", Side: SideBuy, Qty: 25, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 25, LeavesQty: 25, AvgPrice: 10.5}
	protected := waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 125 && o.StopPrice == 10 })
	if protected.ID != stop.ID {
		t.Fatal("add created another stop")
	}
	if dup := c.Do(cm); dup.Accepted || !strings.Contains(dup.Reason, "one pending") {
		t.Fatalf("partially filled add admitted another buy: %+v", dup)
	}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: add.ID, Symbol: "AAPL", Side: SideBuy, Qty: 25, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 50, AvgPrice: 10.5}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 150 })
	b.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 120, AvgPrice: 10.1}}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 120 })
	b.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 120, AvgPrice: 10.1}}
	cm.BuyStop, cm.SellStop, cm.Value, cm.MaxQty = 11, 10, 20, 20
	if next := c.Do(cm); !next.Accepted {
		t.Fatalf("next add after partial sale: %+v", next)
	}
}

func TestRiskAddRejectsLowerStopAndStaleOwner(t *testing.T) {
	c, _, _, _, owner, _ := filledRiskPosition(t, nil)
	cm := riskAddCommand(owner)
	cm.SellStop = 9.4
	if ack := c.Do(cm); ack.Accepted || !strings.Contains(ack.Reason, "keep or raise") {
		t.Fatal(ack)
	}
	cm.SellStop, cm.SharedEntryID = 10, ""
	if ack := c.Do(cm); ack.Accepted || !strings.Contains(ack.Reason, "changed") {
		t.Fatal(ack)
	}
}

func TestRiskAddCancelBeforeFillsPreservesStop(t *testing.T) {
	c, _, _, _, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.StopPrice == 9.5 })
	if cancel := c.Do(CancelOrder{Venue: "v", OrderID: ack.OrderID}); !cancel.Accepted {
		t.Fatal(cancel)
	}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Working() && o.Qty == 100 && o.StopPrice == 9.5 })
	if next := c.Do(riskAddCommand(owner)); !next.Accepted {
		t.Fatalf("add after canceled buy: %+v", next)
	}
}

func TestRiskAddPreservesTrailingProgressAndHigherStop(t *testing.T) {
	c, clk, b, ctx, owner, stop := filledRiskPosition(t, &TrailConfig{Mode: "PRICE", Value: .5, Pricing: "CUSHION"})
	cm := riskAddCommand(owner)
	cm.SellStop = 9.8
	cm.Protection = &TrailConfig{Mode: "PERCENT", Value: 20, Pricing: "CUSHION"}
	ack := c.Do(cm)
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing add")
	}
	b.ev <- OrderAccepted{V: "v", OID: ack.OrderID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 10, LeavesQty: 25, AvgPrice: 10.5}
	got := waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 110 && o.Held.Trail.Stop >= 10 })
	if got.Held.Trail.Config.Mode != "PRICE" || got.Held.Trail.Watermark != 10.5 || got.StopPrice != 10 || got.Held.Trail.Floor != 10 {
		t.Fatalf("trail reset or loosened: %+v", got.Held.Trail)
	}
}

func TestRiskAddStopCancelsPendingBuyAndProtectsLateFill(t *testing.T) {
	c, clk, b, ctx, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing add buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: ack.OrderID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: 25, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 25, LeavesQty: 25, AvgPrice: 10.5}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 125 && o.StopPrice == 10 })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(4, clk.Now(), 10))
	select {
	case req := <-b.submits:
		if req.Side != SideSell || req.Qty != 125 {
			t.Fatal(req)
		}
	case <-time.After(time.Second):
		t.Fatal("missing shared exit")
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	// A raced fill after cancel still needs its own cleanup, not a larger main exit.
	b.ev <- OrderAccepted{V: "v", OID: stop.ID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: 5, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 30, AvgPrice: 10.5}
	select {
	case req := <-b.submits:
		if req.Side != SideSell || req.Qty != 5 {
			t.Fatalf("late fill duplicated original shares: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("missing late fill cleanup")
	}
}

func TestRiskAddCancelAfterPartialFillKeepsCombinedProtection(t *testing.T) {
	c, clk, b, ctx, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: ack.OrderID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: 25, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 25, LeavesQty: 25, AvgPrice: 10.5}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 125 })
	if cancel := c.Do(CancelOrder{Venue: "v", OrderID: ack.OrderID}); !cancel.Accepted {
		t.Fatal(cancel)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 125 && o.StopPrice == 10 && o.Working() })
	cm := riskAddCommand(owner)
	cm.BuyStop = 11
	if next := c.Do(cm); !next.Accepted {
		t.Fatalf("next add after canceled remainder: %+v", next)
	}
}

func TestRiskAddManualFlatCancelsPendingEntry(t *testing.T) {
	c, clk, b, ctx, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	b.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 0}}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Status == StatusCanceled })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case req := <-b.submits:
		t.Fatalf("flat cycle reopened by pending add: %+v", req)
	default:
	}
}

func TestRiskAddAmendmentCapsReviewedSharesAndLeavesStopUntouched(t *testing.T) {
	c, _, _, _, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	add := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 && o.StopPrice == 9.5 })
	edit := c.Do(ReplaceOrder{Venue: "v", OrderID: add.ID, StopPrice: 10.4, Qty: 500.5, ExpectedHeldPhase: add.Held.Phase, ExpectedRiskEntryPhase: "WAITING"})
	if !edit.Accepted {
		t.Fatal(edit)
	}
	got := waitOrder(t, c, add.ID, func(o Order) bool { return o.StopPrice == 10.4 })
	if got.Qty != 50 {
		t.Fatalf("add amendment grew reviewed quantity: %+v", got)
	}
	if cancel := c.Do(CancelOrder{Venue: "v", OrderID: add.ID}); !cancel.Accepted {
		t.Fatal(cancel)
	}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 && o.StopPrice == 9.5 })
}

func TestRiskAddStillEnforcesCombinedPositionCap(t *testing.T) {
	c, _, _, _, owner, _ := filledRiskPosition(t, nil)
	cm := riskAddCommand(owner)
	cm.Value, cm.MaxQty = 500, 1000
	if ack := c.Do(cm); ack.Accepted || !strings.Contains(ack.Reason, "position exceeds share cap") {
		t.Fatal(ack)
	}
}

func TestRiskAddRejectKeepsOriginalProtection(t *testing.T) {
	c, clk, b, ctx, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing buy")
	}
	b.ev <- OrderRejected{V: "v", OID: ack.OrderID, Reason: "buy rejected", Ts: clk.Now().UnixMilli()}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusRejected })
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 && o.StopPrice == 9.5 && o.Working() })
	if next := c.Do(riskAddCommand(owner)); !next.Accepted {
		t.Fatalf("add after rejected buy: %+v", next)
	}
}

func TestRiskAddKeepsSizingRecipeStableWhilePending(t *testing.T) {
	c, _, _, _, owner, stop := filledRiskPosition(t, &TrailConfig{Mode: "PRICE", Value: .5, Pricing: "CUSHION"})
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	config := stop.Held.Trail.Config
	config.Cushion = LimitCushion{Value: .1, Unit: "$"}
	edit := c.Do(EditTrail{Venue: "v", OrderID: stop.ID, ExpectedHeldPhase: stop.Held.Phase, ExpectedStop: stop.StopPrice, Config: config})
	if edit.Accepted || !strings.Contains(edit.Reason, "cancel pending add") {
		t.Fatalf("pending add changed execution recipe: %+v", edit)
	}
}

func TestRiskAddRecoveryKeepsSharedLinkAndPendingStop(t *testing.T) {
	c, clk, b, _, owner, stop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	cold := NewCore(CoreConfig{Venues: []VenueID{"v"}, Store: c.store, Brokers: map[VenueID]Broker{"v": b}, Clock: clk, IDGen: c.idgen})
	if err := cold.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	add, protection := cold.order(ack.OrderID), cold.order(stop.ID)
	if add.RiskEntry.SharedEntryID != owner.ID || add.RiskEntry.StopApplied || protection.StopPrice != 9.5 || add.Held.Phase != HeldPaused || protection.Held.Phase != HeldPaused {
		t.Fatalf("shared recovery: add=%+v stop=%+v", add, protection)
	}
}

func TestRiskAddFixedExitPositionBeforeFillDoesNotRetireCoverageTwice(t *testing.T) {
	c, clk, b, ctx, owner, initialStop := filledRiskPosition(t, nil)
	ack := c.Do(riskAddCommand(owner))
	if !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.5))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: ack.OrderID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: 25, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 25, LeavesQty: 25, AvgPrice: 10.5}
	waitOrder(t, c, initialStop.ID, func(o Order) bool { return o.Qty == 125 && o.StopPrice == 10 })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(4, clk.Now(), 10))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("missing exit")
	}
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	b.ev <- OrderAccepted{V: "v", OID: initialStop.ID, Ts: clk.Now().UnixMilli()}
	stop := waitHeldOrder(t, c, initialStop.ID, HeldWorking)
	started, release := make(chan struct{}, 3), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	b.mu.Lock()
	b.snapshotStarted, b.snapshotRelease = started, release
	b.mu.Unlock()
	b.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 95, AvgPrice: 10}}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fixed shared exit did not reconcile ambiguous position")
	}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: stop.ID, Symbol: "AAPL", Side: SideSell, Qty: 30, Price: 10, TsMs: clk.Now().UnixMilli()}, PositionExecID: "shared-exit30", CumQty: 30, LeavesQty: 95, AvgPrice: 10}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.ExecutedQty == 30 })
	stop.ExecutedQty, stop.LeavesQty, stop.Status = 30, 95, StatusPartiallyFilled
	b.mu.Lock()
	b.positions = []Position{{Venue: "v", Symbol: "AAPL", Qty: 95, AvgPrice: 10}}
	b.orders = map[string]Order{stop.ID: stop}
	b.mu.Unlock()
	close(release)
	remaining := waitOrder(t, c, stop.ID, func(o Order) bool { return o.Held.PausedReason == "" && o.ExecutedQty == 30 })
	if remaining.Qty != 125 || remaining.LeavesQty != 95 {
		t.Fatalf("frozen shared exit changed: %+v", remaining)
	}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: ack.OrderID, Symbol: "AAPL", Side: SideBuy, Qty: 5, Price: 10.5, TsMs: clk.Now().UnixMilli()}, CumQty: 30, AvgPrice: 10.5}
	select {
	case req := <-b.submits:
		if req.Side != SideSell || req.Qty != 5 {
			t.Fatalf("shared coverage retired twice: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("double retirement lost late-fill protection")
	}
}
