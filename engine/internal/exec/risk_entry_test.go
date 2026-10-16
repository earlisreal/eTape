package exec

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func newRiskCoreWithoutPrint(t *testing.T) (*Core, *clock.Fake, *heldTestBroker, context.Context) {
	t.Helper()
	c, clk, b, ctx := newHeldCore(t, time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC))
	c.PublishBrokerEvent(BrokerAccount{Account: AccountSnapshot{Venue: "v", BuyingPower: 100000, AvailableCash: 100000, TsMs: clk.Now().UnixMilli()}})
	deadline := time.After(time.Second)
	ready := false
	for !ready {
		select {
		case u := <-c.Updates():
			if a, ok := u.(AccountUpdate); ok && a.Account.TsMs == clk.Now().UnixMilli() {
				ready = true
			}
		case <-deadline:
			t.Fatal("account refresh missing")
		}
	}
	return c, clk, b, ctx
}

func newRiskCore(t *testing.T) (*Core, *clock.Fake, *heldTestBroker, context.Context) {
	t.Helper()
	c, clk, b, ctx := newRiskCoreWithoutPrint(t)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 9))
	until := time.Now().Add(time.Second)
	for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		if time.Now().After(until) {
			t.Fatal("eligible preview missing")
		}
		time.Sleep(time.Millisecond)
	}
	return c, clk, b, ctx
}

func TestRiskEntryTriggersAtChartRoundedPrice(t *testing.T) {
	c, clk, broker, ctx := newRiskCoreWithoutPrint(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 3.0700000000000003, SellStop: 3.04,
		Mode: "Dollar", Value: 0.5, MaxQty: 4, BuyCushion: LimitCushion{Value: 1, Unit: "%"}, SellCushion: LimitCushion{Value: 1, Unit: "%"}})
	if !ack.Accepted {
		t.Fatalf("admission: %+v", ack)
	}
	waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 3.0699))
	waitHeldOrder(t, c, ack.OrderID, HeldArmed)
	select {
	case req := <-broker.submits:
		t.Fatalf("submitted below trigger: %+v", req)
	default:
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 3.07))
	select {
	case req := <-broker.submits:
		if req.ClientOrderID != ack.OrderID || req.Type != TypeLimit || req.Qty != 4 || req.LimitPrice != 3.11 {
			t.Fatalf("triggered child: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("$3.07 touch did not activate chart-rounded buy stop")
	}
}

func TestRiskEntryWaitsForFreshPrint(t *testing.T) {
	for _, state := range []string{"missing", "stale"} {
		t.Run(state, func(t *testing.T) {
			c, clk, broker, ctx := newRiskCoreWithoutPrint(t)
			if state == "stale" {
				c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 10.1))
				until := time.Now().Add(time.Second)
				for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
					if time.Now().After(until) {
						t.Fatal("eligible preview missing")
					}
					time.Sleep(time.Millisecond)
				}
				clk.Advance(2001 * time.Millisecond)
			}
			ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
			if !ack.Accepted {
				t.Fatalf("%s trade blocked acceptance: %+v", state, ack)
			}
			entry := waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
			if entry.Qty != 500 || entry.Held.PausedReason != "Waiting for market data" {
				t.Fatalf("waiting risk entry: %+v", entry)
			}
			select {
			case req := <-broker.submits:
				t.Fatalf("submitted before a fresh trade: %+v", req)
			default:
			}
			clk.Advance(time.Millisecond)
			c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10))
			entry = waitHeldOrder(t, c, entry.ID, HeldActivating)
			if entry.Held.PausedReason != "" {
				t.Fatalf("waiting reason survived activation: %+v", entry.Held)
			}
			select {
			case req := <-broker.submits:
				if req.ClientOrderID != entry.ID || req.Type != TypeLimit || req.Qty != 500 {
					t.Fatalf("triggered child: %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("fresh qualifying trade did not activate")
			}
		})
	}
}

type riskEntryTestDemand struct {
	heldDemandStub
	beforeAcquire func()
	err           error
}

func (d *riskEntryTestDemand) Acquire(ctx context.Context, id, symbol string) error {
	if d.beforeAcquire != nil {
		d.beforeAcquire()
	}
	if d.err != nil {
		return d.err
	}
	return d.heldDemandStub.Acquire(ctx, id, symbol)
}

func TestRiskEntryRechecksFreshPrintAfterTickerAcquisition(t *testing.T) {
	for _, state := range []string{"fresh", "expired"} {
		t.Run(state, func(t *testing.T) {
			c, clk, broker, _ := newRiskCore(t)
			if state == "expired" {
				demand := &riskEntryTestDemand{beforeAcquire: func() { clk.Advance(1500 * time.Millisecond) }}
				if ack := c.Do(ConfigureHeldDemand{Demand: demand}); !ack.Accepted {
					t.Fatalf("configure delayed subscription: %+v", ack)
				}
			}
			ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 8.9, SellStop: 8.7, Mode: "Dollar", Value: 100, MaxQty: 500})
			if !ack.Accepted {
				t.Fatalf("admission: %+v", ack)
			}
			if state == "expired" {
				if current := c.order(ack.OrderID); current.Held.Phase != HeldWaiting {
					t.Fatalf("expired print activated entry: %+v", current.Held)
				}
				entry := waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
				if entry.Held.PausedReason != "Waiting for market data" {
					t.Fatalf("expired print did not wait: %+v", entry.Held)
				}
				select {
				case req := <-broker.submits:
					t.Fatalf("print expired during subscription activated entry: %+v", req)
				default:
				}
			} else {
				waitHeldOrder(t, c, ack.OrderID, HeldActivating)
				select {
				case <-broker.submits:
				case <-time.After(time.Second):
					t.Fatal("fresh already-satisfied BUY did not activate")
				}
			}
		})
	}
}

func TestRiskEntryWaitingForDataPausesOnFeedGap(t *testing.T) {
	c, clk, broker, ctx := newRiskCoreWithoutPrint(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	if !ack.Accepted {
		t.Fatalf("admission: %+v", ack)
	}
	waitHeldOrder(t, c, ack.OrderID, HeldWaiting)
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, EligiblePrint{Gap: true, RecvTsMs: clk.Now().UnixMilli()})
	paused := waitHeldOrder(t, c, ack.OrderID, HeldPaused)
	if paused.Held.PausedReason != "eligible-print gap; resume manually" {
		t.Fatalf("feed gap did not pause custody: %+v", paused.Held)
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(1, clk.Now(), 10.1))
	until := time.Now().Add(time.Second)
	for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		if time.Now().After(until) {
			t.Fatal("feed did not recover")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case req := <-broker.submits:
		t.Fatalf("feed recovery silently resumed paused risk entry: %+v", req)
	default:
	}
}

func TestRiskEntryStillRequiresAccountAndTicker(t *testing.T) {
	for _, blocker := range []string{"account", "ticker"} {
		t.Run(blocker, func(t *testing.T) {
			c, clk, broker, _ := newRiskCoreWithoutPrint(t)
			reason := "fresh account data required"
			if blocker == "account" {
				clk.Advance(31 * time.Second)
			} else {
				reason = "execution ticker unavailable: offline"
				if ack := c.Do(ConfigureHeldDemand{Demand: &riskEntryTestDemand{err: errors.New("offline")}}); !ack.Accepted {
					t.Fatalf("configure failed subscription: %+v", ack)
				}
			}
			ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
			if ack.Accepted || ack.Reason != reason {
				t.Fatalf("%s gate: %+v", blocker, ack)
			}
			select {
			case req := <-broker.submits:
				t.Fatalf("blocked risk entry reached broker: %+v", req)
			default:
			}
		})
	}
}

func TestRiskEntryRevalidatesAfterTickerAcquisition(t *testing.T) {
	for _, blocker := range []string{"account", "session"} {
		t.Run(blocker, func(t *testing.T) {
			c, clk, broker, _ := newRiskCoreWithoutPrint(t)
			delay, reason := 16*time.Second, "fresh account data required"
			if blocker == "session" {
				delay, reason = 6*time.Hour, "risk entry session expired while acquiring ticker"
			}
			demand := &riskEntryTestDemand{beforeAcquire: func() { clk.Advance(delay) }}
			if ack := c.Do(ConfigureHeldDemand{Demand: demand}); !ack.Accepted {
				t.Fatalf("configure delayed subscription: %+v", ack)
			}
			ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
			if ack.Accepted || ack.Reason != reason {
				t.Fatalf("%s expired during subscription: %+v", blocker, ack)
			}
			demand.mu.Lock()
			held := len(demand.held)
			demand.mu.Unlock()
			if held != 0 {
				t.Fatalf("failed admission retained %d ticker leases", held)
			}
			select {
			case req := <-broker.submits:
				t.Fatalf("expired admission reached broker: %+v", req)
			default:
			}
		})
	}
}

func TestRiskEntryAdmission(t *testing.T) {
	c, _, broker, _ := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8,
		Mode: "Dollar", Value: 100, MaxQty: 500})
	if !ack.Accepted {
		t.Fatalf("admission: %+v", ack)
	}
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	if entry.Qty != 500 || entry.RiskEntry.Budget != 100 || entry.Held.DeadlineMs != time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("risk-sized DAY pair: %+v", entry)
	}
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.RiskEntryID == entry.ID })
	if stop.Qty != 0 || stop.ExecutedQty != 0 {
		t.Fatalf("protection must await entry fills: %+v", stop)
	}
	select {
	case req := <-broker.submits:
		t.Fatalf("broker order before trigger: %+v", req)
	default:
	}
	if dup := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500}); dup.Accepted {
		t.Fatal("second pair admitted on working symbol")
	}
}

func TestRiskEntryPartialFillProtectionSurvivesDisarm(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held != nil })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10))
	select {
	case req := <-broker.submits:
		if req.Side != SideBuy || req.Qty != 500 {
			t.Fatalf("entry: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("entry did not trigger")
	}
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 })
	if disarm := c.Do(Disarm{}); !disarm.Accepted {
		t.Fatalf("disarm: %+v", disarm)
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.8))
	select {
	case req := <-broker.submits:
		if req.Side != SideSell || req.Qty != 100 || req.LimitPrice != 9.8 {
			t.Fatalf("fill-scoped protection: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("protection stopped on disarm")
	}
}

func TestRiskEntryLateFillsDoNotDuplicatePausedProtection(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held != nil })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10))
	<-broker.submits
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.8))
	<-broker.submits
	broker.ev <- OrderAccepted{V: "v", OID: stop.ID, Ts: clk.Now().UnixMilli()}
	c.FeedEligiblePrint(ctx, EligiblePrint{Gap: true})
	// Synchronize the gap through the public preview query.
	for c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		time.Sleep(time.Millisecond)
	}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 20, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 120, AvgPrice: 10}
	var late Order
	deadline := time.After(time.Second)
	for late.ID == "" {
		select {
		case u := <-c.Updates():
			if update, ok := u.(OrderUpdate); ok && update.Order.RiskEntryID == entry.ID && update.Order.ID != stop.ID {
				late = update.Order
			}
		case <-deadline:
			t.Fatal("late fill protection missing")
		}
	}
	if late.Qty != 20 || late.Held.Phase != HeldPaused {
		t.Fatalf("late protection: %+v", late)
	}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 130, AvgPrice: 10}
	deadline = time.After(time.Second)
	for {
		select {
		case u := <-c.Updates():
			if update, ok := u.(OrderUpdate); ok && update.Order.RiskEntryID == entry.ID && update.Order.ID != stop.ID && update.Order.ID != late.ID {
				if update.Order.Qty != 10 {
					t.Fatalf("duplicate coverage: %+v", update.Order)
				}
				return
			}
		case <-deadline:
			t.Fatal("additional late fill protection missing")
		}
	}
}

func TestRiskEntryDragRevalidatesEntryActivation(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500, BuyCushion: LimitCushion{Value: 0.05, Unit: "$"}})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held != nil })
	if entry.Qty != 400 {
		t.Fatalf("cushion risk size: %+v", entry)
	}
	edit := c.Do(ReplaceOrder{Venue: "v", OrderID: entry.ID, Qty: 222, StopPrice: 10.2, ExpectedHeldPhase: HeldWaiting, ExpectedRiskEntryPhase: "WAITING"})
	if !edit.Accepted {
		t.Fatalf("waiting edit: %+v", edit)
	}
	entry = waitOrder(t, c, entry.ID, func(o Order) bool { return o.StopPrice == 10.2 })
	if entry.Qty != 222 || math.Abs(entry.LimitPrice-10.25) > 1e-8 {
		t.Fatalf("original budget sizing: %+v", entry)
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10.2))
	<-broker.submits
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	waitHeldOrder(t, c, entry.ID, HeldWorking)
	if stale := c.Do(ReplaceOrder{Venue: "v", OrderID: stop.ID, StopPrice: 9.9, ExpectedHeldPhase: HeldWaiting, ExpectedRiskEntryPhase: "WAITING"}); stale.Accepted {
		t.Fatal("stop drag silently changed sizing semantics after buy activation")
	}
}

func TestRiskEntryRecoveryKeepsPairPausedWithoutBrokerReplay(t *testing.T) {
	c, clk, _, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "CashPct", Value: 1, MaxQty: 40})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held != nil })
	if entry.Qty != 40 || entry.RiskEntry.Budget != 1000 {
		t.Fatalf("preview cap and Cash risk: %+v", entry)
	}
	broker := newHeldTestBroker()
	recovered := NewCore(CoreConfig{Venues: []VenueID{"v"}, Gate: c.gate, Store: c.store, Brokers: map[VenueID]Broker{"v": broker}, Clock: clk})
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	restored := waitHeldOrder(t, recovered, entry.ID, HeldPaused)
	protection := waitHeldOrder(t, recovered, stop.ID, HeldPaused)
	if restored.RiskEntry == nil || restored.RiskEntry.StopID != stop.ID || protection.RiskEntryID != entry.ID {
		t.Fatal("pair linkage lost during recovery")
	}
	select {
	case req := <-broker.submits:
		t.Fatalf("restart replayed order: %+v", req)
	default:
	}
}

func TestRiskEntryReconciledFillsGrowPausedProtection(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held != nil })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10))
	<-broker.submits
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	waitHeldOrder(t, c, entry.ID, HeldWorking)
	broker.ev <- BrokerConnDown{V: "v"}
	waitHeldOrder(t, c, stop.ID, HeldPaused)
	entry.Type = TypeLimit
	entry.Held = nil
	entry.RiskEntry = nil
	entry.ExecutedQty = 100
	entry.LeavesQty = 400
	entry.Status = StatusPartiallyFilled
	broker.ev <- BrokerSnapshot{V: "v", Account: AccountSnapshot{Venue: "v", BuyingPower: 100000, AvailableCash: 100000, TsMs: clk.Now().UnixMilli()}, Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 100, AvgPrice: 10}}, Orders: []Order{entry}}
	updated := waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 })
	if updated.Held.Phase != HeldPaused {
		t.Fatal("reconciliation automatically resumed protection")
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.9))
	for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		time.Sleep(time.Millisecond)
	}
	if resume := c.Do(ResumeHeldOrder{Venue: "v", OrderID: stop.ID}); !resume.Accepted {
		t.Fatalf("resume: %+v", resume)
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(4, clk.Now(), 9.8))
	select {
	case req := <-broker.submits:
		if req.Side != SideSell || req.Qty != 100 {
			t.Fatalf("reconciled protection: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed protection missing")
	}
}

func TestRiskEntryWaitingEditCapsReviewedQuantity(t *testing.T) {
	c, _, _, _ := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	if edit := c.Do(ReplaceOrder{Venue: "v", OrderID: entry.ID, Qty: 100, StopPrice: 10, ExpectedHeldPhase: HeldWaiting, ExpectedRiskEntryPhase: "WAITING"}); !edit.Accepted {
		t.Fatalf("edit: %+v", edit)
	}
	updated := waitOrder(t, c, entry.ID, func(o Order) bool { return o.RiskEntry != nil })
	if updated.Qty != 100 {
		t.Fatalf("entry grew above reviewed preview: %+v", updated)
	}
}

func TestRiskEntryReconciledLateFillRequiresResume(t *testing.T) {
	c, clk, broker, ctx := newRiskCore(t)
	ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", BuyStop: 10, SellStop: 9.8, Mode: "Dollar", Value: 100, MaxQty: 500})
	entry := waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	stop := waitOrder(t, c, entry.RiskEntry.StopID, func(o Order) bool { return o.Held != nil })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(2, clk.Now(), 10))
	<-broker.submits
	broker.ev <- OrderAccepted{V: "v", OID: entry.ID, Ts: clk.Now().UnixMilli()}
	broker.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: entry.ID, Symbol: "AAPL", Side: SideBuy, Qty: 100, Price: 10, TsMs: clk.Now().UnixMilli()}, CumQty: 100, AvgPrice: 10}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 100 })
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(3, clk.Now(), 9.8))
	<-broker.submits
	broker.ev <- OrderAccepted{V: "v", OID: stop.ID, Ts: clk.Now().UnixMilli()}
	stop = waitHeldOrder(t, c, stop.ID, HeldWorking)
	broker.ev <- BrokerConnDown{V: "v"}
	for c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		time.Sleep(time.Millisecond)
	}
	clk.Advance(time.Millisecond)
	c.FeedEligiblePrint(ctx, eligible(4, clk.Now(), 9.8))
	for !c.PreviewEligiblePrint(ctx, "AAPL").Trusted {
		time.Sleep(time.Millisecond)
	}
	entry.Type, entry.Held, entry.RiskEntry = TypeLimit, nil, nil
	entry.ExecutedQty, entry.LeavesQty, entry.Status = 120, 380, StatusPartiallyFilled
	stop.Type, stop.Held, stop.RiskEntryID = TypeLimit, nil, ""
	broker.ev <- BrokerSnapshot{V: "v", Account: AccountSnapshot{Venue: "v", BuyingPower: 100000, AvailableCash: 100000, TsMs: clk.Now().UnixMilli()}, Positions: []Position{{Venue: "v", Symbol: "AAPL", Qty: 120, AvgPrice: 10}}, Orders: []Order{entry, stop}}
	deadline := time.After(time.Second)
	for {
		select {
		case u := <-c.Updates():
			if update, ok := u.(OrderUpdate); ok && update.Order.RiskEntryID == entry.ID && update.Order.ID != stop.ID {
				if update.Order.Qty != 20 || update.Order.Held.Phase != HeldPaused {
					t.Fatalf("reconciled late protection must await Resume: %+v", update.Order)
				}
				return
			}
		case <-deadline:
			t.Fatal("reconciled late protection missing")
		}
	}
}
