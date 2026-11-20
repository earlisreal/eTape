package exec

import (
	"context"
	"fmt"

	"github.com/earlisreal/eTape/engine/internal/instrument"
)

func editablePerpetualHeld(o Order) bool {
	return o.Held != nil && (o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed) &&
		o.Held.ChildClientID == "" && !o.Held.CancelRequested && !o.Held.PerpetualTriggered
}

func (c *Core) replacePerpetualRisk(ctx context.Context, cm ReplaceOrder, o Order) CmdAck {
	ack := CmdAck{OrderID: o.ID}
	if !o.Working() || o.StopPrice != cm.ExpectedStopPrice ||
		o.Action != nil && (o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		ack.Reason = "order changed during edit; review the current prices"
		return ack
	}
	b, rules, err := c.perpetualRoute(o.Venue, o.Symbol)
	if err != nil {
		ack.Reason = err.Error()
		return ack
	}
	if !rules.ValidPrice(cm.StopPrice) {
		ack.Reason = "use a price on the exchange tick grid"
		return ack
	}
	if o.Type == TypeStopLimit && o.Perpetual.IsRiskEntry() {
		if !editablePerpetualHeld(o) || (cm.ExpectedHeldPhase != HeldWaiting && cm.ExpectedHeldPhase != HeldArmed) ||
			o.Perpetual.ProtectionPrice != cm.ExpectedProtectionPrice || o.Held.DeadlineMs <= c.now() {
			ack.Reason = "entry or protection changed during edit; review the current order"
			return ack
		}
		return c.replacePerpetualEntry(ctx, cm, o, rules)
	}
	if cm.PendingProtection || o.Type != TypeStop || cm.Qty != 0 || !reducesPosition(c.state, OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side}) {
		ack.Reason = "only Chart Risk Entry triggers and protective stops can be moved"
		return ack
	}
	if !c.state.PositionsReady(o.Venue) || c.state.Venue(o.Venue).FlattenPending {
		ack.Reason = "reconcile the position before editing its stop"
		return ack
	}
	if o.Held == nil && !b.Capabilities().ReconciledPerpetual {
		err = b.ReplaceOrder(ctx, o.ID, ReplaceRequest{StopPrice: cm.StopPrice, ExpectedStopPrice: cm.ExpectedStopPrice})
	} else if o.isPerpetualRiskStop() && editablePerpetualHeld(o) &&
		(cm.ExpectedHeldPhase == HeldWaiting || cm.ExpectedHeldPhase == HeldArmed) &&
		c.ownsPerpetualPosition(o, c.state.Venue(o.Venue).Positions[o.Symbol]) {
		if fair, fresh := b.FairPrice(o.Symbol); fresh && StopLimitTriggered(o.Side, fair, cm.StopPrice) {
			if ok, reason := c.checkSellLeaves(OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side}, o.LeavesQty, o.ID); !ok {
				ack.Reason = fmt.Sprintf("stop stays at %g: %s", o.StopPrice, reason)
				return ack
			}
		}
		err = c.appendAndFold(OrderReplaced{V: o.Venue, OID: o.ID, NewStop: cm.StopPrice, Ts: c.now()}, SrcLocal)
		if err == nil {
			c.activatePerpetualRiskStop(ctx, c.order(o.ID))
		}
	} else {
		ack.Reason = "protective stop is unavailable or has already triggered"
		return ack
	}
	if err != nil {
		ack.Reason = err.Error()
		return ack
	}
	ack.Accepted = true
	return ack
}

func (c *Core) replacePerpetualEntry(ctx context.Context, cm ReplaceOrder, o Order, rules instrument.ContractRules) CmdAck {
	blocked := func(reason string) CmdAck { return CmdAck{Reason: reason, OrderID: o.ID} }
	if !c.perpetualRiskAccountFresh(o.Venue) || !rules.ValidQuantity(cm.Qty) {
		return blocked("fresh account data and a reviewed contract quantity required")
	}
	options := *o.Perpetual
	trigger, limit := o.StopPrice, o.LimitPrice
	long := o.Side == SideBuy
	if cm.PendingProtection {
		options.ProtectionPrice = cm.StopPrice
	} else {
		trigger = cm.StopPrice
		var ok bool
		if options.EntryCushion == nil {
			limit, ok = rules.MoveEntryLimit(trigger, o.StopPrice, o.LimitPrice)
		} else {
			limit, ok = rules.EntryLimit(trigger, options.EntryCushion.Value, options.EntryCushion.Unit == "%", long)
		}
		if !ok {
			return blocked("entry cushion does not produce a valid limit")
		}
	}
	if long && options.ProtectionPrice >= trigger || !long && options.ProtectionPrice <= trigger {
		return blocked("place the stop on the loss side of the entry trigger")
	}
	cash := c.state.Venue(o.Venue).Account.AvailableCash
	qty := min(cm.Qty, float64(rules.SizeRisk(options.RiskBudget, cash, limit, options.ProtectionPrice, options.Leverage)))
	if options.AutoLeverage {
		terms := rules
		terms.MinLeverage = options.Leverage
		sized, _ := terms.AutoRiskSize(options.RiskBudget, cash, limit, options.ProtectionPrice, options.Leverage, int64(cm.Qty), long)
		qty = float64(sized)
	}
	if !rules.ValidQuantity(qty) {
		return blocked("risk, margin or liquidation buffer cannot support the minimum contract size")
	}
	req := OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side, Type: TypeStopLimit, TIF: o.TIF, Session: o.Session,
		Qty: qty, StopPrice: trigger, LimitPrice: limit, ClientOrderID: o.ID, Perpetual: &options}
	if err := c.validatePerpetual(&req, o.ID); err != nil {
		return blocked(err.Error())
	}
	if ok, reason := c.heldGate(req, o.ID); !ok {
		return blocked(reason)
	}
	o.Qty, o.LeavesQty, o.LimitPrice, o.StopPrice, o.Perpetual, o.UpdatedMs = qty, qty, limit, trigger, &options, c.now()
	if err := c.appendAndFold(RiskEntryChanged{Entry: o}, SrcLocal); err != nil {
		return blocked(err.Error())
	}
	c.checkPerpetualEntryTrigger(ctx, c.order(o.ID))
	return CmdAck{Accepted: true, OrderID: o.ID}
}
