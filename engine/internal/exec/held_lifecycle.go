package exec

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

func (c *Core) shutdownHeldOrders() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary := HeldShutdownSummary{}
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Held == nil {
			continue
		}
		if o.Held.Trail != nil {
			if o.Held.ChildClientID == "" {
				c.retireTrail(ctx, o, "engine terminated")
				continue
			}
			t := *o.Held.Trail
			t.Retired = true
			if err := c.saveTrail(o, t, o.Held.Phase, "engine terminated"); err != nil {
				c.syslog("exec.trail", err.Error())
				summary.Unconfirmed++
				continue
			}
			o = c.order(o.ID)
		}
		switch o.Held.Phase {
		case HeldWaiting, HeldArmed:
			c.changeHeld(o, HeldPaused, "engine shutdown; resume manually", false)
			summary.Paused++
		case HeldActivating:
			c.requestHeldVenueCancel(ctx, o, "engine shutdown; child outcome requires reconciliation")
			summary.CancelRequested++
			summary.Unconfirmed++
		case HeldCancelRequested:
			if !o.Held.CancelSent {
				summary.CancelRequested++
				summary.Unconfirmed++
				continue
			}
			fallthrough
		case HeldWorking:
			action := OrderAction{Kind: ActionCancel, Phase: ActionRequested, PreviousLimitPrice: o.LimitPrice, PreviousStopPrice: o.StopPrice}
			if err := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: id, Action: action, Ts: c.now()}, SrcLocal); err != nil {
				c.syslog("exec.shutdown", "persist child cancel request "+id+": "+err.Error())
				summary.Unconfirmed++
				continue
			}
			h := *o.Held
			h.Phase, h.CancelRequested, h.CancelSent = HeldCancelRequested, true, true
			h.PausedReason = "engine shutdown; child cancel outcome requires reconciliation"
			if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: id, Held: h, Ts: c.now()}, SrcLocal); err != nil {
				c.syslog("exec.shutdown", "persist child cancel intent "+id+": "+err.Error())
				summary.Unconfirmed++
				continue
			}
			summary.CancelRequested++
			summary.Unconfirmed++
			b := c.brokers[o.Venue]
			if b == nil {
				continue
			}
			if err := b.CancelOrder(ctx, id); err != nil {
				action.Phase, action.Reason = ActionUnknown, "shutdown cancel unconfirmed: "+err.Error()
				if foldErr := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: id, Action: action, Ts: c.now()}, SrcReconcile); foldErr != nil {
					c.syslog("exec.shutdown", "persist unknown cancel outcome "+id+": "+foldErr.Error())
				}
				h = *c.order(id).Held
				h.PausedReason = "shutdown cancel unconfirmed; reconcile on restart"
				if foldErr := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: id, Held: h, Ts: c.now()}, SrcReconcile); foldErr != nil {
					c.syslog("exec.shutdown", "persist unresolved child "+id+": "+foldErr.Error())
				}
			}
		}
	}
	c.shutdownHeld = summary
	c.syslog("exec.shutdown", fmt.Sprintf("held orders: paused=%d child-cancel-requested=%d unconfirmed=%d", summary.Paused, summary.CancelRequested, summary.Unconfirmed))
}

const eligiblePrintMaxAge = 2 * time.Second

func (c *Core) freshEligiblePrint(p EligiblePrint) bool {
	age := c.clk.Now().Sub(time.UnixMilli(p.RecvTsMs))
	return p.RecvTsMs > 0 && age >= 0 && age <= eligiblePrintMaxAge
}

func (c *Core) handleEligiblePrint(ctx context.Context, p EligiblePrint) {
	if p.Gap {
		clear(c.trailBooks)
		clear(c.trailCandles)
		gapAt := p.RecvTsMs
		if gapAt <= 0 {
			gapAt = c.now()
		}
		if gapAt > c.lastPrintGapMs {
			c.lastPrintGapMs = gapAt
		}
		c.printHealthy = false
		clear(c.lastEligible)
		clear(c.lastPrintSeq)
		clear(c.lastPrintDay)
		for _, id := range c.heldOrderIDs() {
			o := c.order(id)
			if o.Held != nil && (o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed) {
				c.changeHeld(o, HeldPaused, "eligible-print gap; resume manually", false)
			}
		}
		return
	}
	if p.Symbol == "" || !finitePositive(p.Price) || p.Seq <= 0 || p.RecvTsMs <= 0 {
		return
	}
	if p.RecvTsMs <= c.lastPrintGapMs {
		return
	}
	day := session.DayMs(p.TsMs)
	if day != c.lastPrintDay[p.Symbol] {
		c.lastPrintDay[p.Symbol], c.lastPrintSeq[p.Symbol] = day, 0
	}
	if p.Seq <= c.lastPrintSeq[p.Symbol] {
		return
	}
	c.lastPrintSeq[p.Symbol] = p.Seq
	c.lastEligible[p.Symbol] = p
	if !c.freshEligiblePrint(p) {
		return
	}
	c.printHealthy = true
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Symbol != p.Symbol || o.Held == nil || (o.Held.Phase != HeldWaiting && o.Held.Phase != HeldArmed) ||
			p.RecvTsMs <= o.CreatedMs || p.RecvTsMs <= o.Held.ResumeAfterMs {
			continue
		}
		if o.Held.DeadlineMs <= c.now() {
			c.expireHeld(o)
			continue
		}
		if o.Held.Trail != nil {
			c.advanceTrail(ctx, o, p, false)
		} else if HeldOrderTriggered(o.Type, o.Side, p.Price, o.StopPrice) {
			c.activateHeld(ctx, o)
		} else {
			c.changeHeld(o, HeldArmed, "", false)
		}
	}
}

func (c *Core) heldOrderIDs() []string {
	ids := make([]string, 0)
	for _, vs := range c.state.Venues {
		for id, o := range vs.Orders {
			if o.Held != nil && o.Working() {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func (c *Core) order(id string) Order {
	v, ok := c.state.OrderVenue(id)
	if !ok {
		return Order{}
	}
	return c.state.Venue(v).Orders[id]
}

func (c *Core) changeHeld(o Order, phase HeldPhase, reason string, cancelRequested bool) {
	if o.Held == nil {
		return
	}
	h := *o.Held
	h.Phase, h.PausedReason, h.CancelRequested = phase, reason, cancelRequested
	if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: h, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.held", "persist held phase "+o.ID+": "+err.Error())
	}
}

func (c *Core) heldGate(req OrderRequest, orderID string) (bool, string) {
	copyState := *c.state
	gate := c.gate
	if o := c.order(orderID); (o.RiskEntryID != "" || o.Held != nil && o.Held.Trail != nil) && (req.Side == SideSell || req.Side == SideCover) && reducesPosition(c.state, req) {
		copyState.MasterArmed = true
		gate.Global.MaxDayLoss = 0
	}
	copyVenue := *c.state.Venue(req.Venue)
	copyVenue.Orders = make(map[string]Order, len(c.state.Venue(req.Venue).Orders))
	for id, o := range c.state.Venue(req.Venue).Orders {
		if id != orderID {
			copyVenue.Orders[id] = o
		}
	}
	copyState.Venues = make(map[VenueID]*VenueState, len(c.state.Venues))
	for v, vs := range c.state.Venues {
		copyState.Venues[v] = vs
	}
	copyState.Venues[req.Venue] = &copyVenue
	copyState.orderIndex = make(map[string]VenueID, len(c.state.orderIndex))
	for id, v := range c.state.orderIndex {
		if id != orderID {
			copyState.orderIndex[id] = v
		}
	}
	if req.Qty == 0 {
		return EvaluateDeferredAdmission(&copyState, gate, req)
	}
	return Evaluate(&copyState, gate, req, c.marks)
}

func (c *Core) activateHeld(ctx context.Context, o Order) {
	if o.Held == nil || !o.Working() || o.Held.Phase == HeldActivating || o.Held.Phase == HeldWorking || o.Held.CancelRequested || o.Held.Trail != nil && (o.Held.Trail.Retired || !o.Held.Trail.Config.Sessions.allows(c.clk.Now())) {
		return
	}
	if o.Held.DeadlineMs <= c.now() {
		if o.Held.Trail != nil {
			c.retireTrail(ctx, o, "session deadline reached")
		} else {
			c.expireHeld(o)
		}
		return
	}
	if o.Held.Trail != nil && o.Held.Trail.PositionCycle != "" && o.Held.Trail.PositionCycle != c.trailPositionCycle(o.Venue, o.Symbol) {
		c.retireTrail(ctx, o, "position cycle changed before submit")
		return
	}
	if o.Held.Trail != nil {
		if identity := c.heldLiveIdentity[o.Venue]; identity != "" && c.heldLiveAck[o.Venue] != identity {
			c.rejectTrail(ctx, o, "account acknowledgement changed")
			return
		}
	}
	if o.RiskEntry != nil {
		defer func() {
			if !c.order(o.ID).Working() {
				c.syncRiskProtection(ctx, c.order(o.ID), false)
			}
		}()
	}
	if o.RiskEntryID != "" {
		entry := c.riskOwner(o)
		if entry.RiskEntry == nil || entry.RiskEntry.ProtectionCanceled || entry.RiskEntry.Failure != "" || o.Qty <= 0 {
			return
		}
		defer func() {
			current := c.order(o.ID)
			if current.Status == StatusRejected {
				c.failRisk(ctx, c.riskOwner(current), "protection trigger rejected: "+current.RejectReason)
			}
		}()
		if !c.beginRiskExit(ctx, entry) {
			return
		}
	}
	if (o.Side == SideSell || o.Side == SideCover) && !c.state.PositionsReady(o.Venue) {
		c.changeHeld(o, HeldPaused, "position data unavailable; reconcile and resume manually", false)
		return
	}
	if (o.Side == SideSell || o.Side == SideCover) && c.state.Venue(o.Venue).FlattenPending {
		c.changeHeld(o, HeldPaused, "venue flatten is awaiting authoritative reconciliation; resume manually", false)
		return
	}
	child := OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side, Type: TypeLimit, TIF: o.TIF, Session: o.Session,
		Qty: o.LeavesQty, LimitPrice: o.LimitPrice, ClientOrderID: o.ID}
	if o.DeferredPositionPct > 0 && (o.Held.Trail == nil || o.Held.ResolvedQty == 0) {
		if !c.state.PositionsReady(o.Venue) {
			c.changeHeld(o, HeldPaused, "position data unavailable; reconcile and resume manually", false)
			return
		}
		long := c.state.VenuePositionShares(o.Venue, o.Symbol)
		if o.Held.Trail != nil && o.Side == SideCover {
			long = -long
		}
		if long <= 0 {
			reason := "no open position to size stop-sell"
			if long < 0 {
				reason = "percentage stop-sell requires a long position"
			}
			if err := c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Reason: reason, Ts: c.now()}, SrcLocal); err != nil {
				c.syslog("exec.held", "persist flat position rejection "+o.ID+": "+err.Error())
			}
			c.releaseHeld(o.ID)
			return
		}
		child.Qty = math.Floor(long * o.DeferredPositionPct / 100)
		if child.Qty <= 0 {
			if err := c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Reason: "position % rounds to 0 shares", Ts: c.now()}, SrcLocal); err != nil {
				c.syslog("exec.held", "persist rounded-zero rejection "+o.ID+": "+err.Error())
			}
			c.releaseHeld(o.ID)
			return
		}
	}
	if c.requiresReductionGuard(child) {
		if ok, reason := c.checkSellLeaves(child, child.Qty, o.ID); !ok {
			if err := c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Reason: "trigger gate: " + reason, Ts: c.now()}, SrcLocal); err != nil {
				c.syslog("exec.held", "persist sell availability rejection "+o.ID+": "+err.Error())
			}
			c.releaseHeld(o.ID)
			return
		}
	}
	if ok, reason := c.heldGate(child, o.ID); !ok {
		if err := c.appendAndFold(OrderRejected{V: o.Venue, OID: o.ID, Reason: "trigger gate: " + reason, Ts: c.now()}, SrcLocal); err != nil {
			c.syslog("exec.held", "persist trigger gate rejection "+o.ID+": "+err.Error())
		}
		c.releaseHeld(o.ID)
		return
	}
	h := *o.Held
	h.Phase, h.PausedReason, h.CancelRequested = HeldActivating, "", false
	if o.DeferredPositionPct > 0 {
		h.ResolvedQty = child.Qty
	}
	h.ChildClientID = o.ID
	if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: h, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.held", "persist activation intent "+o.ID+": "+err.Error())
		return
	}
	go c.postHeldChild(ctx, c.brokers[o.Venue], child)
}

func (c *Core) postHeldChild(ctx context.Context, b Broker, req OrderRequest) {
	if b == nil {
		return
	}
	if _, err := b.SubmitOrder(ctx, req); err != nil {
		select {
		case c.bevents <- HeldActivationOutcome{V: req.Venue, OID: req.ClientOrderID, Reason: err.Error()}:
		case <-ctx.Done():
		}
	}
}

func (c *Core) expireHeldOrders(ctx context.Context) {
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Held == nil || o.Held.DeadlineMs > c.now() {
			continue
		}
		if o.Held.Trail != nil {
			c.retireTrail(ctx, o, "session deadline reached")
			continue
		}
		if o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed || o.Held.Phase == HeldPaused {
			c.expireHeld(o)
			continue
		}
		if (o.Held.Phase == HeldActivating || o.Held.Phase == HeldWorking) && !o.Held.CancelRequested {
			c.requestHeldVenueCancel(ctx, o, "session deadline reached; cancel pending")
		}
	}
}

func (c *Core) expireHeld(o Order) {
	if err := c.appendAndFold(OrderExpired{V: o.Venue, OID: o.ID, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.held", "persist expiry "+o.ID+": "+err.Error())
		return
	}
	c.releaseHeld(o.ID)
}

func (c *Core) releaseHeld(orderID string) {
	if c.heldDemand != nil {
		c.heldDemand.Release(orderID)
	}
}

func (c *Core) requestHeldVenueCancel(ctx context.Context, o Order, reason string) {
	if o.Held == nil || o.Action != nil && o.Action.Kind == ActionCancel &&
		(o.Action.Phase == ActionRequested || o.Action.Phase == ActionUnknown) {
		return
	}
	action := OrderAction{Kind: ActionCancel, Phase: ActionRequested, PreviousLimitPrice: o.LimitPrice, PreviousStopPrice: o.StopPrice}
	if err := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: o.ID, Action: action, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.held", "persist cancel request "+o.ID+": "+err.Error())
		return
	}
	h := *o.Held
	h.Phase, h.PausedReason, h.CancelRequested = HeldCancelRequested, reason, true
	if o.Held.Phase != HeldActivating {
		h.CancelSent = true
	}
	if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: h, Ts: c.now()}, SrcLocal); err != nil {
		c.syslog("exec.held", "persist cancel intent "+o.ID+": "+err.Error())
	}
	if o.Held.Phase == HeldActivating {
		return // the accepted-child event dispatches the cancel after the submit race settles.
	}
	if b := c.brokers[o.Venue]; b != nil {
		c.startVenueAction(ctx, b, o.Venue, o.ID, ActionCancel, ReplaceRequest{})
	} else {
		action.Phase, action.Reason = ActionUnknown, "venue unavailable"
		if err := c.appendAndFold(OrderActionChanged{V: o.Venue, OID: o.ID, Action: action, Ts: c.now()}, SrcLocal); err != nil {
			c.syslog("exec.held", "persist unknown cancel outcome "+o.ID+": "+err.Error())
		}
	}
}

func (c *Core) pauseHeldVenue(venue VenueID, reason string) {
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Venue == venue && o.Held != nil && (o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed) {
			c.changeHeld(o, HeldPaused, reason, false)
		}
	}
}

func (c *Core) pauseHeldReducingVenue(venue VenueID, reason string) {
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if o.Venue == venue && c.requiresReductionGuard(OrderRequest{Venue: o.Venue, Symbol: o.Symbol, Side: o.Side}) && o.Held != nil &&
			(o.Held.Phase == HeldWaiting || o.Held.Phase == HeldArmed) {
			c.changeHeld(o, HeldPaused, reason, false)
		}
	}
}

func (c *Core) disarmHeld(ctx context.Context, venue VenueID, cancelPretrigger bool) {
	for _, id := range c.heldOrderIDs() {
		o := c.order(id)
		if !cancelPretrigger && (o.RiskEntryID != "" || o.RiskEntry != nil || o.Held != nil && o.Held.Trail != nil) {
			continue
		}
		if venue != "" && o.Venue != venue {
			continue
		}
		if o.Held == nil {
			continue
		}
		switch o.Held.Phase {
		case HeldWaiting, HeldArmed, HeldPaused, HeldWaitingQuote:
			if cancelPretrigger {
				if o.Held.Trail != nil {
					c.retireTrail(ctx, o, "protection canceled")
					continue
				}
				if err := c.appendAndFold(OrderCanceled{V: o.Venue, OID: o.ID, Ts: c.now()}, SrcLocal); err == nil {
					c.releaseHeld(o.ID)
				}
			} else {
				c.changeHeld(o, HeldPaused, "master disarmed; resume manually", false)
			}
		case HeldActivating:
			c.requestHeldVenueCancel(ctx, o, "disarm cancel pending")
		}
	}
}

func (c *Core) handleResumeHeld(ctx context.Context, cm ResumeHeldOrder) CmdAck {
	o := c.order(cm.OrderID)
	if o.ID == "" || o.Venue != cm.Venue || o.Held == nil || !o.Working() {
		return CmdAck{Accepted: false, Reason: "unknown held order", OrderID: cm.OrderID}
	}
	if o.Held.Trail != nil && (o.Held.Trail.Retired || !o.Working()) {
		return CmdAck{Reason: "trail permanently retired", OrderID: o.ID}
	}
	if o.Held.Phase != HeldPaused {
		return CmdAck{Accepted: false, Reason: "held order is not paused", OrderID: o.ID}
	}
	if !c.state.MasterArmed && o.RiskEntryID == "" && o.Held.Trail == nil {
		return CmdAck{Accepted: false, Reason: "master disarmed", OrderID: o.ID}
	}
	if (o.Side == SideSell || o.Side == SideCover) && c.state.Venue(o.Venue).FlattenPending {
		return CmdAck{Accepted: false, Reason: "venue flatten is awaiting authoritative reconciliation", OrderID: o.ID}
	}
	if (o.Side == SideSell || o.Side == SideCover) && !c.state.PositionsReady(o.Venue) {
		return CmdAck{Accepted: false, Reason: "position data unavailable; reconcile the venue before resuming", OrderID: o.ID}
	}
	if !c.printHealthy {
		return CmdAck{Accepted: false, Reason: "waiting for a new eligible real-time print", OrderID: o.ID}
	}
	if o.Held.DeadlineMs <= c.now() {
		c.expireHeld(o)
		return CmdAck{Accepted: false, Reason: "held order session expired", OrderID: o.ID}
	}
	if c.heldDemand == nil {
		return CmdAck{Accepted: false, Reason: "execution ticker demand unavailable", OrderID: o.ID}
	}
	if err := c.acquireHeldDemand(ctx, o); err != nil {
		return CmdAck{Accepted: false, Reason: "execution ticker unavailable: " + err.Error(), OrderID: o.ID}
	}
	h := *o.Held
	h.Phase, h.PausedReason, h.CancelRequested, h.ResumeAfterMs = HeldWaiting, "", false, c.now()
	if err := c.appendAndFold(HeldOrderChanged{V: o.Venue, OID: o.ID, Held: h, Ts: c.now()}, SrcLocal); err != nil {
		c.heldDemand.Release(o.ID)
		return CmdAck{Accepted: false, Reason: "event append failed: " + err.Error(), OrderID: o.ID}
	}
	if o.Held.Trail != nil {
		if o.Held.Trail.Triggered {
			if o.Held.Trail.Config.Pricing == "QUOTE" {
				c.changeHeld(c.order(o.ID), HeldWaitingQuote, "waiting for execution quote", false)
				c.activateTrailQuote(ctx, c.order(o.ID))
			} else {
				c.activateHeld(ctx, c.order(o.ID))
			}
		} else if p, ok := c.lastEligible[o.Symbol]; ok && c.freshEligiblePrint(p) {
			c.advanceTrail(ctx, c.order(o.ID), p, true)
		}
	}
	return CmdAck{Accepted: true, OrderID: o.ID}
}
