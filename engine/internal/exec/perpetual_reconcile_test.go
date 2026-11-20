package exec

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/instrument"
)

type mexcRiskBroker struct {
	*heldTestBroker
	fair float64
}

func (mexcRiskBroker) Capabilities() Capabilities {
	return Capabilities{ReconciledPerpetual: true, EngineHeldPerpetualRiskEntry: true}
}
func (mexcRiskBroker) ContractRules(symbol string) (instrument.ContractRules, bool) {
	return instrument.ContractRules{Base: "BTC", Settlement: "USDT", Multiplier: "0.001", PriceTick: "1",
		QuantityStep: 1, Minimum: 1, Maximum: 1000, MinLeverage: 1, MaxLeverage: 10, TakerFee: "0.0005",
		TradingEnabled: true}, symbol == "PERP.MEXC.BTC_USDT"
}
func (b *mexcRiskBroker) FairPrice(string) (float64, bool) { return b.fair, b.fair > 0 }
func (*mexcRiskBroker) PendingOrderIDs() []string          { return nil }

type mexcRiskHistoryFixture struct{ *heldTestStore }

func (s mexcRiskHistoryFixture) ReadExecPerpetualRiskOrderHistories(VenueID) ([]EventEnvelope, error) {
	return s.ReadExecOrderHistoriesFor([]string{"entry", "stop"})
}

func TestMEXCLiveChartRiskEntry(t *testing.T) {
	const venue, symbol = VenueID("mexc-live"), "PERP.MEXC.BTC_USDT"
	for _, side := range []Side{SideBuy, SideShort} {
		t.Run(side.String(), func(t *testing.T) {
			ctx := t.Context()
			clk := clock.NewFake(time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC))
			broker := &mexcRiskBroker{heldTestBroker: newHeldTestBroker(), fair: 100_000}
			demand := &heldDemandStub{}
			core := NewCore(CoreConfig{Venues: []VenueID{venue}, Clock: clk, Store: &heldTestStore{}, IDGen: NewOrderIDGen(clk, rand.New(rand.NewSource(1))),
				Gate:    GateConfig{Venue: map[VenueID]VenueLimits{venue: {}}},
				Brokers: map[VenueID]Broker{venue: broker}, HeldStopLimitLiveIdentity: map[VenueID]string{venue: "account"}})
			core.heldDemand = demand
			core.state.SetMasterArmed(true)
			core.state.SetPositionsReady(venue, true)
			core.state.ReconcileAccount(AccountSnapshot{Venue: venue, AvailableCash: 500, TsMs: clk.Now().UnixMilli()})
			trigger, stop, crossed := 101_000.0, 99_000.0, 101_001.0
			if side == SideShort {
				trigger, stop, crossed = 99_000, 101_000, 98_999
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
			clk.Advance(time.Millisecond)
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Price: crossed, Seq: 1, TsMs: core.now(), RecvTsMs: core.now()})
			select {
			case req := <-broker.submits:
				if req.Venue != venue || req.ClientOrderID != ack.OrderID || req.Type != TypeLimit || req.TIF != TIFIOC ||
					req.Side != side || req.Qty != 3 || req.Perpetual.ProtectionPrice != 0 || req.Perpetual.Leverage != 5 {
					t.Fatalf("wrong live IOC request: %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("live risk entry did not activate")
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
				t.Fatalf("IOC remainder did not reconcile: %+v", o)
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
			position.Qty /= 2
			core.handleBrokerEvent(ctx, BrokerSnapshot{V: venue, Positions: []Position{position}})
			clk.Advance(time.Millisecond)
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Price: stop, Seq: 2, TsMs: core.now(), RecvTsMs: core.now()})
			if o := core.order(stopID); !o.Working() || o.Qty != 1 || o.Held.ChildClientID != "" {
				t.Fatalf("disarm, reduction or a last trade incorrectly changed the fair-price stop: %+v", o)
			}
			core.handleEligiblePrint(ctx, EligiblePrint{Symbol: symbol, Gap: true})
			broker.fair = stop
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
