package exec

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

const bingxTestSymbol = "PERP.BINGX.BTC-USDT"

type bingxRiskFixture struct {
	*mexcRiskBroker
	terms      PerpetualOpeningTerms
	armStarted chan struct{}
	armFinish  chan struct{}
}

func (b *bingxRiskFixture) ContractRules(symbol string) (instrument.ContractRules, bool) {
	rules, _ := b.mexcRiskBroker.ContractRules("PERP.MEXC.BTC_USDT")
	return rules, symbol == bingxTestSymbol
}
func (b *bingxRiskFixture) OpeningTerms(context.Context, string) (PerpetualOpeningTerms, error) {
	return b.terms, nil
}
func (*bingxRiskFixture) AccountIdentity() string { return "virtual-account" }
func (b *bingxRiskFixture) ArmPerpetualRisk(ctx context.Context, _ OrderRequest) (int64, error) {
	if b.armStarted != nil {
		close(b.armStarted)
		select {
		case <-b.armFinish:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return b.terms.Long.MaxContracts, nil
}

func TestBingXArmingDoesNotBlockDisarm(t *testing.T) {
	const venue = VenueID("bingx-vst")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	clk := clock.NewFake(time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC))
	broker := &bingxRiskFixture{mexcRiskBroker: &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000},
		armStarted: make(chan struct{}), armFinish: make(chan struct{}),
		terms: PerpetualOpeningTerms{Collateral: 1000, Long: PerpetualOpeningLimit{Leverage: 5, MaxLeverage: 10, MaxContracts: 20},
			Notional: &instrument.NotionalRisk{Mark: 100_000, Tiers: []instrument.NotionalTier{{Maximum: 1_000_000, Rate: 0.005}}}}}
	core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: &heldTestStore{},
		IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))), Brokers: map[VenueID]Broker{venue: broker},
		Gate: GateConfig{Venue: map[VenueID]VenueLimits{venue: {}}}})
	core.heldDemand = &heldDemandStub{}
	core.state.SetMasterArmed(true)
	core.state.SetPositionsReady(venue, true)
	core.state.ReconcileAccount(AccountSnapshot{Venue: venue, AvailableCash: 1000, TsMs: clk.Now().UnixMilli()})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = core.Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	reply := make(chan CmdAck, 1)
	go func() {
		reply <- core.DoContext(ctx, SubmitPerpetualRiskEntry{Venue: venue, Symbol: bingxTestSymbol, Side: SideBuy,
			Trigger: 100_500, Stop: 99_500, Budget: 10, MaxQty: 2, Leverage: 5})
	}()
	select {
	case <-broker.armStarted:
	case ack := <-reply:
		t.Fatalf("entry never started arming: %+v", ack)
	case <-ctx.Done():
		t.Fatal("entry did not begin arming")
	}
	if ack := core.DoContext(ctx, Disarm{}); !ack.Accepted {
		t.Fatalf("could not disarm while verifying leverage: %+v", ack)
	}
	close(broker.armFinish)
	select {
	case ack := <-reply:
		if ack.Accepted || ack.OrderID == "" {
			t.Fatalf("completed preflight resurrected a canceled entry: %+v", ack)
		}
	case <-ctx.Done():
		t.Fatal("arming did not finish")
	}
}

func TestBingXChartRiskEntrySharesWholePosition(t *testing.T) {
	for _, test := range []struct {
		venue       VenueID
		open, close Side
		sign        float64
	}{
		{"bingx-vst", SideBuy, SideSell, 1},
		{"bingx-live", SideShort, SideCover, -1},
	} {
		t.Run(string(test.venue), func(t *testing.T) {
			ctx := t.Context()
			clk := clock.NewFake(time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC))
			broker := &bingxRiskFixture{mexcRiskBroker: &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000}}
			identity := map[VenueID]string{}
			if test.venue == "bingx-live" {
				identity[test.venue] = "live-account"
			}
			core := NewCore(CoreConfig{Venues: []VenueID{test.venue}, Clock: clk, Store: &heldTestStore{},
				IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))), Brokers: map[VenueID]Broker{test.venue: broker},
				Gate: GateConfig{Venue: map[VenueID]VenueLimits{test.venue: {}}}, HeldStopLimitLiveIdentity: identity})
			core.heldDemand = &heldDemandStub{}
			core.state.SetMasterArmed(true)
			account := AccountSnapshot{Venue: test.venue, AvailableCash: 1000, TsMs: clk.Now().UnixMilli()}
			snapshot := func(positions ...Position) {
				account.TsMs = clk.Now().UnixMilli()
				core.handleBrokerEvent(ctx, BrokerSnapshot{V: test.venue, Account: account, Positions: positions})
			}
			snapshot()
			position := Position{Venue: test.venue, Symbol: bingxTestSymbol, BrokerPositionID: "90071992547409955",
				AvgPrice: 100_000, Leverage: 5, IsolatedMargin: 80, LiquidationPrice: 100_000 - test.sign*15_000}
			terms := PerpetualOpeningLimit{Leverage: 5, MaxContracts: 100, MaxLeverage: 10}
			prepare := func(command SubmitPerpetualRiskEntry, p Position) CmdAck {
				command.openingLimit = terms
				command.openingTerms = PerpetualOpeningTerms{PositionID: p.BrokerPositionID, Collateral: 1000,
					Notional: &instrument.NotionalRisk{Mark: broker.fair, MinimumValue: 2,
						Tiers: []instrument.NotionalTier{{Maximum: 1_000_000, Rate: 0.005}},
						Position: instrument.RiskPosition{Qty: p.Qty, AvgPrice: p.AvgPrice, Margin: p.IsolatedMargin,
							LiquidationPrice: p.LiquidationPrice, Leverage: p.Leverage}}}
				return core.handlePerpetualRiskEntry(ctx, command)
			}
			waiting := func(command SubmitPerpetualRiskEntry, p Position) Order {
				t.Helper()
				ack := prepare(command, p)
				if ack.perpetualArm == nil || core.order(ack.OrderID).Held.Phase != HeldPaused {
					t.Fatalf("entry was not staged for signed arming: %+v", ack)
				}
				if armed := core.finishWholePositionArm(ctx, finishPerpetualArm{orderID: ack.OrderID, capacity: 100}); !armed.Accepted {
					t.Fatalf("entry did not arm: %+v", armed)
				}
				return core.order(ack.OrderID)
			}
			submission := func() OrderRequest {
				t.Helper()
				select {
				case req := <-broker.submits:
					return req
				case <-time.After(time.Second):
					t.Fatal("no broker submission")
					return OrderRequest{}
				}
			}
			var sequence int64
			trigger := func(o Order) {
				t.Helper()
				broker.fair = o.StopPrice
				clk.Advance(time.Millisecond)
				sequence++
				core.handleEligiblePrint(ctx, EligiblePrint{Symbol: o.Symbol, Price: o.StopPrice, Seq: sequence,
					TsMs: core.now(), RecvTsMs: core.now()})
				request := submission()
				if request.ClientOrderID != o.ID || request.Type != TypeLimit || request.TIF != TIFIOC ||
					!request.Perpetual.WholePosition || request.Perpetual.ProtectionID != o.Perpetual.ProtectionID {
					t.Fatalf("wrong protected IOC: %+v", request)
				}
			}
			reconcile := func(o Order, status OrderStatus, id string, quantity float64, fills ...PerpetualFill) {
				t.Helper()
				report := PerpetualObservation{Venue: test.venue, OrderID: o.ID, BrokerOrderID: o.ID + "-broker",
					Status: status, PositionID: id, OrderQty: quantity, Fills: fills, Persisted: make(chan bool, 1)}
				core.handleBrokerEvent(ctx, report)
				if !<-report.Persisted {
					t.Fatal("observation failed to reconcile")
				}
			}
			entry := SubmitPerpetualRiskEntry{Venue: test.venue, Symbol: bingxTestSymbol, Side: test.open, Leverage: 5,
				Trigger: 100_500 - 500*(1-test.sign), Stop: 100_000 - test.sign*200, Budget: 20, MaxQty: 4}
			if identity[test.venue] != "" {
				if ack := prepare(entry, Position{}); ack.perpetualArm != nil || ack.Reason == "" {
					t.Fatal("live chart entry bypassed account acknowledgement")
				}
				core.heldLiveAck[test.venue] = identity[test.venue]
			}
			root := waiting(entry, Position{})
			trigger(root)
			reconcile(root, StatusFilled, position.BrokerPositionID, 0, PerpetualFill{ID: "root", Qty: 4, Price: 100_000, TsMs: core.now()})
			position.Qty = 4 * test.sign
			snapshot(position)
			stopID := root.Perpetual.ProtectionID
			stop := core.order(stopID)
			if stop.Qty != 4 || !stop.isPerpetualRiskStop() || stop.Side != test.close || stop.StopPrice != entry.Stop {
				t.Fatalf("incorrect initial stop: %+v", stop)
			}

			add := entry
			add.SharedStopID, add.Trigger, add.Stop, add.MaxQty = stopID, 100_700-700*(1-test.sign), 100_000-test.sign*100, 2
			looser := add
			looser.Stop = entry.Stop - test.sign*50
			if reject := prepare(looser, position); reject.perpetualArm != nil {
				t.Fatal("a linked add loosened the shared stop")
			}
			zero := waiting(add, position)
			trigger(zero)
			reconcile(zero, StatusCanceled, "", 0)
			if core.order(stopID).StopPrice != entry.Stop {
				t.Fatal("an unfilled add changed the stop")
			}
			linked := waiting(add, position)
			if repeated := prepare(add, position); repeated.perpetualArm != nil {
				t.Fatal("armed multiple adds on one contract")
			}
			trigger(linked)
			fill := PerpetualFill{ID: "add", Qty: 1, Price: 100_000, TsMs: core.now()}
			reconcile(linked, StatusAccepted, position.BrokerPositionID, 0, fill)
			position.Qty = 5 * test.sign
			snapshot(position)
			if o := core.order(stopID); o.StopPrice != add.Stop || o.Qty != 5 {
				t.Fatalf("first fill did not tighten and resize the shared stop: %+v", o)
			}
			reconcile(linked, StatusCanceled, position.BrokerPositionID, 0, fill)
			position.Qty = 8 * test.sign
			snapshot(position)
			if core.order(stopID).Qty != 8 {
				t.Fatal("external same-side fills did not grow the shared stop")
			}

			core.handleArm(ctx, false)
			reduce := OrderRequest{Venue: test.venue, Symbol: bingxTestSymbol, Side: test.close, Type: TypeMarket,
				Qty: 4, Perpetual: &PerpetualOrder{Multiplier: 0.001}}
			if ok, reason := core.checkSellLeaves(reduce, reduce.Qty, ""); !ok {
				t.Fatalf("the shared stop reserved the manual reduction: %s", reason)
			}
			position.Qty /= 2
			snapshot(position)
			if core.order(stopID).Qty != 4 {
				t.Fatal("the shared stop did not shrink after selling half")
			}
			core.handleBrokerEvent(ctx, BrokerConnDown{V: test.venue})
			broker.fair = add.Stop
			snapshot(position)
			if o := core.order(stopID); o.Held.Phase != HeldPaused || o.Held.ChildClientID != "" {
				t.Fatalf("the crossed stop activated without Resume: %+v", o)
			}
			if ack := core.handleResumeHeld(ctx, ResumeHeldOrder{Venue: test.venue, OrderID: stopID}); !ack.Accepted {
				t.Fatalf("could not resume risk reduction while disarmed: %+v", ack)
			}
			close := submission()
			if close.Type != TypeMarket || close.Side != test.close || close.Qty != math.Abs(position.Qty) ||
				close.ClientOrderID != stopID || !close.Perpetual.WholePosition || close.Perpetual.PositionID != position.BrokerPositionID {
				t.Fatalf("wrong full-position close after the reduction: %+v", close)
			}
			// A direct same-side fill can land before the exchange executes its ID close.
			reconcile(core.order(stopID), StatusFilled, position.BrokerPositionID, 5,
				PerpetualFill{ID: "close", Qty: 5, Price: 100_000, TsMs: core.now()})
			if o := core.order(stopID); o.Status != StatusFilled || o.ExecutedQty != 5 {
				t.Fatalf("position close did not use exchange size: %+v", o)
			}
			position.BrokerPositionID = "90071992547409956"
			snapshot(position)
			if ids := core.heldOrderIDs(); len(ids) != 0 {
				t.Fatalf("old stop acquired a replacement position: %v", ids)
			}
		})
	}
}

func TestBingXStopWaitsForSubmittedAddAndClosesOnlyItsProvenLateFill(t *testing.T) {
	const venue, oldID, lateID = VenueID("bingx-vst"), "90071992547409955", "90071992547409956"
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC))
	broker := &bingxRiskFixture{mexcRiskBroker: &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 99_000}}
	core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: &heldTestStore{},
		IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))), Brokers: map[VenueID]Broker{venue: broker}})
	core.heldDemand = &heldDemandStub{}
	terms := PerpetualOrder{Multiplier: 0.001, PositionID: oldID, WholePosition: true, LocalProtection: true, AccountIdentity: broker.AccountIdentity()}
	stop := newOrderFromRequest(OrderRequest{Venue: venue, Symbol: bingxTestSymbol, Side: SideSell, Type: TypeStop,
		Qty: 4, StopPrice: 99_000, ClientOrderID: "old-stop", Perpetual: &terms}, core.now())
	stop.Held = &HeldOrder{Phase: HeldWaiting}
	addTerms := terms
	addTerms.ProtectionID, addTerms.ProtectionPrice, addTerms.RiskBudget = stop.ID, stop.StopPrice, 5
	add := newOrderFromRequest(OrderRequest{Venue: venue, Symbol: bingxTestSymbol, Side: SideBuy, Type: TypeStopLimit,
		Qty: 1, StopPrice: 100_000, ClientOrderID: "linked-add", Perpetual: &addTerms}, core.now())
	add.Held = &HeldOrder{Phase: HeldActivating, ChildClientID: add.ID}
	for _, o := range []Order{stop, add} {
		if err := core.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
			t.Fatal(err)
		}
	}
	original := Position{Venue: venue, Symbol: bingxTestSymbol, Qty: 4, BrokerPositionID: oldID}
	core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{original}})
	if o := core.order(stop.ID); !o.Held.PerpetualTriggered || o.Held.ChildClientID != "" {
		t.Fatalf("crossed stop must wait for the submitted add: %+v", o)
	}
	core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue})
	if core.order(stop.ID).Working() {
		t.Fatal("old stop did not retire at flat")
	}
	report := PerpetualObservation{Venue: venue, OrderID: add.ID, BrokerOrderID: "123", Status: StatusFilled, PositionID: lateID,
		Fills: []PerpetualFill{{ID: "late", Qty: 1, Price: 100_000, TsMs: core.now()}}, Persisted: make(chan bool, 1)}
	core.handleBrokerEvent(ctx, report)
	if !<-report.Persisted {
		t.Fatal("confirmed late add was not reconciled")
	}
	lateStop := core.order(add.ID).Perpetual.ProtectionID
	unrelated := Position{Venue: venue, Symbol: bingxTestSymbol, Qty: 1, BrokerPositionID: "90071992547409957"}
	core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{unrelated}})
	if core.order(lateStop).ID != "" {
		t.Fatal("late fill adopted an unrelated one-way position")
	}
	late := unrelated
	late.BrokerPositionID = lateID
	core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{late}})
	select {
	case req := <-broker.submits:
		if req.ClientOrderID != lateStop || !req.Perpetual.WholePosition || req.Perpetual.PositionID != lateID || req.Qty != 1 {
			t.Fatalf("late fill submitted an incorrect protective close: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("proven late fill did not receive its own protective close")
	}
}
