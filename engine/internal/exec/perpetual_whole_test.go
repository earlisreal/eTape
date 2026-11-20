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
	terms       PerpetualOpeningTerms
	readStarted chan struct{}
	readFinish  chan struct{}
}

func (b *bingxRiskFixture) ContractRules(symbol string) (instrument.ContractRules, bool) {
	rules, _ := b.mexcRiskBroker.ContractRules("PERP.MEXC.BTC_USDT")
	return rules, symbol == bingxTestSymbol
}
func (*bingxRiskFixture) Capabilities() Capabilities {
	return Capabilities{EngineHeldPerpetualRiskEntry: true, WholePositionPerpetualRiskEntry: true, ReconciledPerpetual: true}
}
func (b *bingxRiskFixture) OpeningTerms(ctx context.Context, _ string) (PerpetualOpeningTerms, error) {
	if b.readStarted != nil {
		b.readStarted <- struct{}{}
		select {
		case <-b.readFinish:
		case <-ctx.Done():
			return PerpetualOpeningTerms{}, ctx.Err()
		}
	}
	return b.terms, nil
}
func (b *bingxRiskFixture) CachedOpeningTerms(string) (PerpetualOpeningTerms, error) {
	return b.terms, nil
}
func (*bingxRiskFixture) AccountIdentity() string { return "virtual-account" }

func TestBingXArmsWithoutBrokerPreflight(t *testing.T) {
	const venue = VenueID("bingx-vst")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	clk := clock.NewFake(time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC))
	broker := &bingxRiskFixture{mexcRiskBroker: &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000},
		readStarted: make(chan struct{}, 2), readFinish: make(chan struct{}),
		terms: PerpetualOpeningTerms{Collateral: 1000, Long: PerpetualOpeningLimit{Leverage: 5, MaxLeverage: 10, MaxContracts: 20},
			Notional: &instrument.NotionalRisk{Mark: 100_000, Tiers: []instrument.NotionalTier{{Maximum: 1_000_000, Rate: 0.005}}}}}
	core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: &heldTestStore{},
		IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))), Brokers: map[VenueID]Broker{venue: broker},
		Gate: GateConfig{Venue: map[VenueID]VenueLimits{venue: {}}}})
	core.heldDemand = &heldDemandStub{}
	core.state.SetMasterArmed(true)
	core.state.SetPositionsReady(venue, true)
	core.state.ReconcileAccount(AccountSnapshot{Venue: venue, AvailableCash: 1000, TsMs: clk.Now().UnixMilli()})
	core.handleEligiblePrint(ctx, EligiblePrint{Symbol: bingxTestSymbol, Price: 100_000, Seq: 1, TsMs: core.now(), RecvTsMs: core.now()})
	core.handlePerpetualBook(ctx, TrailBook{Symbol: bingxTestSymbol, Bid: 100_003, Ask: 100_004, ReceiptMs: core.now()})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = core.Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	// Keep a settings refresh in flight while the chart command is sent.
	go func() { _, _ = core.PreviewPerpetualOpening(ctx, venue, bingxTestSymbol) }()
	select {
	case <-broker.readStarted:
	case <-ctx.Done():
		t.Fatal("background refresh did not start")
	}
	defer close(broker.readFinish)
	reply := make(chan CmdAck, 1)
	go func() {
		reply <- core.DoContext(ctx, SubmitPerpetualRiskEntry{Venue: venue, Symbol: bingxTestSymbol, Side: SideBuy,
			Trigger: 99_900, Stop: 99_500, Budget: 1.08, MaxQty: 2, Leverage: 5, Cushion: LimitCushion{Value: 2, Unit: "$"}})
	}()
	select {
	case <-broker.readStarted:
		t.Fatal("sending the chart entry waited for broker preflight")
	case ack := <-reply:
		if !ack.Accepted || ack.OrderID == "" {
			t.Fatalf("chart entry did not arm: %+v", ack)
		}
		select {
		case child := <-broker.submits:
			if child.ClientOrderID != ack.OrderID || child.Type != TypeLimit || child.TIF != TIFGTC || child.LimitPrice != 100_006 || child.Qty != 1 {
				t.Fatalf("buy trigger under the current price did not send a regular ask-plus-cushion limit: %+v", child)
			}
		case <-ctx.Done():
			t.Fatal("entry did not send from the existing trade and quote")
		}
	case <-ctx.Done():
		t.Fatal("chart entry did not arm")
	}
}

func TestRestingPerpetualEntryMustCancelBeforeProtectiveClose(t *testing.T) {
	const venue = VenueID("bingx-vst")
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC))
	broker := &bingxRiskFixture{mexcRiskBroker: &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 98_000}}
	core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: &heldTestStore{}, Brokers: map[VenueID]Broker{venue: broker}})
	core.heldDemand = &heldDemandStub{}
	const positionID = "position"
	core.state.SetPositionsReady(venue, true)
	core.state.Venue(venue).Positions[bingxTestSymbol] = Position{Qty: 2, BrokerPositionID: positionID}
	entry := Order{ID: "entry", Venue: venue, Symbol: bingxTestSymbol, Side: SideBuy, Type: TypeStopLimit, TIF: TIFGTC,
		Qty: 4, ExecutedQty: 2, LeavesQty: 2, Status: StatusPartiallyFilled,
		Perpetual: &PerpetualOrder{ProtectionID: "stop", ProtectionPrice: 99_000, RiskBudget: 10},
		Held:      &HeldOrder{Phase: HeldWorking, ChildClientID: "entry", ChildBrokerID: "exchange-entry"}}
	stop := Order{ID: "stop", Venue: venue, Symbol: bingxTestSymbol, Side: SideSell, Type: TypeStop, Qty: 2,
		StopPrice: 99_000, Status: StatusSubmitted,
		Perpetual: &PerpetualOrder{Multiplier: .001, PositionID: positionID, AccountIdentity: broker.AccountIdentity(), WholePosition: true, LocalProtection: true},
		Held:      &HeldOrder{Phase: HeldArmed}}
	for _, o := range []Order{entry, stop} {
		if err := core.appendAndFold(OrderSubmitted{Order: o}, SrcLocal); err != nil {
			t.Fatal(err)
		}
	}
	core.activatePerpetualRiskStop(ctx, core.order(stop.ID))
	if !core.order(entry.ID).Held.CancelRequested || core.order(stop.ID).Held.ChildClientID != "" {
		t.Fatal("stop failed to reserve the close until the resting entry is canceled")
	}
	if err := core.appendAndFold(OrderCanceled{V: venue, OID: entry.ID, Ts: core.now()}, SrcReconcile); err != nil {
		t.Fatal(err)
	}
	core.activatePerpetualRiskStop(ctx, core.order(stop.ID))
	select {
	case child := <-broker.submits:
		if child.ClientOrderID != stop.ID || child.Type != TypeMarket || child.Qty != 2 || child.Perpetual.PositionID != positionID {
			t.Fatalf("wrong close after the entry remainder canceled: %+v", child)
		}
	case <-time.After(time.Second):
		t.Fatal("protective close did not send after confirmed cancellation")
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
				broker.terms = PerpetualOpeningTerms{PositionID: p.BrokerPositionID, Collateral: 1000, Long: terms, Short: terms,
					Notional: &instrument.NotionalRisk{Mark: broker.fair, MinimumValue: 2,
						Tiers: []instrument.NotionalTier{{Maximum: 1_000_000, Rate: 0.005}},
						Position: instrument.RiskPosition{Qty: p.Qty, AvgPrice: p.AvgPrice, Margin: p.IsolatedMargin,
							LiquidationPrice: p.LiquidationPrice, Leverage: p.Leverage}}}
				return core.handlePerpetualRiskEntry(ctx, command)
			}
			waiting := func(command SubmitPerpetualRiskEntry, p Position) Order {
				t.Helper()
				ack := prepare(command, p)
				if !ack.Accepted || core.order(ack.OrderID).Held.PerpetualArmPending {
					t.Fatalf("entry did not arm without preflight: %+v", ack)
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
				if o.Held.ChildClientID == "" {
					broker.fair = o.StopPrice
					clk.Advance(time.Millisecond)
					sequence++
					broker.terms.Notional.Mark = broker.fair
					book := TrailBook{Symbol: o.Symbol, Bid: o.StopPrice - 1, Ask: o.StopPrice + 1, ReceiptMs: core.now()}
					core.handlePerpetualBook(ctx, book)
					core.handleEligiblePrint(ctx, EligiblePrint{Symbol: o.Symbol, Price: o.StopPrice, Seq: sequence,
						TsMs: core.now(), RecvTsMs: core.now()})
				}
				request := submission()
				if request.ClientOrderID != o.ID || request.Type != TypeLimit || request.TIF != TIFGTC || request.LimitPrice != o.StopPrice+test.sign ||
					!request.Perpetual.WholePosition || request.Perpetual.ProtectionID != o.Perpetual.ProtectionID {
					t.Fatalf("wrong protected limit: %+v", request)
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
				if ack := prepare(entry, Position{}); ack.Accepted || ack.Reason == "" {
					t.Fatal("live chart entry bypassed account acknowledgement")
				}
				core.heldLiveAck[test.venue] = identity[test.venue]
			}
			root := waiting(entry, Position{})
			trigger(root)
			firstFill := PerpetualFill{ID: "root", Qty: 2, Price: 100_000, TsMs: core.now()}
			reconcile(root, StatusAccepted, position.BrokerPositionID, 0, firstFill)
			position.Qty = 2 * test.sign
			snapshot(position)
			stopID := root.Perpetual.ProtectionID
			stop := core.order(stopID)
			if stop.Qty != 2 || !stop.isPerpetualRiskStop() || stop.Side != test.close || stop.StopPrice != entry.Stop {
				t.Fatalf("working limit did not protect the first partial fill: %+v", stop)
			}
			reconcile(root, StatusFilled, position.BrokerPositionID, 0, firstFill,
				PerpetualFill{ID: "root-final", Qty: 2, Price: 100_000, TsMs: core.now()})
			position.Qty = 4 * test.sign
			snapshot(position)

			add := entry
			add.SharedStopID, add.Trigger, add.Stop, add.MaxQty = stopID, 100_700-700*(1-test.sign), 100_000-test.sign*100, 2
			looser := add
			looser.Stop = entry.Stop - test.sign*50
			if reject := prepare(looser, position); reject.Accepted {
				t.Fatal("a linked add loosened the shared stop")
			}
			zero := waiting(add, position)
			trigger(zero)
			reconcile(zero, StatusCanceled, "", 0)
			if core.order(stopID).StopPrice != entry.Stop {
				t.Fatal("an unfilled add changed the stop")
			}
			linked := waiting(add, position)
			if repeated := prepare(add, position); repeated.Accepted {
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
