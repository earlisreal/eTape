package exec

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
)

const protectionCheckInterval = 15 * time.Second

type protectionCheck struct {
	inFlight bool
	verified bool
	failed   bool
	revision uint64
	nextMs   int64
	warnings []ProtectionWarning
}

type protectionCheckResult struct {
	venue    VenueID
	revision uint64
	snapshot BrokerSnapshot
	err      error
}

func (protectionCheckResult) isBrokerEvent() {}

// Retire this exposure, not the entry ID: an additional late fill can reopen it.
func (c *Core) confirmFlatRiskEntries(v VenueID, symbol string) error {
	vs := c.state.Venue(v)
	for _, entry := range vs.Orders {
		if entry.RiskEntry == nil || entry.ExecutedQty <= entry.RiskEntry.FlatConfirmedQty ||
			(symbol != "" && entry.Symbol != symbol) || vs.Positions[entry.Symbol].Qty > 0 {
			continue
		}
		risk := *entry.RiskEntry
		risk.FlatConfirmedQty = entry.ExecutedQty
		if err := c.persistRisk(entry, risk); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) protectionProblems(v VenueID) map[string]string {
	vs := c.state.Venue(v)
	exited := make(map[string]float64)
	paused := make(map[string]bool)
	for _, order := range vs.Orders {
		if order.RiskEntryID != "" {
			exited[order.RiskEntryID] += order.ExecutedQty
			if order.Working() && order.Held != nil && (order.Held.Phase == HeldPaused || order.Held.Phase == HeldUnknown) {
				paused[order.RiskEntryID] = true
			}
		}
	}
	problems := make(map[string]string)
	latest := make(map[string]Order)
	for _, entry := range vs.Orders {
		risk := entry.RiskEntry
		if risk == nil || entry.ExecutedQty <= risk.FlatConfirmedQty || entry.ExecutedQty-exited[entry.ID] <= 1e-9 {
			continue
		}
		stop := vs.Orders[risk.StopID]
		reason := risk.Failure
		if strings.HasPrefix(reason, "protection interrupted;") {
			reason = "Protection interrupted"
		}
		if reason == "" {
			switch {
			case risk.ProtectionCanceled:
				reason = "Protection canceled"
			case paused[entry.ID]:
				reason = "Protection paused; review Orders"
			case stop.ID == "":
				reason = "Protection unavailable"
			case stop.Status == StatusExpired:
				reason = "DAY protection ended"
			case stop.Status == StatusCanceled || stop.Status == StatusRejected:
				reason = "Protection interrupted"
			}
		}
		prior, exists := latest[entry.Symbol]
		if reason != "" && (!exists || entry.CreatedMs > prior.CreatedMs || entry.CreatedMs == prior.CreatedMs && entry.ID > prior.ID) {
			problems[entry.Symbol], latest[entry.Symbol] = reason, entry
		}
	}
	return problems
}

func (c *Core) refreshProtectionWarnings(ctx context.Context) {
	for _, v := range c.venues {
		problems := c.protectionProblems(v)
		check := c.protectionChecks[v]
		if check == nil {
			check = &protectionCheck{}
			c.protectionChecks[v] = check
		}
		if len(problems) > 0 && !check.inFlight && c.now() >= check.nextMs {
			c.startProtectionCheck(ctx, v, check)
		}
		warnings := make([]ProtectionWarning, 0, len(problems))
		for symbol, reason := range problems {
			warning := ProtectionWarning{Symbol: symbol, Reason: reason, State: "checking"}
			if check.failed || !c.state.PositionsReady(v) {
				warning.State = "unverified"
			}
			if check.verified && check.revision == c.protectionRevision[v] && c.state.PositionsReady(v) {
				warning.Qty = c.state.Venue(v).Positions[symbol].Qty
				if warning.Qty <= 0 {
					continue
				}
				warning.State = "open"
			}
			warnings = append(warnings, warning)
		}
		sort.Slice(warnings, func(i, j int) bool { return warnings[i].Symbol < warnings[j].Symbol })
		if !slices.Equal(check.warnings, warnings) {
			check.warnings = warnings
			c.emit(ProtectionWarningsUpdate{Venue: v, Warnings: warnings})
		}
	}
}

func (c *Core) startProtectionCheck(ctx context.Context, v VenueID, check *protectionCheck) {
	check.nextMs = c.now() + protectionCheckInterval.Milliseconds()
	broker := c.brokers[v]
	if broker == nil {
		check.verified, check.failed = false, true
		return
	}
	check.inFlight = true
	revision := c.protectionRevision[v]
	go func() {
		queryCtx, cancel := context.WithTimeout(ctx, defaultRecoverSnapshotTimeout)
		defer cancel()
		account, positions, orders, err := broker.Snapshot(queryCtx)
		result := protectionCheckResult{venue: v, revision: revision, err: err,
			snapshot: BrokerSnapshot{V: v, Account: account, Positions: positions, Orders: orders, observational: true}}
		select {
		case c.bevents <- result:
		case <-ctx.Done():
		}
	}()
}

func (c *Core) finishProtectionCheck(ctx context.Context, result protectionCheckResult) {
	check := c.protectionChecks[result.venue]
	check.inFlight = false
	check.nextMs = c.now() + protectionCheckInterval.Milliseconds()
	if result.err != nil || result.revision != c.protectionRevision[result.venue] {
		check.verified, check.failed = false, result.err != nil
		return
	}
	// Reuse snapshot projection without the risk synchronization/activation path.
	c.handleBrokerEvent(ctx, result.snapshot)
	if err := c.confirmFlatRiskEntries(result.venue, ""); err != nil {
		check.verified, check.failed = false, true
		c.syslog("exec.protection", err.Error())
		return
	}
	check.verified, check.failed = true, false
	check.revision = c.protectionRevision[result.venue]
}
