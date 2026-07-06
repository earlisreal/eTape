package exec

import (
	"reflect"
	"testing"
)

func allEvents() []Event {
	return []Event{
		OrderSubmitted{Order: Order{Venue: "sim-1", ID: "ET1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100, Status: StatusSubmitted, LeavesQty: 10, CreatedMs: 1000, UpdatedMs: 1000}},
		OrderAccepted{V: "sim-1", OID: "ET1", BrokerOrderID: "B1", Ts: 1001},
		OrderRejected{V: "sim-1", OID: "ET1", Reason: "R114", Ts: 1002},
		OrderBlocked{V: "sim-1", OID: "ET1", Req: OrderRequest{Venue: "sim-1", Symbol: "AAPL", Side: SideBuy, Type: TypeLimit, Qty: 10, LimitPrice: 100, ClientOrderID: "ET1"}, Reason: "master disarmed", Ts: 1003},
		OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1004}, CumQty: 10, LeavesQty: 0, AvgPrice: 100},
		OrderCanceled{V: "sim-1", OID: "ET1", Ts: 1005},
		OrderExpired{V: "sim-1", OID: "ET1", Ts: 1006},
		OrderReplaced{V: "sim-1", OID: "ET1", NewQty: 20, NewLimit: 101, Ts: 1007},
		StreamGap{V: "sim-1", Ts: 1008},
	}
}

func TestEventCodecRoundTrip(t *testing.T) {
	for _, ev := range allEvents() {
		kind, payload, err := EncodeEvent(ev)
		if err != nil {
			t.Fatalf("EncodeEvent(%T) error: %v", ev, err)
		}
		got, err := DecodeEvent(kind, payload)
		if err != nil {
			t.Fatalf("DecodeEvent(%q) error: %v", kind, err)
		}
		if !reflect.DeepEqual(got, ev) {
			t.Errorf("round-trip %q:\n got %#v\nwant %#v", kind, got, ev)
		}
	}
}

func TestDecodeUnknownKind(t *testing.T) {
	if _, err := DecodeEvent("nope", []byte("{}")); err == nil {
		t.Fatal("DecodeEvent(unknown) = nil error, want error")
	}
}

func TestEnvelopeAndFillRow(t *testing.T) {
	ev := OrderFilled{F: Fill{Venue: "sim-1", OrderID: "ET1", Symbol: "AAPL", Side: SideBuy, Qty: 10, Price: 100, TsMs: 1004}, CumQty: 10, LeavesQty: 0, AvgPrice: 100}
	env := EnvelopeOf(ev, SrcWS, 42)
	if env.Seq != 42 || env.Source != "ws" || env.Venue != "sim-1" || env.OrderID != "ET1" || env.Kind != "order_filled" || env.TsMs != 1004 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	fr, ok := FillRowOf(ev)
	if !ok || fr.OrderID != "ET1" || fr.Side != "BUY" || fr.Qty != 10 || fr.Price != 100 || fr.Venue != "sim-1" {
		t.Fatalf("fill row wrong: %+v ok=%v", fr, ok)
	}
	if _, ok := FillRowOf(OrderCanceled{V: "sim-1", OID: "ET1", Ts: 1}); ok {
		t.Fatal("FillRowOf(OrderCanceled) ok=true, want false")
	}
}
