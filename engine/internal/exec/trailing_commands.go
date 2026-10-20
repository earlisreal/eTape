package exec

import (
	"context"
	"time"
)

type EditTrail struct {
	Venue             VenueID
	OrderID           string
	ExpectedHeldPhase HeldPhase
	ExpectedStop      float64
	Config            TrailConfig
}

func (EditTrail) isCommand() {}

func (c *Core) configureTrail(ctx context.Context, venue VenueID, id string, observed HeldPhase, observedStop float64, config TrailConfig) CmdAck {
	o := c.order(id)
	previous := o
	blocked := func(reason string) CmdAck { return CmdAck{OrderID: id, Reason: reason} }
	if o.ID == "" || o.Venue != venue || !o.Working() || o.Held == nil {
		return blocked("held protection unavailable")
	}
	pre := func(p HeldPhase) bool { return p == HeldWaiting || p == HeldArmed }
	if !pre(o.Held.Phase) || !pre(observed) || observedStop != o.StopPrice || o.Held.CancelRequested || o.Held.ChildClientID != "" || o.Action != nil && (o.Action.Phase == ActionUnknown || o.Action.Phase == ActionRequested) {
		return blocked("protection changed or is no longer pretrigger; review current state")
	}
	if !c.state.PositionsReady(venue) || c.state.Venue(venue).FlattenPending || !c.printHealthy {
		return blocked("reliable position and market data required")
	}
	if o.Held.Trail == nil || o.Held.Trail.Triggered || o.Held.Trail.Retired || config.Mode != o.Held.Trail.Config.Mode {
		return blocked("only pretrigger edits within the current trail mode are supported")
	}
	config.Sessions = o.Held.Trail.Config.Sessions
	if err := validateTrailConfig(&config); err != nil {
		return blocked(err.Error())
	}
	p, ok := c.lastEligible[o.Symbol]
	if !ok || !c.freshEligiblePrint(p) || !config.Sessions.allows(c.clk.Now()) || !config.Sessions.allows(time.UnixMilli(p.TsMs)) || o.Held.DeadlineMs <= c.now() {
		return blocked("fresh permitted-session execution data required")
	}
	t := *o.Held.Trail
	t.Config = config
	h := *o.Held
	h.Trail = &t
	o.Held = &h
	if c.heldDemand == nil {
		return blocked("execution demand unavailable")
	}
	if err := c.acquireHeldDemand(ctx, o); err != nil {
		_ = c.acquireHeldDemand(ctx, previous)
		return blocked(err.Error())
	}
	if !c.freshEligiblePrint(p) || o.Held.DeadlineMs <= c.now() || !config.Sessions.allows(c.clk.Now()) {
		_ = c.acquireHeldDemand(ctx, previous)
		return blocked("execution evidence expired while acquiring subscriptions")
	}
	if err := c.saveTrail(o, t, o.Held.Phase, ""); err != nil {
		return blocked(err.Error())
	}
	c.advanceTrail(ctx, c.order(id), p, true)
	return CmdAck{Accepted: true, OrderID: id}
}
