package exec

import (
	"context"
	"math"
)

type ownedExposure struct {
	position, entries, exits float64
	entryFills               map[string]float64
}

func (c *Core) trailExposure() map[string]ownedExposure {
	result := make(map[string]ownedExposure)
	for _, vs := range c.state.Venues {
		for _, entry := range vs.Orders {
			if entry.RiskEntry == nil || entry.RiskEntry.SharedEntryID != "" {
				continue
			}
			// Fixed and trailing protection both retain confirmed entry ownership.
			e := ownedExposure{position: vs.Positions[entry.Symbol].Qty, entries: c.riskFilledQty(entry)}
			e.entryFills = make(map[string]float64)
			for _, member := range c.riskMembers(entry) {
				e.entryFills[member.ID] = member.ExecutedQty
			}
			for _, exit := range vs.Orders {
				if exit.RiskEntryID == entry.ID {
					e.exits += exit.ExecutedQty
				}
			}
			result[entry.ID] = e
		}
	}
	return result
}

// Compare authoritative effects after fill deduplication; a matching position
// snapshot cannot retire the same shares a second time.
func (c *Core) retireReducedOwnership(ctx context.Context, before map[string]ownedExposure) {
	for id, after := range c.trailExposure() {
		if c.trailPositionChecks[c.order(id).Venue] != nil {
			continue
		}
		prior, ok := before[id]
		if !ok {
			continue
		}
		reduction := prior.position - after.position + (after.entries - prior.entries) - (after.exits - prior.exits)
		if reduction <= 1e-9 {
			continue
		}
		entry := c.order(id)
		risk := *entry.RiskEntry
		risk.RetiredQty = math.Max(0, math.Min(after.entries-after.exits, risk.RetiredQty+reduction))
		if err := c.persistRisk(entry, risk); err != nil {
			c.syslog("exec.trail", err.Error())
			continue
		}
		if after.position <= 0 && len(c.riskMembers(entry)) > 1 {
			if !c.beginRiskExit(ctx, c.order(id)) {
				continue
			}
			stop := c.order(risk.StopID)
			if stop.Working() && stop.Held != nil && stop.Held.ChildClientID == "" {
				if err := c.appendAndFold(OrderCanceled{V: stop.Venue, OID: stop.ID, Ts: c.now()}, SrcLocal); err != nil {
					c.syslog("exec.risk", err.Error())
					continue
				}
				c.releaseHeld(stop.ID)
			}
			c.syncRiskProtection(ctx, c.order(id), true)
			continue
		}
		c.syncRiskProtection(ctx, c.order(id), false)
	}
}
