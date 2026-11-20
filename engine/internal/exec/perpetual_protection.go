package exec

import (
	"context"
	"math"
)

func (o Order) isPerpetualRiskStop() bool {
	return o.Type == TypeStop && o.Perpetual != nil && o.Perpetual.LocalProtection && o.Held != nil
}

func (o Order) perpetualPositionID() string {
	if o.Held != nil && o.Perpetual.isRiskEntry() {
		return o.Held.PerpetualPositionID
	}
	return o.Perpetual.PositionID
}

func (c *Core) ownsPerpetualPosition(o Order, p Position) bool {
	id := o.perpetualPositionID()
	return id != "" && p.BrokerPositionID == id &&
		o.Perpetual.AccountIdentity != "" && o.Perpetual.AccountIdentity == c.heldLiveIdentity[o.Venue] &&
		p.Qty != 0 && (p.Qty > 0) == (o.Side == SideBuy || o.Side == SideSell)
}

// Called after the adapter has reconciled the entry and published a current
// position snapshot. The IOC child must be terminal before a close can use
// the journal's reservation on this contract.
func (c *Core) syncPerpetualProtection(ctx context.Context, v VenueID) {
	b, ok := c.brokers[v].(PerpetualBroker)
	if !ok || !b.Capabilities().EngineHeldPerpetualRiskEntry || !c.state.PositionsReady(v) || c.heldDemand == nil {
		return
	}
	vs := c.state.Venue(v)
	for _, entry := range vs.Orders {
		o := entry.Perpetual
		if o == nil || !o.LocalProtection || !o.isRiskEntry() || entry.Working() || entry.ExecutedQty <= 0 ||
			vs.Orders[o.ProtectionID].ID != "" || !c.ownsPerpetualPosition(entry, vs.Positions[entry.Symbol]) {
			continue
		}
		side := SideSell
		if entry.Side == SideShort {
			side = SideCover
		}
		qty := min(entry.ExecutedQty, math.Abs(vs.Positions[entry.Symbol].Qty))
		req := OrderRequest{Venue: v, Symbol: entry.Symbol, Side: side, Type: TypeStop, TIF: TIFIOC, Session: SessionAuto,
			Qty: qty, StopPrice: o.ProtectionPrice, ClientOrderID: o.ProtectionID,
			Perpetual: &PerpetualOrder{Multiplier: o.Multiplier, LocalProtection: true, PositionID: entry.perpetualPositionID(), AccountIdentity: o.AccountIdentity}}
		stop := newOrderFromRequest(req, c.now())
		stop.Held = &HeldOrder{Phase: HeldWaiting}
		if err := c.heldDemand.Acquire(ctx, stop.ID, stop.Symbol); err != nil {
			stop.Held.Phase, stop.Held.PausedReason = HeldPaused, "fair-price demand unavailable; resume manually"
		}
		if err := c.appendAndFold(OrderSubmitted{Order: stop}, SrcLocal); err != nil {
			c.heldDemand.Release(stop.ID)
			c.syslog("exec.perpetual", "persist protective stop: "+err.Error())
		}
	}
	for _, stop := range vs.Orders {
		if !stop.isPerpetualRiskStop() || !stop.Working() || stop.Held.ChildClientID != "" {
			continue
		}
		p := vs.Positions[stop.Symbol]
		if !c.ownsPerpetualPosition(stop, p) {
			_ = c.handleCancel(ctx, CancelOrder{Venue: v, OrderID: stop.ID})
			continue
		}
		if qty := math.Abs(p.Qty); qty < stop.LeavesQty {
			held := *stop.Held
			held.ResolvedQty = qty
			if err := c.appendAndFold(HeldOrderChanged{V: v, OID: stop.ID, Held: held, Ts: c.now()}, SrcReconcile); err != nil {
				c.syslog("exec.perpetual", "resize protective stop: "+err.Error())
			}
		}
	}
}

func (c *Core) checkPerpetualRiskStops(ctx context.Context) {
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.isPerpetualRiskStop() && (o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed) {
			c.activatePerpetualRiskStop(ctx, o)
		}
	}
}

func (c *Core) activatePerpetualRiskStop(ctx context.Context, stop Order) {
	b, ok := c.brokers[stop.Venue].(PerpetualBroker)
	p := c.state.Venue(stop.Venue).Positions[stop.Symbol]
	if !ok || c.heldDemand == nil || !c.state.PositionsReady(stop.Venue) || !c.ownsPerpetualPosition(stop, p) {
		return
	}
	held := *stop.Held
	if !held.PerpetualTriggered {
		price, fresh := b.FairPrice(stop.Symbol)
		if !fresh || !StopLimitTriggered(stop.Side, price, stop.StopPrice) {
			return
		}
		held.PerpetualTriggered = true
		if err := c.appendAndFold(HeldOrderChanged{V: stop.Venue, OID: stop.ID, Held: held, Ts: c.now()}, SrcLocal); err != nil {
			c.syslog("exec.perpetual", "persist protective stop trigger: "+err.Error())
			return
		}
	}
	req := OrderRequest{Venue: stop.Venue, Symbol: stop.Symbol, Side: stop.Side, Type: TypeMarket,
		TIF: TIFIOC, Session: SessionAuto, Qty: min(stop.LeavesQty, math.Abs(p.Qty)),
		ClientOrderID: stop.ID, Perpetual: &PerpetualOrder{Multiplier: stop.Perpetual.Multiplier, PositionID: stop.Perpetual.PositionID}}
	if err := c.validatePerpetual(&req, stop.ID); err != nil {
		return
	}
	if ok, _ := c.checkSellLeaves(req, req.Qty, stop.ID); !ok {
		return
	}
	held.Phase, held.ChildClientID, held.ResolvedQty = HeldActivating, stop.ID, req.Qty
	if err := c.appendAndFold(HeldOrderChanged{V: stop.Venue, OID: stop.ID, Held: held, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.perpetual", "persist protective close before submit: "+err.Error())
		return
	}
	go c.postHeldChild(ctx, b, req)
}

func (c *Core) resumePerpetualRiskStop(ctx context.Context, o Order) CmdAck {
	p := c.state.Venue(o.Venue).Positions[o.Symbol]
	if !c.state.PositionsReady(o.Venue) || !c.ownsPerpetualPosition(o, p) {
		return CmdAck{OrderID: o.ID, Reason: "reconcile the original MEXC position before resuming"}
	}
	if c.heldDemand == nil {
		return CmdAck{OrderID: o.ID, Reason: "execution price demand unavailable"}
	}
	if err := c.heldDemand.Acquire(ctx, o.ID, o.Symbol); err != nil {
		return CmdAck{OrderID: o.ID, Reason: err.Error()}
	}
	held := *o.Held
	held.Phase, held.PausedReason = HeldWaiting, ""
	if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: held, Ts: c.now()}, SrcLocal); err != nil {
		return CmdAck{OrderID: o.ID, Reason: err.Error()}
	}
	c.activatePerpetualRiskStop(ctx, c.order(o.ID))
	return CmdAck{Accepted: true, OrderID: o.ID}
}
