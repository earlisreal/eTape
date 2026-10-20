package exec

import "context"

// Absolute position pushes carry no execution watermark. While a linked LIMIT
// is working, a reduction may precede its fill; only a combined snapshot can
// distinguish that fill from a separate external exit.
type trailPositionCheck struct {
	before   map[string]ownedExposure
	exits    map[string]bool
	attempts int
}

const trailAttributionPending = "position attribution pending; LIMIT remains working; cleanup blocked"

type trailPositionCheckResult struct {
	venue    VenueID
	revision uint64
	snapshot BrokerSnapshot
	err      error
}

func (trailPositionCheckResult) isBrokerEvent() {}

func (c *Core) checkAbsoluteTrailAttribution(ctx context.Context, event BrokerEvent, before map[string]ownedExposure) {
	var venue VenueID
	var positions map[string]float64
	switch e := event.(type) {
	case BrokerPosition:
		venue, positions = e.Position.Venue, map[string]float64{e.Position.Symbol: e.Position.Qty}
	case BrokerPositions:
		venue, positions = e.V, make(map[string]float64)
		for symbol := range c.state.Venue(venue).Positions {
			positions[symbol] = 0
		}
		for _, p := range e.Positions {
			positions[p.Symbol] = p.Qty
		}
	default:
		return
	}
	if c.trailPositionChecks[venue] != nil {
		return
	}
	for _, o := range c.state.Venue(venue).Orders {
		qty, present := positions[o.Symbol]
		if !present || qty <= 0 || qty >= c.state.VenuePositionShares(venue, o.Symbol) || o.RiskEntryID == "" || !o.Working() || o.Held == nil || o.Held.Trail == nil || o.Held.ChildClientID == "" {
			continue
		}
		anchor := make(map[string]ownedExposure)
		for id, exposure := range before {
			if c.order(id).Venue == venue {
				anchor[id] = exposure
			}
		}
		exits := make(map[string]bool)
		for _, exit := range c.state.Venue(venue).Orders {
			if exit.RiskEntryID != "" && exit.Working() && exit.Held != nil && exit.Held.Trail != nil && exit.Held.ChildClientID != "" {
				exits[exit.ID] = true
			}
		}
		c.trailPositionChecks[venue] = &trailPositionCheck{before: anchor, exits: exits}
		c.trailAttributionReason(venue, trailAttributionPending)
		c.startTrailPositionCheck(ctx, venue)
		return
	}
}

func (c *Core) trailAttributionReason(venue VenueID, reason string) {
	for _, o := range c.state.Venue(venue).Orders {
		if o.RiskEntryID == "" || o.Held == nil || o.Held.Trail == nil || !o.Working() || o.Held.Trail.Retired {
			continue
		}
		if reason == "" && o.Held.PausedReason != trailAttributionPending {
			continue
		}
		if err := c.saveTrail(o, *o.Held.Trail, o.Held.Phase, reason); err != nil {
			c.syslog("exec.trail", err.Error())
		}
	}
}

func (c *Core) startTrailPositionCheck(ctx context.Context, venue VenueID) {
	check := c.trailPositionChecks[venue]
	if check == nil || c.brokers[venue] == nil {
		return
	}
	check.attempts++
	revision := c.protectionRevision[venue]
	broker := c.brokers[venue]
	go func() {
		queryCtx, cancel := context.WithTimeout(ctx, defaultRecoverSnapshotTimeout)
		defer cancel()
		account, positions, orders, err := broker.Snapshot(queryCtx)
		result := trailPositionCheckResult{venue: venue, revision: revision, snapshot: BrokerSnapshot{V: venue, Account: account, Positions: positions, Orders: orders}, err: err}
		select {
		case c.bevents <- result:
		case <-ctx.Done():
		}
	}()
}

func (c *Core) finishTrailPositionCheck(ctx context.Context, result trailPositionCheckResult) {
	check := c.trailPositionChecks[result.venue]
	if check == nil {
		return
	}
	if result.err != nil {
		c.syslog("exec.trail", "position attribution unresolved; reconcile venue: "+result.err.Error())
		return
	}
	if result.revision != c.protectionRevision[result.venue] {
		if check.attempts < 3 {
			c.startTrailPositionCheck(ctx, result.venue)
		}
		return
	}
	// A stale/missing local execution cannot establish ownership. Keep the
	// existing LIMIT and reservation, and require manual reconciliation.
	c.handleBrokerEvent(ctx, result.snapshot)
	c.checkTrailPositions(ctx)
}

func (c *Core) trailSnapshotCurrent(venue VenueID, snapshot BrokerSnapshot) bool {
	observed := make(map[string]Order)
	for _, o := range snapshot.Orders {
		observed[o.ID] = o
	}
	for id := range c.trailPositionChecks[venue].exits {
		remote, ok := observed[id]
		if !ok && !c.order(id).Working() {
			continue
		}
		if !ok || remote.ExecutedQty < c.order(id).ExecutedQty {
			return false
		}
	}
	for id, anchor := range c.trailPositionChecks[venue].before {
		owner := c.order(id)
		remote, ok := observed[id]
		if ok && remote.ExecutedQty < owner.ExecutedQty || !ok && owner.Working() && owner.ExecutedQty > anchor.entries {
			return false
		}
	}
	return true
}
