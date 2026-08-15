package exec

import (
	"fmt"
	"sort"
)

// ClosedOrder is the read-only historical projection of one terminal order
// leg. RowID is stable for ordinary terminal orders and is event-seq-derived
// for replaced legs, so the live domain order ID can remain stable across a
// broker replacement.
type ClosedOrder struct {
	RowID string
	Order Order
}

type closedOrders struct {
	active            map[string]Order
	rows              map[string]ClosedOrder
	seeded            map[string]bool
	replacementSerial uint64
}

func newClosedOrders() *closedOrders {
	return &closedOrders{active: map[string]Order{}, rows: map[string]ClosedOrder{}, seeded: map[string]bool{}}
}

func (p *closedOrders) apply(ev Event, seq int64) []ClosedOrder {
	switch e := ev.(type) {
	case OrderSubmitted:
		p.active[e.Order.ID] = e.Order
		p.seeded[e.Order.ID] = true
	case OrderBlocked:
		o := Order{Venue: e.V, ID: e.OID, Symbol: e.Req.Symbol, Side: e.Req.Side,
			Type: e.Req.Type, TIF: e.Req.TIF, Session: e.Req.Session, Qty: e.Req.Qty,
			LimitPrice: e.Req.LimitPrice, StopPrice: e.Req.StopPrice, Status: StatusBlocked,
			RejectReason: e.Reason, CreatedMs: e.Ts, UpdatedMs: e.Ts}
		return []ClosedOrder{p.close(o, o.ID)}
	case OrderAccepted:
		p.mutate(e.V, e.OID, e.Ts, func(o *Order) { o.Status = StatusAccepted })
	case OrderRejected:
		return p.terminal(e.V, e.OID, e.Ts, StatusRejected, e.Reason)
	case OrderFilled:
		return p.fill(e)
	case OrderCanceled:
		return p.terminal(e.V, e.OID, e.Ts, StatusCanceled, "")
	case OrderExpired:
		return p.terminal(e.V, e.OID, e.Ts, StatusExpired, "")
	case OrderReplaced:
		return p.replace(e, seq)
	}
	return nil
}

func (p *closedOrders) mutate(v VenueID, id string, ts int64, fn func(*Order)) {
	o, ok := p.active[id]
	if !ok || o.Venue != v {
		return
	}
	fn(&o)
	o.UpdatedMs = ts
	p.active[id] = o
}

func (p *closedOrders) terminal(v VenueID, id string, ts int64, status OrderStatus, reason string) []ClosedOrder {
	o, ok := p.active[id]
	if !ok || o.Venue != v || !o.Working() {
		return nil
	}
	o.Status = status
	o.UpdatedMs = ts
	if reason != "" {
		o.RejectReason = reason
	}
	delete(p.active, id)
	return []ClosedOrder{p.close(o, o.ID)}
}

func (p *closedOrders) fill(e OrderFilled) []ClosedOrder {
	o, ok := p.active[e.F.OrderID]
	if !ok {
		o = Order{Venue: e.F.Venue, ID: e.F.OrderID, Symbol: e.F.Symbol, Side: e.F.Side, CreatedMs: e.F.TsMs}
	}
	o.ExecutedQty = e.CumQty
	o.LeavesQty = e.LeavesQty
	o.AvgFillPrice = e.AvgPrice
	o.UpdatedMs = e.F.TsMs
	if e.LeavesQty > 0 {
		o.Status = StatusPartiallyFilled
		p.active[o.ID] = o
		return nil
	}
	if !ok && p.rows[o.ID].Order.Status == StatusFilled {
		return nil
	}
	o.Status = StatusFilled
	delete(p.active, o.ID)
	return []ClosedOrder{p.close(o, o.ID)}
}

func (p *closedOrders) replace(e OrderReplaced, seq int64) []ClosedOrder {
	o, ok := p.active[e.OID]
	if !ok || o.Venue != e.V || !o.Working() {
		return nil
	}
	old := o
	old.Status = StatusReplaced
	old.UpdatedMs = e.Ts
	rowID := fmt.Sprintf("%s#replace-%d", old.ID, seq)
	if seq <= 0 {
		p.replacementSerial++
		rowID = fmt.Sprintf("%s#replace-%d", old.ID, p.replacementSerial)
	}
	row := p.close(old, rowID)
	if e.NewQty > 0 {
		o.Qty = e.NewQty
	}
	if e.NewLimit > 0 {
		o.LimitPrice = e.NewLimit
	}
	if e.NewStop > 0 {
		o.StopPrice = e.NewStop
	}
	o.LeavesQty = o.Qty - o.ExecutedQty
	o.Status = StatusAccepted
	o.UpdatedMs = e.Ts
	p.active[e.OID] = o
	return []ClosedOrder{row}
}

func (p *closedOrders) close(o Order, rowID string) ClosedOrder {
	row := ClosedOrder{RowID: rowID, Order: o}
	p.rows[rowID] = row
	return row
}

func (p *closedOrders) adopt(o Order) {
	if o.Working() {
		p.active[o.ID] = o
	}
}

func (p *closedOrders) hasSeed(id string) bool { return p.seeded[id] }

func (p *closedOrders) seedState(s *State) {
	for _, vs := range s.Venues {
		for _, o := range vs.Orders {
			if o.Working() {
				if _, exists := p.active[o.ID]; !exists {
					p.active[o.ID] = o
				}
			}
		}
	}
}

func (p *closedOrders) snapshotSince(cutoffMs int64) []ClosedOrder {
	out := make([]ClosedOrder, 0, len(p.rows))
	for _, row := range p.rows {
		if row.Order.UpdatedMs < cutoffMs {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order.UpdatedMs != out[j].Order.UpdatedMs {
			return out[i].Order.UpdatedMs < out[j].Order.UpdatedMs
		}
		return out[i].RowID < out[j].RowID
	})
	return out
}
