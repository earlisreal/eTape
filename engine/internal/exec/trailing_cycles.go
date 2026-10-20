package exec

type trailCycleKey struct {
	venue  VenueID
	symbol string
}
type trailCycle struct {
	id   string
	long bool
}

// A cycle changes on every observed flat/reversal or loss of authoritative
// continuity. Reconciliation cannot revive the previous identity.
func (c *Core) trailPositionCycle(venue VenueID, symbol string) string {
	if c.trailCycles == nil {
		c.trailCycles = make(map[trailCycleKey]trailCycle)
	}
	key := trailCycleKey{venue, symbol}
	qty := c.state.VenuePositionShares(venue, symbol)
	if !c.state.PositionsReady(venue) || qty == 0 {
		delete(c.trailCycles, key)
		return ""
	}
	cycle, ok := c.trailCycles[key]
	if !ok || cycle.long != (qty > 0) {
		cycle = trailCycle{id: c.idgen.Next(), long: qty > 0}
		c.trailCycles[key] = cycle
	}
	return cycle.id
}
func (c *Core) syncTrailPositionCycles() {
	for key := range c.trailCycles {
		c.trailPositionCycle(key.venue, key.symbol)
	}
}
