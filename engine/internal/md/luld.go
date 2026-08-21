package md

import (
	"math"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

const (
	luldCoverageWindow = 5 * time.Minute
	luldWarmup         = 5 * time.Minute
	luldRefCadence     = 30 * time.Second
	luldRefThreshold   = 0.01
)

type LULDState string

const (
	LULDUnavailable LULDState = "unavailable"
	LULDWarming     LULDState = "warming"
	LULDEstimated   LULDState = "estimated"
	LULDFrozen      LULDState = "frozen"
)

const (
	LULDReasonOutsideRTH      = "outside_rth"
	LULDReasonTierUnknown     = "tier_unknown"
	LULDReasonRegistryExpired = "registry_expired"
	LULDReasonPreviousClose   = "previous_close_unavailable"
	LULDReasonWarming         = "warming"
	LULDReasonProviderStatus  = "provider_status"
	LULDReasonTransport       = "transport_interrupted"
)

// EstimatedLULD is a display-only local approximation. It is deliberately a
// separate md value so it cannot be mistaken for an order or risk signal.
type EstimatedLULD struct {
	Lower        float64
	Upper        float64
	Reference    float64
	Tier         string
	State        LULDState
	Reason       string
	RegistryAsOf string
}

type EstimatedLULDUpdate struct {
	Symbol string
	Value  EstimatedLULD
}

type luldPrint struct {
	at    time.Time
	price float64
}

type luldCalculator struct {
	symbol   string
	registry luldRegistry

	prints      []luldPrint
	sessionDate time.Time
	epochStart  time.Time
	hasPrint    bool

	prevClose float64

	effectiveRef float64
	effectiveAt  time.Time
	lastBand     *EstimatedLULD

	providerStatus  feed.ProviderStatus
	providerFrozen  bool
	transportFrozen bool
}

func newLULDCalculator(symbol string, registry luldRegistry) *luldCalculator {
	return &luldCalculator{symbol: symbol, registry: registry}
}

func (c *luldCalculator) onQuote(q feed.Quote, now time.Time) {
	c.resetSession(now)
	c.prevClose = q.PrevClose

	switch {
	case q.ProviderSuspended || q.ProviderStatus == feed.ProviderStatusNonnormal:
		c.providerFrozen = true
		if q.ProviderStatus == feed.ProviderStatusNonnormal {
			c.providerStatus = q.ProviderStatus
		}
	case q.ProviderStatus == feed.ProviderStatusNormal:
		if c.providerFrozen || c.providerStatus != feed.ProviderStatusNormal {
			c.providerFrozen = false
			c.resetCoverage()
		}
		c.providerStatus = q.ProviderStatus
	}
	if session.PhaseAt(now) == session.RTH && !c.providerFrozen && !c.transportFrozen && c.epochStart.IsZero() {
		c.epochStart = now
	}
}

func (c *luldCalculator) onPrint(t feed.Tick, now time.Time) {
	if t.Symbol != c.symbol || !t.LastEligible || !finitePositive(t.Price) || c.providerFrozen || c.transportFrozen {
		return
	}
	sampleAt := time.UnixMilli(t.TsMs)
	if session.PhaseAt(sampleAt) != session.RTH {
		return
	}
	c.resetSession(now)
	if c.epochStart.IsZero() {
		c.epochStart = now
	}
	c.prints = append(c.prints, luldPrint{at: sampleAt, price: t.Price})
	c.hasPrint = true
	c.prune(now)
	c.recomputeReference(now)
}

func (c *luldCalculator) onTransport(down bool, now time.Time) {
	c.resetSession(now)
	if c.transportFrozen == down {
		return
	}
	c.transportFrozen = down
	c.resetCoverage()
}

func (c *luldCalculator) advance(now time.Time) EstimatedLULD {
	c.resetSession(now)
	c.prune(now)
	c.recomputeReference(now)

	entry, known := c.registry.symbols[c.symbol]
	_, active := c.registry.lookup(c.symbol, now)
	if !active {
		reason := LULDReasonTierUnknown
		if known {
			reason = LULDReasonRegistryExpired
		}
		return c.unavailable(reason, entry)
	}
	base := EstimatedLULD{Tier: string(entry.Tier), RegistryAsOf: c.registry.asOf.Format("2006-01-02")}
	if session.PhaseAt(now) != session.RTH {
		base.State = LULDUnavailable
		base.Reason = LULDReasonOutsideRTH
		return base
	}
	if !finitePositive(c.prevClose) {
		base.State = LULDUnavailable
		base.Reason = LULDReasonPreviousClose
		return base
	}
	if c.providerFrozen || c.transportFrozen {
		if c.lastBand != nil {
			base = *c.lastBand
		}
		base.State = LULDFrozen
		if c.providerFrozen {
			base.Reason = LULDReasonProviderStatus
		} else {
			base.Reason = LULDReasonTransport
		}
		return base
	}
	if c.epochStart.IsZero() || !c.hasPrint || now.Before(c.epochStart.Add(luldWarmup)) || c.effectiveRef <= 0 {
		base.State = LULDWarming
		base.Reason = LULDReasonWarming
		return base
	}
	band := c.band(now, entry)
	band.State = LULDEstimated
	band.Reason = ""
	band.Tier = string(entry.Tier)
	band.RegistryAsOf = c.registry.asOf.Format("2006-01-02")
	c.lastBand = &band
	return band
}

func (c *luldCalculator) unavailable(reason string, entry luldRegistryEntry) EstimatedLULD {
	band := EstimatedLULD{State: LULDUnavailable, Reason: reason, Tier: "UNKNOWN"}
	if entry.Tier != "" {
		band.Tier = string(entry.Tier)
		band.RegistryAsOf = c.registry.asOf.Format("2006-01-02")
	}
	return band
}

func (c *luldCalculator) resetSession(now time.Time) {
	sched := session.Schedule(now)
	date := sched.Date
	if c.sessionDate.IsZero() {
		c.sessionDate = date
		return
	}
	if c.sessionDate.Year() == date.Year() && c.sessionDate.YearDay() == date.YearDay() {
		return
	}
	c.sessionDate = date
	c.resetCoverage()
	c.lastBand = nil
}

func (c *luldCalculator) resetCoverage() {
	c.prints = nil
	c.epochStart = time.Time{}
	c.hasPrint = false
	c.effectiveRef = 0
	c.effectiveAt = time.Time{}
}

func (c *luldCalculator) prune(now time.Time) {
	cutoff := now.Add(-luldCoverageWindow)
	kept := c.prints[:0]
	for _, p := range c.prints {
		if !p.at.Before(cutoff) {
			kept = append(kept, p)
		}
	}
	c.prints = kept
}

func (c *luldCalculator) recomputeReference(now time.Time) {
	if c.epochStart.IsZero() || !c.hasPrint || now.Before(c.epochStart.Add(luldWarmup)) {
		return
	}
	var sum float64
	for _, p := range c.prints {
		sum += p.price
	}
	if len(c.prints) == 0 {
		return // quiet input retains the last effective reference.
	}
	candidate := sum / float64(len(c.prints))
	if c.effectiveRef == 0 {
		c.effectiveRef = candidate
		c.effectiveAt = now
		return
	}
	if now.Sub(c.effectiveAt) < luldRefCadence || math.Abs(candidate-c.effectiveRef)/c.effectiveRef < luldRefThreshold {
		return
	}
	c.effectiveRef = candidate
	c.effectiveAt = now
}

func (c *luldCalculator) band(now time.Time, entry luldRegistryEntry) EstimatedLULD {
	width := luldWidth(c.effectiveRef, c.prevClose, entry.Tier, now)
	width *= entry.Multiplier
	return EstimatedLULD{
		Lower:     roundLULDCents(c.effectiveRef - width),
		Upper:     roundLULDCents(c.effectiveRef + width),
		Reference: c.effectiveRef,
	}
}

func luldWidth(reference, prevClose float64, tier luldTier, now time.Time) float64 {
	var width float64
	switch {
	case prevClose > 3:
		if tier == luldTier1 {
			width = reference * 0.05
		} else {
			width = reference * 0.10
		}
	case prevClose >= 0.75:
		width = reference * 0.20
	default:
		width = math.Min(0.15, reference*0.75)
	}
	sched := session.Schedule(now)
	if tier == luldTier1 || prevClose <= 3 {
		if sched.TradingDay && !now.Before(sched.Close.Add(-25*time.Minute)) {
			width *= 2
		}
	}
	return width
}

func roundLULDCents(v float64) float64 { return math.Round(v*100) / 100 }

func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
