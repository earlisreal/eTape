package exec

import (
	"context"
	"math"
)

type ownedExposure struct{ position, entries, exits float64 }

func (c *Core) trailExposure() map[string]ownedExposure {
	result := make(map[string]ownedExposure)
	for _, vs := range c.state.Venues {
		for _, entry := range vs.Orders {
			if entry.RiskEntry == nil {
				continue
			}
			stop := c.order(entry.RiskEntry.StopID)
			if stop.Held == nil || stop.Held.Trail == nil {
				continue
			}
			e := ownedExposure{position: vs.Positions[entry.Symbol].Qty, entries: entry.ExecutedQty}
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
		risk.RetiredQty = math.Max(0, math.Min(entry.ExecutedQty-after.exits, risk.RetiredQty+reduction))
		if err := c.persistRisk(entry, risk); err != nil {
			c.syslog("exec.trail", err.Error())
			continue
		}
		c.syncRiskProtection(ctx, c.order(id), false)
	}
}
