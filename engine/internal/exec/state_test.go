package exec

import (
	"reflect"
	"testing"
)

// submitEv builds an OrderSubmitted for a fresh working order.
func submitEv(v VenueID, id, sym string, side Side, qty, limit float64, ts int64) OrderSubmitted {
	return OrderSubmitted{Order: Order{Venue: v, ID: id, Symbol: sym, Side: side, Type: TypeLimit,
		TIF: TIFDay, Qty: qty, LimitPrice: limit, Status: StatusSubmitted, LeavesQty: qty,
		CreatedMs: ts, UpdatedMs: ts}}
}

func TestApplyOrderLifecycle(t *testing.T) {
	venues := []VenueID{"sim-1"}
	s := NewState(venues)
	s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000))
	if v, ok := s.OrderVenue("ET1"); !ok || v != "sim-1" {
		t.Fatalf("order index missing ET1: %v %v", v, ok)
	}
	o := s.Venue("sim-1").Orders["ET1"]
	if o.Status != StatusSubmitted || !o.Working() {
		t.Fatalf("after submit: %+v", o)
	}
	s.Apply(OrderAccepted{V: "sim-1", OID: "ET1", BrokerOrderID: "B1", Ts: 1001})
	if s.Venue("sim-1").Orders["ET1"].Status != StatusAccepted {
		t.Fatalf("after accept: %+v", s.Venue("sim-1").Orders["ET1"])
	}
	s.Apply(OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1002}, CumQty: 10, LeavesQty: 0, AvgPrice: 100})
	o = s.Venue("sim-1").Orders["ET1"]
	if o.Status != StatusFilled || o.ExecutedQty != 10 || o.LeavesQty != 0 || o.AvgFillPrice != 100 || o.Working() {
		t.Fatalf("after fill: %+v", o)
	}
	if len(s.Venue("sim-1").Fills) != 1 {
		t.Fatalf("fills = %d, want 1", len(s.Venue("sim-1").Fills))
	}
}

func TestApplyPartialThenFill(t *testing.T) {
	s := NewState([]VenueID{"sim-1"})
	s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000))
	s.Apply(OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 4, Price: 100, TsMs: 1001}, CumQty: 4, LeavesQty: 6, AvgPrice: 100})
	if o := s.Venue("sim-1").Orders["ET1"]; o.Status != StatusPartiallyFilled || o.LeavesQty != 6 {
		t.Fatalf("after partial: %+v", o)
	}
	s.Apply(OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 6, Price: 101, TsMs: 1002}, CumQty: 10, LeavesQty: 0, AvgPrice: 100.6})
	if o := s.Venue("sim-1").Orders["ET1"]; o.Status != StatusFilled || o.ExecutedQty != 10 || o.AvgFillPrice != 100.6 {
		t.Fatalf("after full: %+v", o)
	}
}

func TestApplyBlockedAndCancel(t *testing.T) {
	s := NewState([]VenueID{"sim-1"})
	s.Apply(OrderBlocked{V: "sim-1", OID: "ETb", Req: OrderRequest{Venue: "sim-1", Symbol: "AAPL", ClientOrderID: "ETb"}, Reason: "master disarmed", Ts: 1000})
	// Blocked orders are recorded (duplicate-ID defense) but terminal + not working.
	if _, ok := s.OrderVenue("ETb"); !ok {
		t.Fatal("blocked order not indexed")
	}
	if o := s.Venue("sim-1").Orders["ETb"]; o.Status != StatusBlocked || o.Working() {
		t.Fatalf("blocked order: %+v", o)
	}
	s.Apply(submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1001))
	s.Apply(OrderCanceled{V: "sim-1", OID: "ET1", Ts: 1002})
	if o := s.Venue("sim-1").Orders["ET1"]; o.Status != StatusCanceled || o.Working() {
		t.Fatalf("after cancel: %+v", o)
	}
}

// The core invariant: folding a log twice yields byte-identical state, and the
// fold is chunking-invariant (same events, any grouping, same state).
func TestReplayEqualsState(t *testing.T) {
	venues := []VenueID{"sim-1", "sim-2"}
	log := []Event{
		submitEv("sim-1", "ET1", "AAPL", SideBuy, 10, 100, 1000),
		submitEv("sim-2", "ET2", "MSFT", SideShort, 5, 300, 1001),
		OrderAccepted{V: "sim-1", OID: "ET1", BrokerOrderID: "B1", Ts: 1002},
		OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1003}, CumQty: 10, LeavesQty: 0, AvgPrice: 100},
		OrderCanceled{V: "sim-2", OID: "ET2", Ts: 1004},
		OrderReplaced{V: "sim-1", OID: "ET1", NewQty: 10, NewLimit: 100, Ts: 1005},
	}
	a := Replay(log, venues)
	b := Replay(log, venues)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two replays of the same log differ")
	}
	// Chunking invariance: fold event-by-event vs all-at-once → same state.
	c := NewState(venues)
	for _, ev := range log {
		c.Apply(ev)
	}
	if !reflect.DeepEqual(a, c) {
		t.Fatal("Replay != incremental Apply")
	}
}
