package exec

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func (d *heldDemandStub) AcquireTrail(ctx context.Context, id, symbol string, _ TrailConfig) error {
	return d.Acquire(ctx, id, symbol)
}

type verifiedBookDemand struct{ heldDemandStub }

func (*verifiedBookDemand) TrailBookCoverage(string) uint64 { return 42 }

func TestTrailQuoteRequiresCurrentAcknowledgedBookCoverage(t *testing.T) {
	c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	c.Do(ConfigureHeldDemand{Demand: &verifiedBookDemand{}})
	b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	a := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .3, Sessions: TrailSessions{RTH: true}, Pricing: "QUOTE", QuoteSide: "BID"}})
	if !a.Accepted {
		t.Fatal(a)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	waitHeldOrder(t, c, a.OrderID, HeldArmed)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Bid: 9.68, Ask: 9.69, Generation: 1, Coverage: 41})
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 9.7))
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.7)) // distinct trade corroboration; execution quote remains unavailable
	waitHeldOrder(t, c, a.OrderID, HeldWaitingQuote)
	select {
	case child := <-b.submits:
		t.Fatalf("used prior subscription: %+v", child)
	case <-time.After(20 * time.Millisecond):
	}
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Bid: 9.68, Ask: 9.69, Generation: 1, Coverage: 42})
	select {
	case child := <-b.submits:
		if child.LimitPrice != 9.68 {
			t.Fatal(child)
		}
	case <-time.After(time.Second):
		t.Fatal("current acknowledged book did not activate")
	}
}

func TestRiskTrailingPresetKeepsInitialFloorAndPartialFillProgress(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500,
		Protection: &TrailConfig{Mode: "PRICE", Value: .30, Pricing: "CUSHION"}})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-broker.submits:
	case <-time.After(time.Second):
		t.Fatal("entry did not activate")
	}
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 100 && o.Held.Trail != nil && o.Held.Trail.Watermark == 10 })
	if stop.StopPrice != 9.8 {
		t.Fatalf("initial floor lost: %+v", stop)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 10.2))
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.StopPrice == 9.9 })
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 50, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 150, AvgPrice: 10}
	stop = waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 150 })
	if stop.StopPrice != 9.9 || stop.Held.Trail.Watermark != 10.2 {
		t.Fatalf("partial fill reset trail: %+v", stop)
	}
}

func TestCandlePresetClearlyRetainsFixedProtectionBeforeReferenceReady(t *testing.T) {
	c, clk, b, ctx := newRiskCore(t)
	a := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500, Protection: &TrailConfig{Mode: "CANDLE", Timeframe: "1m", Pricing: "CUSHION"}})
	if !a.Accepted {
		t.Fatal(a)
	}
	entry := waitOrder(t, c, a.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("no entry")
	}
	b.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held.Phase == HeldArmed })
	if stop.StopPrice != 9.8 || stop.Held.PausedReason != "fixed fallback; waiting for a verified completed candle" {
		t.Fatalf("fallback misrepresented: %+v", stop.Held)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 9.8))
	select {
	case child := <-b.submits:
		if child.Qty != 100 || child.LimitPrice != 9.8 {
			t.Fatal(child)
		}
	case <-time.After(time.Second):
		t.Fatal("fixed fallback did not protect")
	}
}

func TestRiskTrailManualReductionCannotBeRestoredByIndependentBuy(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500, Protection: &TrailConfig{Mode: "PRICE", Value: .30, Pricing: "CUSHION"}})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-broker.submits:
	case <-time.After(time.Second):
		t.Fatal("no buy")
	}
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 100 })
	broker.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 60, AvgPrice: 10}}
	waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 60 })
	broker.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 80, AvgPrice: 10}}
	waitPosition(t, c, "AAPL", 80)
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 9.8))
	select {
	case child := <-broker.submits:
		if child.Qty != 60 {
			t.Fatalf("retired shares restored: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("no protective exit")
	}
}

func TestWaitingCandleTrailCanBeEditedBeforeExecutionReferenceExists(t *testing.T) {
	c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	config := TrailConfig{Mode: "CANDLE", Timeframe: "1m", Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION"}
	a := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: config})
	if !a.Accepted {
		t.Fatal(a)
	}
	waitHeldOrder(t, c, a.OrderID, HeldWaiting)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		time.Sleep(time.Millisecond)
	}
	config.Timeframe = "5m"
	if ack := c.Do(EditTrail{Venue: "v", OrderID: a.OrderID, ExpectedHeldPhase: HeldWaiting, Config: config}); !ack.Accepted {
		t.Fatal(ack)
	}
	stop := waitOrder(t, c, a.OrderID, func(o Order) bool { return o.Held.Trail.Config.Timeframe == "5m" })
	if stop.StopPrice != 0 || stop.Held.Phase != HeldWaiting {
		t.Fatalf("edit fabricated a reference: %+v", stop)
	}
}

func TestFixedProtectionKeepsOwnedCoverageAfterManualReduction(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-broker.submits:
	case <-time.After(time.Second):
		t.Fatal("no entry")
	}
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 100 })
	broker.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 60, AvgPrice: 10}}
	waitPosition(t, c, "AAPL", 60)
	broker.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 100, AvgPrice: 10}}
	waitPosition(t, c, "AAPL", 100)
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 9.8))
	select {
	case child := <-broker.submits:
		if child.Qty != 60 {
			t.Fatalf("fixed protection sold unrelated shares: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("no protective exit")
	}
}

func TestCancelFixedProtectionThenPlaceIndependentTrail(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	a := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	if !a.Accepted {
		t.Fatal(a)
	}
	entry := waitOrder(t, c, a.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10))
	select {
	case <-broker.submits:
	case <-time.After(time.Second):
		t.Fatal("no entry")
	}
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 100 })
	config := TrailConfig{Mode: "PRICE", Value: .3, Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION"}
	trail := SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: config}
	if c.Do(trail).Accepted {
		t.Fatal("overlapping protection accepted before cancellation")
	}
	if c.Do(EditTrail{Venue: "v", OrderID: stop.ID, ExpectedHeldPhase: stop.Held.Phase, ExpectedStop: stop.StopPrice, Config: config}).Accepted {
		t.Fatal("EditTrail converted fixed protection")
	}
	if ack := c.Do(CancelOrder{Venue: "v", OrderID: stop.ID}); !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Status == StatusCanceled })
	ack := c.Do(trail)
	if !ack.Accepted {
		t.Fatal(ack)
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.7))
	select {
	case child := <-broker.submits:
		if child.ClientOrderID != ack.OrderID || child.ClientOrderID == stop.ID || child.Qty != 100 || math.Abs(child.LimitPrice-9.7) > 1e-9 {
			t.Fatalf("wrong replacement trail: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("manual replacement trail did not trigger")
	}
}

func TestWaitingQuoteTrailCancelReleasesReservation(t *testing.T) {
	c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	a := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .3, Sessions: TrailSessions{RTH: true}, Pricing: "QUOTE", QuoteSide: "BID"}})
	if !a.Accepted {
		t.Fatal(a)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	waitHeldOrder(t, c, a.OrderID, HeldArmed)
	clk.Advance(time.Millisecond)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Generation: 1, Gap: true})
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 9.7))
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.7)) // distinct trade corroboration; execution quote remains unavailable
	waitHeldOrder(t, c, a.OrderID, HeldWaitingQuote)
	if ack := c.Do(CancelOrder{Venue: "v", OrderID: a.OrderID}); !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, a.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	if ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideSell, Qty: 100, Type: TypeLimit, TIF: TIFDay, LimitPrice: 9.7}); !ack.Accepted {
		t.Fatal(ack)
	}
}

func TestTrailingPresetCannotResumePreparationAfterRestart(t *testing.T) {
	c, clk, broker, _ := newRiskCore(t)
	a := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500, Protection: &TrailConfig{Mode: "PRICE", Value: .3, Pricing: "CUSHION"}})
	if !a.Accepted {
		t.Fatal(a)
	}
	events, err := c.store.ReadExecEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	recovered := NewCore(CoreConfig{Venues: []VenueID{"v"}, Brokers: map[VenueID]Broker{"v": broker}, Store: &heldTestStore{events: events}, Clock: clk, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(9)))})
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitOrder(t, recovered, a.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
}

func TestCandleAdvanceDoesNotTriggerAgainstPrintBeforeReferenceOnPartialFill(t *testing.T) {
	c, clk, b, ctx := newRiskCore(t)
	start := clk.Now().Add(-time.Minute).UnixMilli()
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: start, EndMs: start + 60000, Low: 9.8, High: 10, Verified: true})
	a := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.5, Mode: "Dollar", Value: 100, MaxQty: 200, Protection: &TrailConfig{Mode: "CANDLE", Timeframe: "1m", Pricing: "CUSHION"}})
	if !a.Accepted {
		t.Fatal(a)
	}
	entry := waitOrder(t, c, a.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("no buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.StopPrice == 9.8 })
	clk.Advance(time.Minute)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 9.85))
	for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		time.Sleep(time.Millisecond)
	}
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: start + 60000, EndMs: start + 120000, Low: 9.9, High: 10, Verified: true})
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 50, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 150, AvgPrice: 10}
	waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 150 })
	select {
	case child := <-b.submits:
		t.Fatalf("new candle used older cached print: %+v", child)
	case <-time.After(25 * time.Millisecond):
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(4, clk.Now(), 9.85))
	select {
	case child := <-b.submits:
		if child.Qty != 150 || child.LimitPrice != 9.9 {
			t.Fatal(child)
		}
	case <-time.After(time.Second):
		t.Fatal("new causal print did not trigger")
	}
}

func TestTrailingLateFillCleanupDoesNotDoubleCountLinkedFillSnapshot(t *testing.T) {
	testTrailingLateFillSnapshot(t, false, false, false)
}

func TestTrailingPositionBeforeFillReconcilesWithoutCancelingRemainder(t *testing.T) {
	testTrailingLateFillSnapshot(t, true, false, false)
}

func TestTrailingReconciliationRejectsSnapshotBeforeObservedLateBuyFill(t *testing.T) {
	testTrailingLateFillSnapshot(t, true, true, false)
}

func TestTrailingReconciliationAcceptsConfirmedTerminalExitAbsentFromOpenOrders(t *testing.T) {
	testTrailingLateFillSnapshot(t, true, false, true)
}

func testTrailingLateFillSnapshot(t *testing.T, positionFirst, lateWhilePending, terminalExit bool) {
	t.Helper()
	c, clk, b, ctx := newRiskCore(t)
	a := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500, Protection: &TrailConfig{Mode: "PRICE", Value: .3, Pricing: "CUSHION"}})
	if !a.Accepted {
		t.Fatal(a)
	}
	entry := waitOrder(t, c, a.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 10))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("no buy")
	}
	b.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Qty == 100 })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 9.8))
	select {
	case <-b.submits:
	case <-time.After(time.Second):
		t.Fatal("no exit")
	}
	b.ev <- OrderAccepted{V: "v", OID: entry.RiskEntry.StopID, Ts: clk.Now().UnixMilli()}
	stop := waitHeldOrder(t, c, entry.RiskEntry.StopID, HeldWorking)
	var release chan struct{}
	if positionFirst {
		release = make(chan struct{})
		started := make(chan struct{}, 2)
		b.mu.Lock()
		b.snapshotStarted, b.snapshotRelease = started, release
		b.mu.Unlock()
		b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 60, AvgPrice: 10}}}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("ambiguous attribution did not request reconciliation")
		}
	}
	b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.RiskEntry.StopID, Symbol: "AAPL", Side: SideSell, Qty: 40, Price: 9.8, TsMs: clk.Now().UnixMilli()}, PositionExecID: "sell40", CumQty: 40, LeavesQty: 60, AvgPrice: 9.8}
	waitPosition(t, c, "AAPL", 60)
	if positionFirst {
		waitOrder(t, c, stop.ID, func(o Order) bool { return o.ExecutedQty == 40 })
		stop.ExecutedQty, stop.LeavesQty, stop.Status = 40, 60, StatusPartiallyFilled
		entry.ExecutedQty, entry.Status, entry.LeavesQty = 100, StatusCanceled, 0
		if terminalExit {
			b.ev <- OrderCanceled{V: "v", OID: stop.ID, Ts: clk.Now().UnixMilli()}
			waitOrder(t, c, stop.ID, func(o Order) bool { return o.Status == StatusCanceled })
		}
		positionQty := 60.0
		if lateWhilePending {
			b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 20, Price: 10, TsMs: clk.Now().UnixMilli()}, PositionExecID: "late20", CumQty: 120, AvgPrice: 10}
			waitOrder(t, c, entry.ID, func(o Order) bool { return o.ExecutedQty == 120 })
			c.PublishBrokerEvent(BrokerSnapshot{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 80, AvgPrice: 10}}, Orders: []Order{entry, stop}})
			c.Do(Disarm{})
			entry.ExecutedQty, positionQty = 120, 80
		}
		b.mu.Lock()
		b.positions = []Position{{Venue: "v", Symbol: "AAPL", Qty: positionQty, AvgPrice: 10}}
		b.orders[stop.ID], b.orders[entry.ID] = stop, entry
		if lateWhilePending {
			delete(b.orders, entry.ID)
		}
		if terminalExit {
			delete(b.orders, stop.ID)
		}
		b.mu.Unlock()
		close(release)
		if terminalExit {
			waitPosition(t, c, "AAPL", 60)
			ack := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideSell, Type: TypeLimit, TIF: TIFDay, Session: SessionExtended, Qty: 1, LimitPrice: 9.8})
			if !ack.Accepted {
				t.Fatalf("terminal exit kept venue blocked: %+v", ack)
			}
			return
		}
		remaining := waitOrder(t, c, stop.ID, func(o Order) bool {
			return o.Held.Phase == HeldWorking && o.Held.PausedReason == "" && o.ExecutedQty == 40
		})
		if remaining.Held.Trail.Retired || remaining.LeavesQty != 60 {
			t.Fatalf("working remainder changed: %+v", remaining)
		}
	}
	if !lateWhilePending {
		b.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: 60, AvgPrice: 10}}
		b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 20, Price: 10, TsMs: clk.Now().UnixMilli()}, PositionExecID: "late20", CumQty: 120, AvgPrice: 10}
	}
	if lateWhilePending {
		deadline := time.After(time.Second)
		for {
			select {
			case update := <-c.Updates():
				if got, ok := update.(OrderUpdate); ok && got.Order.RiskEntryID == entry.ID && got.Order.ID != stop.ID && got.Order.Qty == 20 && got.Order.Held.Phase == HeldPaused {
					return
				}
			case child := <-b.submits:
				t.Fatalf("reconciliation unexpectedly submitted cleanup: %+v", child)
			case <-deadline:
				t.Fatal("reconciliation lost the observed late-fill exposure")
			}
		}
	}
	select {
	case cleanup := <-b.submits:
		if cleanup.ClientOrderID == entry.RiskEntry.StopID || cleanup.Type != TypeLimit || cleanup.Qty != 20 || cleanup.LimitPrice != 9.8 {
			t.Fatalf("wrong cleanup: %+v", cleanup)
		}
	case <-time.After(time.Second):
		t.Fatal("new uncovered shares did not receive one LIMIT cleanup")
	}
}

func TestTrailingPriceExitRatchetsAndSubmitsOneLimit(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, at)
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100, AvgPrice: 9}}}
	waitPosition(t, c, "AAPL", 100)
	ack := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100,
		Config: TrailConfig{Mode: "PRICE", Value: .30, Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION", Cushion: LimitCushion{Value: .02, Unit: "$"}}})
	if !ack.Accepted {
		t.Fatalf("admit trail: %+v", ack)
	}
	for i, sample := range []struct{ price, stop float64 }{{10, 9.70}, {10.20, 9.90}, {10.05, 9.90}} {
		clk.Advance(time.Millisecond)
		feedCorroboratedPrint(t, c, ctx, eligible(int64(i+1), clk.Now(), sample.price))
		waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.StopPrice == sample.stop })
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(4, clk.Now(), 9.90))
	select {
	case child := <-broker.submits:
		if child.Type != TypeLimit || child.Qty != 100 || child.LimitPrice != 9.88 {
			t.Fatalf("wrong child: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("exact touch did not submit LIMIT")
	}
}

func TestTrailingPercentCoversOnlyConfirmedShortShares(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc())
	c, clk, broker, ctx := newHeldCore(t, at)
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: -100, AvgPrice: 10}}}
	waitPosition(t, c, "AAPL", -100)
	ack := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideCover, PositionPct: 50,
		Config: TrailConfig{Mode: "PERCENT", Value: 2, Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION", Cushion: LimitCushion{Value: .02, Unit: "$"}}})
	if !ack.Accepted {
		t.Fatalf("admit short trail: %+v", ack)
	}
	for i, sample := range []struct{ price, stop float64 }{{10, 10.20}, {9.50, 9.69}, {9.60, 9.69}} {
		clk.Advance(time.Millisecond)
		feedCorroboratedPrint(t, c, ctx, eligible(int64(i+1), clk.Now(), sample.price))
		waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.StopPrice == sample.stop })
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(4, clk.Now(), 9.69))
	select {
	case child := <-broker.submits:
		if child.Side != SideCover || child.Type != TypeLimit || child.Qty != 50 || child.LimitPrice != 9.71 {
			t.Fatalf("wrong short child: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("short touch did not submit LIMIT")
	}
}

func TestTrailingExitRetiresWhenPositionCloses(t *testing.T) {
	c, clk, broker, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	ack := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .30, Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION"}})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	waitHeldOrder(t, c, ack.OrderID, HeldArmed)
	broker.ev <- BrokerPositions{V: "v"}
	retired := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	if retired.Held.Trail == nil || !retired.Held.Trail.Retired {
		t.Fatal("closed position retained a resumable trail")
	}
	if c.Do(ResumeHeldOrder{Venue: "v", OrderID: ack.OrderID}).Accepted {
		t.Fatal("retired trail resumed")
	}
}

func TestTrailingQuoteWaitFreezesAndReservesExitQuantity(t *testing.T) {
	c, clk, broker, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	ack := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .30, Sessions: TrailSessions{RTH: true}, Pricing: "QUOTE", QuoteSide: "BID", Offset: LimitCushion{Value: -.02, Unit: "$"}}})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	waitHeldOrder(t, c, ack.OrderID, HeldArmed)
	clk.Advance(time.Millisecond)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Generation: 1, Gap: true})
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 9.70))
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.70)) // distinct trade corroboration; execution quote remains unavailable
	waiting := waitHeldOrder(t, c, ack.OrderID, HeldWaitingQuote)
	if waiting.Held.ResolvedQty != 100 {
		t.Fatalf("unreserved waiting exit: %+v", waiting)
	}
	if c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideSell, Type: TypeLimit, TIF: TIFDay, Qty: 1, LimitPrice: 10}).Accepted {
		t.Fatal("another sell consumed reserved shares")
	}
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 150}}}
	waitPosition(t, c, "AAPL", 150)
	feedProtectionBook(t, c, ctx, TrailBook{Symbol: "AAPL", Bid: 9.68, Ask: 9.69, Generation: 1})
	select {
	case child := <-broker.submits:
		if child.Qty != 100 || child.LimitPrice != 9.66 {
			t.Fatalf("waiting exit changed recipe or quantity: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("fresh book did not activate frozen exit")
	}
}

func TestTrailingIntentExpiresAcrossEngineRuns(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	store := &heldTestStore{}
	broker := newHeldTestBroker()
	broker.setPositions([]Position{{Venue: "v", Symbol: "AAPL", Qty: 100}})
	newCore := func(s *heldTestStore) *Core {
		return NewCore(CoreConfig{Venues: []VenueID{"v"}, Brokers: map[VenueID]Broker{"v": broker}, Gate: GateConfig{Venue: map[VenueID]VenueLimits{"v": {}}}, Store: s, Clock: clk, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(8)))})
	}
	c := newCore(store)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Recover(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	c.Do(ConfigureHeldDemand{Demand: &heldDemandStub{}})
	c.Do(Arm{})
	ack := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "PRICE", Value: .30, Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION"}})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	history, err := store.ReadExecEventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	closed := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	if !closed.Held.Trail.Retired {
		t.Fatal("clean shutdown preserved trail")
	}
	recovered := newCore(&heldTestStore{events: history})
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := waitOrder(t, recovered, ack.OrderID, func(o Order) bool { return o.Status == StatusCanceled })
	if !old.Held.Trail.Retired {
		t.Fatal("crash recovery restored trailing intent")
	}
}

func TestCoverCannotConsumeSharesCommittedToAnotherCover(t *testing.T) {
	c, _, broker, _ := newHeldCore(t, time.Date(2026, 10, 20, 10, 0, 0, 0, session.Loc()))
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: -100}}}
	waitPosition(t, c, "AAPL", -100)
	first := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideCover, Type: TypeLimit, TIF: TIFDay, Qty: 70, LimitPrice: 10})
	if !first.Accepted {
		t.Fatal(first)
	}
	second := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideCover, Type: TypeLimit, TIF: TIFDay, Qty: 40, LimitPrice: 10})
	if second.Accepted {
		t.Fatal("COVER bypassed reducing-share reservation")
	}
	buy := c.Do(SubmitOrder{Venue: "v", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, TIF: TIFDay, Qty: 40, LimitPrice: 10})
	if buy.Accepted {
		t.Fatal("BUY consumed short shares already reserved for COVER")
	}
	if c.Do(Flatten{Venue: "v"}).Accepted {
		t.Fatal("flatten overlapped an existing COVER commitment")
	}
}

func TestCandleTrailFreezesReferenceCorrectionsAndOnlyRatchets(t *testing.T) {
	c, clk, broker, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 1, 0, 0, session.Loc()))
	broker.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	ack := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: TrailConfig{Mode: "CANDLE", Timeframe: "1m", Buffer: LimitCushion{Value: .02, Unit: "$"}, Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION"}})
	if !ack.Accepted {
		t.Fatal(ack)
	}
	start := clk.Now().Add(-time.Minute).UnixMilli()
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: start, EndMs: clk.Now().UnixMilli(), Low: 9.80, High: 10, Verified: true})
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.StopPrice == 9.78 })
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: start, EndMs: clk.Now().UnixMilli() - 1, Low: 9.99, High: 10, Verified: true})
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 9.90))
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.StopPrice == 9.78 })
	clk.Advance(time.Minute)
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: start + 60_000, EndMs: start + 120_000, Low: 9.85, High: 10, Verified: true})
	feedCorroboratedPrint(t, c, ctx, eligible(3, clk.Now(), 9.90))
	waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.StopPrice == 9.83 })
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(4, clk.Now(), 9.83))
	select {
	case child := <-broker.submits:
		if child.LimitPrice != 9.83 {
			t.Fatal(child)
		}
	case <-time.After(time.Second):
		t.Fatal("completed-candle boundary did not trigger")
	}
}

func TestCandleTimeframeEditCannotReuseCorrectedPriorReference(t *testing.T) {
	c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 20, 10, 5, 0, 0, session.Loc()))
	b.ev <- BrokerPositions{V: "v", Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100}}}
	waitPosition(t, c, "AAPL", 100)
	minute := clk.Now().Add(-time.Minute).UnixMilli()
	five := clk.Now().Add(-5 * time.Minute).UnixMilli()
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: minute, EndMs: minute + 60000, Low: 9.8, High: 10, Verified: true})
	config := TrailConfig{Mode: "CANDLE", Timeframe: "1m", Sessions: TrailSessions{RTH: true}, Pricing: "CUSHION"}
	a := c.Do(SubmitTrail{Venue: "v", Symbol: "AAPL", Side: SideSell, PositionPct: 100, Config: config})
	if !a.Accepted {
		t.Fatal(a)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(1, clk.Now(), 10))
	waitOrder(t, c, a.OrderID, func(o Order) bool { return o.StopPrice == 9.8 })
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "5m", StartMs: five, EndMs: five + 300000, Low: 9.7, High: 10, Verified: true})
	config.Timeframe = "5m"
	if ack := c.Do(EditTrail{Venue: "v", OrderID: a.OrderID, ExpectedHeldPhase: HeldArmed, ExpectedStop: 9.8, Config: config}); !ack.Accepted {
		t.Fatal(ack)
	}
	waitOrder(t, c, a.OrderID, func(o Order) bool { return o.Held.Trail.Reference != nil && o.Held.Trail.Reference.Timeframe == "5m" })
	c.FeedTrailCandle(ctx, TrailCandle{Symbol: "AAPL", Timeframe: "1m", StartMs: minute, EndMs: minute + 60000, Low: 9.99, High: 10, Verified: true})
	config.Timeframe = "1m"
	if ack := c.Do(EditTrail{Venue: "v", OrderID: a.OrderID, ExpectedHeldPhase: HeldArmed, ExpectedStop: 9.8, Config: config}); !ack.Accepted {
		t.Fatal(ack)
	}
	clk.Advance(time.Millisecond)
	feedCorroboratedPrint(t, c, ctx, eligible(2, clk.Now(), 9.9))
	waitOrder(t, c, a.OrderID, func(o Order) bool { return o.Held.Phase == HeldArmed && o.Held.Trail.Config.Timeframe == "1m" })
	select {
	case child := <-b.submits:
		t.Fatalf("corrected used candle was replayed: %+v", child)
	case <-time.After(20 * time.Millisecond):
	}
}
