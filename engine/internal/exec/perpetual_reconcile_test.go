package exec

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type mexcRiskBroker struct {
	*heldTestBroker
	fair     float64
	capacity int64
}

func (mexcRiskBroker) Capabilities() Capabilities {
	return Capabilities{ReconciledPerpetual: true, EngineHeldPerpetualRiskEntry: true}
}
func (mexcRiskBroker) ContractRules(symbol string) (instrument.ContractRules, bool) {
	return instrument.ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.001", PriceTick: "1",
		QuantityStep: 1, Minimum: 1, Maximum: 1000, MinLeverage: 1, MaxLeverage: 10, TakerFee: "0.0005",
		TradingEnabled: true, RiskTiers: []instrument.RiskTier{{MaxContracts: 1000, MaxLeverage: 10, Maintenance: .005}}}, symbol == "PERP.MEXC.BTC_USDT"
}
func (b *mexcRiskBroker) FairPrice(string) (float64, bool) { return b.fair, b.fair > 0 }
func (*mexcRiskBroker) PendingOrderIDs() []string          { return nil }
func (b *mexcRiskBroker) OpeningTerms(context.Context, string) (PerpetualOpeningTerms, error) {
	return b.CachedOpeningTerms("")
}
func (b *mexcRiskBroker) CachedOpeningTerms(string) (PerpetualOpeningTerms, error) {
	if b.capacity > 0 {
		limit := PerpetualOpeningLimit{Leverage: 5, MaxContracts: b.capacity}
		return PerpetualOpeningTerms{Long: limit, Short: limit}, nil
	}
	return PerpetualOpeningTerms{
		Long:  PerpetualOpeningLimit{Leverage: 5, MaxContracts: 2},
		Short: PerpetualOpeningLimit{Leverage: 4, MaxContracts: 3},
	}, nil
}

type mexcRiskHistoryFixture struct{ *heldTestStore }

func (s mexcRiskHistoryFixture) ReadExecPerpetualRiskOrderHistories(VenueID) ([]EventEnvelope, error) {
	return s.ReadExecOrderHistoriesFor([]string{"entry", "stop"})
}

func TestMEXCLiveChartRiskEntryChecksCurrentLeverageBeforeArming(t *testing.T) {
	const venue, symbol = VenueID("mexc-live"), "PERP.MEXC.BTC_USDT"
	broker := &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000}
	clk := clock.NewFake(time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC))
	store := &heldTestStore{}
	core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: store, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
		Gate:    GateConfig{Venue: map[VenueID]VenueLimits{venue: {}}},
		Brokers: map[VenueID]Broker{venue: broker}, HeldStopLimitLiveIdentity: map[VenueID]string{venue: "account"}})
	core.heldDemand = &heldDemandStub{}
	core.state.SetMasterArmed(true)
	core.state.SetPositionsReady(venue, true)
	core.state.ReconcileAccount(AccountSnapshot{Venue: venue, AvailableCash: 1000, TsMs: clk.Now().UnixMilli()})
	core.heldLiveAck[venue] = "account"
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = core.Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	cmd := SubmitPerpetualRiskEntry{Venue: venue, Symbol: symbol, Side: SideBuy, Trigger: 100_000, Stop: 99_500,
		Leverage: 1, AutoLeverage: true, Budget: 10, MaxQty: 7}
	if ack := core.DoContext(ctx, cmd); ack.Accepted || !strings.Contains(ack.Reason, "isolated leverage is 5×") {
		t.Fatalf("stale preview should block arming: %+v", ack)
	}
	cmd.Leverage = 5
	ack := core.DoContext(ctx, cmd)
	if !ack.Accepted {
		t.Fatalf("reviewed account leverage should arm: %+v", ack)
	}
	history, err := store.ReadExecOrderHistoriesFor([]string{ack.OrderID})
	if err != nil || len(history) != 1 || history[0].Kind != "order_submitted" {
		t.Fatalf("expected one held submission: %+v, %v", history, err)
	}
	event, err := DecodeEvent(history[0].Kind, history[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	order := event.(OrderSubmitted).Order
	if order.Qty != 2 || order.Perpetual.Leverage != 5 || order.Held.Phase != HeldWaiting {
		t.Fatalf("did not honor private leverage and quantity: %+v", order)
	}
	edit := core.DoContext(ctx, ReplaceOrder{Venue: venue, OrderID: order.ID, ExpectedHeldPhase: HeldWaiting,
		ExpectedStopPrice: cmd.Trigger, ExpectedProtectionPrice: cmd.Stop, StopPrice: cmd.Trigger + 1, Qty: 7})
	if !edit.Accepted {
		t.Fatalf("could not amend the trigger: %+v", edit)
	}
	history, err = store.ReadExecOrderHistoriesFor([]string{order.ID})
	if err != nil || len(history) != 2 || history[1].Kind != "risk_entry_changed" {
		t.Fatalf("expected one modification after arming: %+v, %v", history, err)
	}
	event, err = DecodeEvent(history[1].Kind, history[1].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if amended := event.(RiskEntryChanged).Entry; amended.Qty != 2 {
		t.Fatalf("amendment exceeded the verified contract limit: %+v", amended)
	}
}

type mexcLegacyRejectionBroker struct {
	*mexcRiskBroker
	recovered bool
}

func (*mexcLegacyRejectionBroker) PendingOrderIDs() []string { return []string{"entry"} }
func (b *mexcLegacyRejectionBroker) RecoverPerpetualRejection(id, symbol, reason string) (string, error) {
	if id == "entry" && symbol == "PERP.MEXC.BTC_USDT" && reason == "MEXC Futures rejected the request (code 6002)" {
		b.recovered = true
		return "MEXC forbids opening this position (6002)", nil
	}
	return "", nil
}

func TestMEXCChartRiskEntryRestoresDefinitiveRejectionAcrossRestart(t *testing.T) {
	const venue, symbol = VenueID("mexc-live"), "PERP.MEXC.BTC_USDT"
	now := time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC)
	broker := &mexcLegacyRejectionBroker{mexcRiskBroker: &mexcRiskBroker{heldTestBroker: newHeldTestBroker()}}
	store := mexcRiskHistoryFixture{&heldTestStore{}}
	cfg := CoreConfig{Venues: []VenueID{venue}, Clock: clock.NewFake(now.Add(-48 * time.Hour)), Store: store,
		Brokers: map[VenueID]Broker{venue: broker}, HeldStopLimitLiveIdentity: map[VenueID]string{venue: "account"}}
	before := NewCore(cfg)
	order := Order{Venue: venue, ID: "entry", Symbol: symbol, Side: SideBuy, Type: TypeStopLimit, Qty: 7,
		CreatedMs: before.now(), UpdatedMs: before.now(),
		Held:      &HeldOrder{Phase: HeldActivating, ChildClientID: "entry"},
		Perpetual: &PerpetualOrder{LocalProtection: true, AccountIdentity: "account", ProtectionID: "stop", ProtectionPrice: 99_000, RiskBudget: 1}}
	if err := before.appendAndFold(OrderSubmitted{Order: order}, SrcLocal); err != nil {
		t.Fatal(err)
	}
	before.handleBrokerEvent(t.Context(), HeldActivationOutcome{V: venue, OID: order.ID, Reason: "MEXC Futures rejected the request (code 6002)"})
	held := *before.order(order.ID).Held
	held.Phase, held.PausedReason, held.CancelRequested = HeldCancelRequested, "shutdown cancel unconfirmed", true
	if err := before.appendAndFold(HeldOrderChanged{V: venue, OID: order.ID, Held: held, Ts: before.now()}, SrcLocal); err != nil {
		t.Fatal(err)
	}
	cfg.Clock = clock.NewFake(now)
	restarted := NewCore(cfg)
	if err := restarted.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if restored := restarted.order(order.ID); !broker.recovered || restored.Status != StatusRejected || !strings.Contains(restored.RejectReason, "6002") {
		t.Fatalf("authoritative rejection was not restored: %+v", restored)
	}
}

func TestMEXCLiveChartRiskEntry(t *testing.T) {
	const venue, symbol = VenueID("mexc-live"), "PERP.MEXC.BTC_USDT"
	for _, side := range []Side{SideBuy, SideShort} {
		t.Run(side.String(), func(t *testing.T) {
			ctx := t.Context()
			clk := clock.NewFake(time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC))
			broker := &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000, capacity: 10}
			demand := &heldDemandStub{}
			core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: &heldTestStore{}, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
				Gate:    GateConfig{Venue: map[VenueID]VenueLimits{venue: {}}},
				Brokers: map[VenueID]Broker{venue: broker}, HeldStopLimitLiveIdentity: map[VenueID]string{venue: "account"}})
			core.heldDemand = demand
			core.state.SetMasterArmed(true)
			core.state.SetPositionsReady(venue, true)
			core.state.ReconcileAccount(AccountSnapshot{Venue: venue, AvailableCash: 500, TsMs: clk.Now().UnixMilli()})
			trigger, stop, crossed := 101_000.0, 100_500.0, 101_001.0
			if side == SideShort {
				trigger, stop, crossed = 99_000, 99_500, 98_999
			}
			cmd := SubmitPerpetualRiskEntry{Venue: venue, Symbol: symbol, Side: side, Trigger: trigger, Stop: stop,
				Leverage: 5, Cushion: LimitCushion{Value: 1, Unit: "$"}, Budget: 10, MaxQty: 3}
			if ack := core.handlePerpetualRiskEntry(ctx, cmd); ack.Accepted || !strings.Contains(ack.Reason, "enable Stop-Limit") {
				t.Fatalf("entry without live acknowledgement: %+v", ack)
			}
			core.heldLiveAck[venue] = "account"
			ack := core.handlePerpetualRiskEntry(ctx, cmd)
			if !ack.Accepted || core.order(ack.OrderID).Held.Phase != HeldWaiting {
				t.Fatalf("live risk entry not held: %+v", ack)
			}
			step := 1.0
			if side == SideShort {
				step = -1
			}
			trigger, stop, crossed = trigger+10*step, stop-10*step, crossed+10*step
			edit := ReplaceOrder{Venue: venue, OrderID: ack.OrderID, ExpectedHeldPhase: HeldWaiting,
				ExpectedStopPrice: cmd.Trigger, ExpectedProtectionPrice: cmd.Stop, StopPrice: trigger, Qty: 4}
			if amended := core.handleReplace(ctx, edit); !amended.Accepted {
				t.Fatalf("could not move entry trigger: %+v", amended)
			}
			if o := core.order(edit.OrderID); o.StopPrice != trigger || o.LimitPrice != trigger+step || o.LeavesQty != 4 {
				t.Fatalf("entry trigger, reviewed limit or quantity did not update: %+v", o)
			}
			edit.PendingProtection, edit.StopPrice, edit.Qty = true, stop, 3
			if stale := core.handleReplace(ctx, edit); stale.Accepted {
				t.Fatal("edit accepted using old entry trigger")
			}
			edit.ExpectedStopPrice = trigger
			edit.StopPrice = trigger + step
			if wrongSide := core.handleReplace(ctx, edit); wrongSide.Accepted {
				t.Fatal("protection accepted on the profit side of the pending entry")
			}
			edit.StopPrice = stop
			if amended := core.handleReplace(ctx, edit); !amended.Accepted {
				t.Fatalf("could not move pending protection: %+v", amended)
			}
			if o := core.order(edit.OrderID); o.Perpetual.ProtectionPrice != stop || o.StopPrice != trigger || o.Qty != 3 {
				t.Fatalf("pending protection did not preserve entry trigger and resize: %+v", o)
			}
			edit.ExpectedProtectionPrice = stop
			book := TrailBook{Symbol: symbol, Bid: trigger - 1, Ask: trigger + 1, ReceiptMs: core.now()}
			core.handlePerpetualBook(ctx, book)
			clk.Advance(time.Millisecond)
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Price: 100_000, Seq: 1, TsMs: core.now(), RecvTsMs: core.now()})
			if o := core.order(ack.OrderID); o.Held.Phase != HeldArmed || o.Held.ChildClientID != "" {
				t.Fatalf("entry must wait for the trade trigger: %+v", o)
			}
			if side == SideShort {
				clk.Advance(time.Millisecond)
				core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Price: crossed, Seq: 2, TsMs: core.now(), RecvTsMs: core.now()})
				if o := core.order(ack.OrderID); o.Held.Phase != HeldPaused || o.Held.ChildClientID != "" ||
					!strings.Contains(o.Held.PausedReason, "protective stop is already crossed") {
					t.Fatalf("entry must pause if the trade crosses while fair price is still past the stop: %+v", o)
				}
				if resumed := core.handleResumeHeld(ctx, ResumeHeldOrder{Venue: venue, OrderID: ack.OrderID}); !resumed.Accepted {
					t.Fatalf("resume risk entry: %+v", resumed)
				}
			}
			broker.fair = 0
			clk.Advance(time.Millisecond)
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Price: crossed, Seq: 3, TsMs: core.now(), RecvTsMs: core.now()})
			if o := core.order(ack.OrderID); o.Held.ChildClientID != "" || o.Held.Phase == HeldPaused {
				t.Fatalf("entry should wait for fair price: %+v", o.Held)
			}
			broker.fair = trigger
			clk.Advance(time.Millisecond)
			book.ReceiptMs = core.now()
			core.handlePerpetualBook(ctx, book)
			select {
			case req := <-broker.submits:
				if req.Venue != venue || req.ClientOrderID != ack.OrderID || req.Type != TypeLimit || req.TIF != TIFGTC ||
					req.Side != side || req.Qty != 3 || req.LimitPrice != trigger+2*step || req.Perpetual.ProtectionPrice != stop || req.Perpetual.Leverage != 5 {
					t.Fatalf("wrong live limit request: %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("live risk entry did not activate")
			}
			if racing := core.handleReplace(ctx, edit); racing.Accepted {
				t.Fatal("pending stop edit accepted after limit dispatch")
			}
			report := PerpetualObservation{Venue: venue, OrderID: ack.OrderID, BrokerOrderID: "123", Status: StatusAccepted,
				Fills: []PerpetualFill{{ID: "1", Qty: 2, Price: trigger, TsMs: core.now()}}, Persisted: make(chan bool, 1)}
			core.handleBrokerEvent(ctx, report)
			if !<-report.Persisted || core.order(ack.OrderID).Held.Phase != HeldWorking {
				t.Fatal("first partial did not settle the child identity")
			}
			report.Status, report.PositionID = StatusCanceled, "777"
			core.handleBrokerEvent(ctx, report)
			if o := core.order(ack.OrderID); !<-report.Persisted || o.Status != StatusCanceled || o.ExecutedQty != 2 {
				t.Fatalf("limit remainder did not reconcile: %+v", o)
			}
			if _, held := demand.held[ack.OrderID]; held {
				t.Fatal("terminal child kept its ticker lease")
			}
			qty, closeSide := 2.0, SideSell
			if side == SideShort {
				qty, closeSide = -2, SideCover
			}
			stopID := core.order(ack.OrderID).Perpetual.ProtectionID
			position := Position{Venue: venue, Symbol: symbol, Qty: qty, BrokerPositionID: "another-position"}
			core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{position}})
			if core.order(stopID).ID != "" {
				t.Fatal("stop was created for another position")
			}
			position.BrokerPositionID = report.PositionID
			core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{position}})
			if o := core.order(stopID); !o.isPerpetualRiskStop() || o.Qty != 2 || o.Side != closeSide || o.StopPrice != stop {
				t.Fatalf("failed to protect the confirmed partial fill: %+v", o)
			}
			core.handleArm(ctx, false)
			amendedStop := stop + 2*step
			if amended := core.handleReplace(ctx, ReplaceOrder{Venue: venue, OrderID: stopID, ExpectedHeldPhase: HeldWaiting,
				ExpectedStopPrice: stop, StopPrice: amendedStop}); !amended.Accepted {
				t.Fatalf("could not move active protective stop while disarmed: %+v", amended)
			}
			position.Qty /= 2
			core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{position}})
			clk.Advance(time.Millisecond)
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Price: amendedStop, Seq: 5, TsMs: core.now(), RecvTsMs: core.now()})
			if o := core.order(stopID); !o.Working() || o.Qty != 1 || o.StopPrice != amendedStop || o.Held.ChildClientID != "" {
				t.Fatalf("disarm, reduction or a last trade incorrectly changed the fair-price stop: %+v", o)
			}
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Gap: true})
			broker.fair = amendedStop
			core.checkPerpetualRiskStops(ctx)
			if o := core.order(stopID); o.Held.Phase != HeldPaused || o.Held.ChildClientID != "" {
				t.Fatalf("stop submitted after feed gap without resume: %+v", o)
			}
			if ack := core.handleResumeHeld(ctx, ResumeHeldOrder{Venue: venue, OrderID: stopID}); !ack.Accepted {
				t.Fatalf("could not resume position protection while disarmed: %+v", ack)
			}
			core.checkPerpetualRiskStops(ctx)
			select {
			case req := <-broker.submits:
				if req.Venue != venue || req.ClientOrderID != stopID || req.Type != TypeMarket || req.Qty != 1 ||
					req.Side != closeSide || req.Perpetual.PositionID != position.BrokerPositionID || req.Perpetual.ProtectionPrice != 0 {
					t.Fatalf("wrong position-bound close: %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("fair-price stop did not activate while disarmed")
			}
		})
	}
}

func TestMEXCRiskStopRestartsPausedBeforeJournaledClose(t *testing.T) {
	const venue, symbol = VenueID("v"), "PERP.MEXC.BTC_USDT"
	ctx := t.Context()
	now := time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC)
	previous := clock.NewFake(now.Add(-48 * time.Hour))
	held := mexcRiskHistoryFixture{&heldTestStore{}}
	broker := &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000}
	broker.setPositions([]Position{{Venue: venue, Symbol: symbol, Qty: 2, BrokerPositionID: "555"}})
	config := CoreConfig{Venues: []VenueID{venue}, Clock: previous, Store: held,
		Brokers: map[VenueID]Broker{venue: broker}, HeldStopLimitLiveIdentity: map[VenueID]string{venue: "account"}}
	core := NewCore(config)
	entry := Order{Venue: venue, ID: "entry", Symbol: symbol, Side: SideBuy, Qty: 2, Type: TypeStopLimit,
		Status: StatusSubmitted, CreatedMs: core.now(), UpdatedMs: core.now(),
		Held: &HeldOrder{Phase: HeldWorking, PerpetualPositionID: "555"},
		Perpetual: &PerpetualOrder{Multiplier: .001, ProtectionID: "stop", ProtectionPrice: 99_000, RiskBudget: 10,
			LocalProtection: true, AccountIdentity: "account"}}
	stop := Order{Venue: venue, ID: "stop", Symbol: symbol, Side: SideSell, Qty: 2, Type: TypeStop,
		Status: StatusSubmitted, CreatedMs: core.now(), UpdatedMs: core.now(), StopPrice: 99_000,
		Held:      &HeldOrder{Phase: HeldActivating, ChildClientID: "stop", PerpetualTriggered: true},
		Perpetual: &PerpetualOrder{Multiplier: .001, LocalProtection: true, AccountIdentity: "account", PositionID: "555"}}
	for _, ev := range []Event{
		OrderSubmitted{Order: entry},
		OrderFilled{F: Fill{Venue: venue, OrderID: "entry", Symbol: symbol, Side: SideBuy, Qty: 2, Price: 100_000, TsMs: core.now()},
			CumQty: 2, AvgPrice: 100_000, PositionFromSnapshot: true},
		OrderSubmitted{Order: stop},
	} {
		if err := core.appendAndFold(ev, SrcLocal); err != nil {
			t.Fatal(err)
		}
	}
	config.Clock = clock.NewFake(now)
	restarted := NewCore(config)
	if err := restarted.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	restored := restarted.order("stop")
	if !restored.Working() || restored.Held.Phase != HeldPaused || restored.Held.ChildClientID != "" || !restored.Held.PerpetualTriggered {
		t.Fatalf("stop did not recover in a resumable state: %+v", restored)
	}
	restarted.heldDemand = &heldDemandStub{}
	if ack := restarted.handleResumeHeld(ctx, ResumeHeldOrder{Venue: venue, OrderID: "stop"}); !ack.Accepted {
		t.Fatalf("could not resume stop: %+v", ack)
	}
	select {
	case req := <-broker.submits:
		if req.ClientOrderID != "stop" || req.Perpetual.PositionID != "555" {
			t.Fatalf("resumed the wrong position or stop intent: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed stop did not submit its previously triggered close")
	}
}

type perpetualRecoveryBroker struct {
	*heldTestBroker
	id string
}

func (b *perpetualRecoveryBroker) PendingOrderIDs() []string { return []string{b.id} }

func TestPerpetualRecoveryDeduplicatesPartialFillsAcrossDays(t *testing.T) {
	const venue VenueID = "vst"
	const symbol = "PERP.BINGX.BTC-USDT"
	const id = "ETRESTORE"
	now := time.Date(2026, 11, 19, 15, 30, 0, 0, time.UTC)
	created := now.Add(-72 * time.Hour).UnixMilli()
	store := &heldTestStore{}
	broker := &perpetualRecoveryBroker{heldTestBroker: newHeldTestBroker(), id: id}
	cfg := CoreConfig{Venues: []VenueID{venue}, Brokers: map[VenueID]Broker{venue: broker}, Store: store,
		Clock: clock.NewFake(now)}
	prior := NewCore(cfg)
	req := OrderRequest{Venue: venue, ClientOrderID: id, Symbol: symbol, Side: SideShort, Type: TypeLimit,
		Qty: 8, LimitPrice: 50, Perpetual: &PerpetualOrder{Leverage: 3, Multiplier: .01}}
	if err := prior.appendAndFold(OrderSubmitted{Order: newOrderFromRequest(req, created)}, SrcLocal); err != nil {
		t.Fatal(err)
	}
	first := PerpetualFill{ID: "fill-1", Qty: 3, Price: 51, TsMs: created + 5}
	report := PerpetualObservation{Venue: venue, OrderID: id, BrokerOrderID: "123", Status: StatusAccepted,
		Fills: []PerpetualFill{first}}
	if err := prior.foldPerpetualObservation(report); err != nil {
		t.Fatal(err)
	}

	recovered := NewCore(cfg)
	recovered.state.SetPositionsReady(venue, true)
	recovered.state.ReconcilePositions(venue, []Position{{Venue: venue, Symbol: symbol, Qty: -8}})
	if err := recovered.recoverPerpetualOrders(); err != nil {
		t.Fatal(err)
	}
	report.Status = StatusFilled
	report.Fills = append(report.Fills, PerpetualFill{ID: "fill-2", Qty: 5, Price: 52, TsMs: now.UnixMilli() - 5})
	for range 2 {
		if err := recovered.foldPerpetualObservation(report); err != nil {
			t.Fatal(err)
		}
	}
	q := recovered.state.VenuePositionShares(venue, symbol)
	if o := recovered.order(id); o.Status != StatusFilled || o.ExecutedQty != 8 || o.AvgFillPrice != 51.625 || q != -8 {
		t.Fatalf("unexpected order or authoritative position: order %+v, position %v", o, q)
	}
	var fills int
	for _, env := range store.events {
		ev, err := DecodeEvent(env.Kind, env.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := ev.(OrderFilled); ok {
			fills++
		}
	}
	if fills != 2 {
		t.Fatalf("recorded %d fills; want 2 unique fills", fills)
	}
}
