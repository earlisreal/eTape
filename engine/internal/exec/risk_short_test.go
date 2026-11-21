package exec

import (
	"context"
	"testing"
	"time"
)

func (*heldTestBroker) StockShortCapacity(context.Context, string, float64) (float64, error) {
	return 1000, nil
}

func TestStockRiskShortSharedCover(t *testing.T) {
	c, clk, b, ctx := newRiskCoreWithoutPrint(t)
	submit := func(trigger, stop, maxQty float64, owner string) Order {
		t.Helper()
		ack := c.Do(SubmitRiskEntry{Venue: "v", Symbol: "AAPL", Side: SideShort, BuyStop: trigger, SellStop: stop,
			Mode: "Dollar", Value: 100, MaxQty: maxQty, SharedEntryID: owner})
		if !ack.Accepted {
			t.Fatal(ack)
		}
		return waitOrder(t, c, ack.OrderID, func(o Order) bool { return o.RiskEntry != nil })
	}
	print := func(seq int64, price float64) {
		clk.Advance(time.Millisecond)
		feedCorroboratedPrint(t, c, ctx, eligible(seq, clk.Now(), price))
	}
	expectChild := func(id string, side Side, qty, limit float64) {
		t.Helper()
		select {
		case req := <-b.submits:
			if req.ClientOrderID != id || req.Side != side || req.Qty != qty || req.LimitPrice != limit {
				t.Fatalf("unexpected child: %+v", req)
			}
		case <-time.After(time.Second):
			t.Fatal("missing linked order child")
		}
	}
	fill := func(o Order, qty, leaves, price float64) {
		b.ev <- OrderAccepted{V: "v", OID: o.ID, Ts: clk.Now().UnixMilli()}
		b.ev <- OrderFilled{F: Fill{Venue: "v", OrderID: o.ID, Symbol: o.Symbol, Side: o.Side, Qty: qty, Price: price, TsMs: clk.Now().UnixMilli()},
			CumQty: qty, LeavesQty: leaves, AvgPrice: price}
	}
	initial := submit(10, 11, 100, "")
	print(1, 10.5)
	waitHeldOrder(t, c, initial.ID, HeldArmed)
	select {
	case req := <-b.submits:
		t.Fatalf("short triggered above entry: %+v", req)
	default:
	}
	print(2, 10)
	expectChild(initial.ID, SideShort, 100, 10)
	fill(initial, 100, 0, 10)
	stop := waitOrder(t, c, initial.RiskEntry.StopID, func(o Order) bool { return o.Side == SideCover && o.Qty == 100 })
	add := submit(9.5, 10.5, 50, initial.ID)
	print(3, 9.5)
	expectChild(add.ID, SideShort, 50, 9.5)
	fill(add, 20, 30, 9.5)
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 120 && o.StopPrice == 10.5 })
	b.ev <- BrokerPosition{Position: Position{Venue: "v", Symbol: "AAPL", Qty: -110, AvgPrice: 9.9}}
	waitOrder(t, c, stop.ID, func(o Order) bool { return o.Qty == 110 })
	print(4, 10.5)
	expectChild(stop.ID, SideCover, 110, 10.5)
}
