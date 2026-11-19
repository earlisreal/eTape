package exec

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

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
