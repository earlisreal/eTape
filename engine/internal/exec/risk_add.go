package exec

import (
	"context"
	"math"
)

func (c *Core) riskMembers(owner Order) []Order {
	entries := []Order{owner}
	for _, o := range c.state.Venue(owner.Venue).Orders {
		if o.RiskEntry != nil && o.RiskEntry.SharedEntryID == owner.ID {
			entries = append(entries, o)
		}
	}
	return entries
}

func (c *Core) riskFilledQty(owner Order) float64 {
	qty := 0.0
	for _, entry := range c.riskMembers(owner) {
		qty += entry.ExecutedQty
	}
	return qty
}

// Only fully covered, untriggered local protection can be shared with an add.
func (c *Core) riskAddProtection(venue VenueID, symbol string) (Order, Order, string) {
	vs := c.state.Venue(venue)
	qty := vs.Positions[symbol].Qty
	if qty == 0 {
		return Order{}, Order{}, ""
	}
	if qty < 0 {
		return Order{}, Order{}, "risk entry requires a flat symbol or a protected long position"
	}
	if c.trailPositionChecks[venue] != nil {
		return Order{}, Order{}, "position attribution pending; reconcile first"
	}
	for _, stop := range vs.Orders {
		if stop.Symbol != symbol || stop.Side != SideSell || !stop.Working() || stop.RiskEntryID == "" {
			continue
		}
		owner := c.riskOwner(stop)
		if owner.RiskEntry == nil || owner.RiskEntry.StopID != stop.ID {
			continue
		}
		risk, held := owner.RiskEntry, stop.Held
		if risk.Closing || risk.ProtectionCanceled || risk.Failure != "" || held == nil ||
			(held.Phase != HeldWaiting && held.Phase != HeldArmed) || held.ChildClientID != "" ||
			held.CancelRequested || held.DeadlineMs <= c.now() || stop.Action != nil && (stop.Action.Phase == ActionRequested || stop.Action.Phase == ActionUnknown) ||
			held.Trail != nil && (held.Trail.Retired || held.Trail.Triggered) {
			return Order{}, Order{}, "add requires healthy untriggered shared protection"
		}
		if held.Trail != nil && held.Trail.Config.Pricing != "CUSHION" {
			return Order{}, Order{}, "add requires stop-relative Limit Cushion protection"
		}
		if math.Abs(stop.LeavesQty-qty) > 1e-9 || math.Abs(c.riskFilledQty(owner)-risk.RetiredQty-qty) > 1e-9 {
			return Order{}, Order{}, "add requires full confirmed shared protection"
		}
		return owner, stop, ""
	}
	return Order{}, Order{}, "risk entry requires a flat symbol or a healthy Chart Risk Entry stop"
}

func (c *Core) cancelRiskBuys(ctx context.Context, owner Order, reason string) {
	for _, entry := range c.riskMembers(owner) {
		if !entry.Working() || entry.Held == nil {
			continue
		}
		if entry.Held.ChildClientID == "" {
			_ = c.handleCancel(ctx, CancelOrder{Venue: entry.Venue, OrderID: entry.ID})
		} else {
			c.requestHeldVenueCancel(ctx, c.order(entry.ID), reason)
		}
	}
}

func (c *Core) applyRiskAddStops(owner, stop Order) (Order, bool) {
	for _, add := range c.riskMembers(owner) {
		if add.RiskEntry.SharedEntryID == "" || add.ExecutedQty == 0 || add.RiskEntry.StopApplied {
			continue
		}
		price := math.Max(stop.StopPrice, add.RiskEntry.AddStopPrice)
		cushion := owner.RiskEntry.SellCushion
		if stop.Held.Trail != nil {
			cushion = stop.Held.Trail.Config.Cushion
		}
		limit, err := riskLimit(price, cushion, false)
		if err != nil {
			c.syslog("exec.risk", err.Error())
			return stop, false
		}
		stop.StopPrice, stop.LimitPrice = price, limit
		h := *stop.Held
		if h.Trail != nil {
			trail := *h.Trail
			trail.Floor, trail.Stop, trail.Limit = math.Max(trail.Floor, price), price, limit
			h.Trail = &trail
		}
		stop.Held = &h
		risk := *add.RiskEntry
		risk.StopApplied = true
		add.RiskEntry, add.UpdatedMs = &risk, c.now()
		// The first-fill price, coverage and applied marker recover together.
		if err := c.appendAndFold(RiskEntryChanged{Entry: add, Stop: stop}, SrcLocal); err != nil {
			c.syslog("exec.risk", err.Error())
			return stop, false
		}
	}
	return stop, true
}

func (c *Core) replaceRiskAdd(ctx context.Context, cm ReplaceOrder, entry, stop Order) CmdAck {
	blocked := func(reason string) CmdAck { return CmdAck{Reason: reason, OrderID: entry.ID} }
	owner := c.riskOwner(entry)
	if owner.RiskEntry == nil || owner.RiskEntry.Closing || owner.RiskEntry.ProtectionCanceled || owner.RiskEntry.Failure != "" {
		return blocked("shared protection unavailable")
	}
	if !validLimitIfTouchedPrice(cm.StopPrice) || cm.StopPrice <= math.Max(stop.StopPrice, entry.RiskEntry.AddStopPrice) {
		return blocked("add buy trigger must be above the shared stop")
	}
	if !finitePositive(cm.Qty) {
		return blocked("positive reviewed share quantity required before buy activation")
	}
	account := c.state.Venue(entry.Venue).Account
	if !riskAccountFresh(c.state, entry.Venue) || account.TsMs <= 0 || c.now()-account.TsMs > 30_000 {
		return blocked("fresh account data required")
	}
	limit, err := riskLimit(cm.StopPrice, entry.RiskEntry.BuyCushion, true)
	if err != nil {
		return blocked(err.Error())
	}
	sell, err := riskLimit(math.Max(stop.StopPrice, entry.RiskEntry.AddStopPrice), entry.RiskEntry.SellCushion, false)
	if err != nil {
		return blocked(err.Error())
	}
	funds := account.BuyingPower
	if entry.RiskEntry.Mode == "CashPct" {
		funds = account.AvailableCash
	}
	qty := math.Min(entry.Qty, math.Min(math.Floor(cm.Qty), riskShares(entry.RiskEntry.Budget, limit, sell, funds)))
	if qty <= 0 {
		return blocked("risk or funding budget rounds to zero shares")
	}
	req := OrderRequest{Venue: entry.Venue, Symbol: entry.Symbol, Side: SideBuy, Type: TypeStopLimit, TIF: TIFDay, Session: SessionExtended, Qty: qty, StopPrice: cm.StopPrice, LimitPrice: limit, ClientOrderID: entry.ID}
	if good, why := c.heldGate(req, entry.ID); !good {
		return blocked(why)
	}
	entry.StopPrice, entry.LimitPrice, entry.Qty, entry.LeavesQty, entry.UpdatedMs = cm.StopPrice, limit, qty, qty, c.now()
	if err := c.appendAndFold(RiskEntryChanged{Entry: entry}, SrcLocal); err != nil {
		return blocked("event append failed: " + err.Error())
	}
	if last, ok := c.lastEligible[entry.Symbol]; ok && c.freshEligiblePrint(last) && StopLimitTriggered(SideBuy, last.Price, entry.StopPrice) {
		c.activateHeld(ctx, entry)
	}
	return CmdAck{Accepted: true, OrderID: entry.ID}
}
