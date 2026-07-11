// Package synth generates a live synthetic market-data feed for eTape's demo
// mode: a fictional trading universe, price/order-book simulation, and
// tick/bar emission, replacing the toy replay-journal demo. This file
// establishes the universe: a fixed name pool, per-symbol personality
// (runner / large-cap / mid-cap), and the immutable per-run parameters
// ("spec") that later synth stages (price, book, ticks, bars) consume.
package synth

import (
	"math/rand"
	"sort"
)

// Personality buckets a symbol's simulated behavior for the run: low-float
// runners gap and trend hard, large caps are tight and quiet, mid caps sit
// between the two.
type Personality uint8

const (
	PersRunner Personality = iota
	PersLargeCap
	PersMidCap
)

func (p Personality) String() string {
	switch p {
	case PersRunner:
		return "RUNNER"
	case PersLargeCap:
		return "LARGECAP"
	case PersMidCap:
		return "MIDCAP"
	}
	return "UNKNOWN"
}

// SpreadProfile parameterizes the simulated bid/ask spread: it normally sits
// between MinCents and MaxCents, occasionally flushing out to MaxCents *
// FlushMult on volatility spikes.
type SpreadProfile struct {
	MinCents  int
	MaxCents  int
	FlushMult float64
}

// SymbolSpec is the immutable per-run parameters for one symbol, drawn once
// by DrawUniverse and consumed by every later synth stage (price simulation,
// order book, ticks, bars). Code is US.-prefixed.
type SymbolSpec struct {
	Code        string
	Pers        Personality
	Open        float64
	PrevClose   float64
	FloatShares int64
	Spread      SpreadProfile

	BookMeanSize  float64
	BookSizeSigma float64

	LambdaMin float64
	LambdaMax float64

	Vol    float64
	GapPct float64
}

// namePool is the fictional 20-name universe DrawUniverse samples 12 names
// from each run. None of these resemble real tickers.
var namePool = []string{
	"VLCN", "MERI", "QNTM", "ZPHR", "NRVA",
	"KLTX", "OBSD", "HLYX", "DRGO", "VNTA",
	"CRUX", "FYNE", "AXOM", "PLRS", "TSVI",
	"OMBR", "GLDN", "WYRE", "ECLP", "THRA",
}

// between returns a uniform random float in [lo, hi).
func between(rng *rand.Rand, lo, hi float64) float64 {
	return lo + rng.Float64()*(hi-lo)
}

// DrawUniverse draws 12 distinct names from namePool, assigns 2 runner / 5
// large-cap / 5 mid-cap personalities, and fills every parametric from the
// personality's ranges via rng. The returned specs are sorted by Code so
// downstream iteration is deterministic regardless of draw order; the
// personality assignment (which sorted slot gets which personality) is fixed,
// so it's the identity of the names landing in each slot — not the slot
// order — that varies across seeds.
func DrawUniverse(rng *rand.Rand) []SymbolSpec {
	names := make([]string, len(namePool))
	copy(names, namePool)
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	names = names[:12]
	sort.Strings(names)

	specs := make([]SymbolSpec, 12)
	for i, name := range names {
		var pers Personality
		switch {
		case i < 2:
			pers = PersRunner
		case i < 7:
			pers = PersLargeCap
		default:
			pers = PersMidCap
		}
		specs[i] = drawSpec(rng, "US."+name, pers)
	}
	return specs
}

// drawSpec fills a SymbolSpec's parametrics from the personality's ranges.
func drawSpec(rng *rand.Rand, code string, pers Personality) SymbolSpec {
	spec := SymbolSpec{Code: code, Pers: pers}

	switch pers {
	case PersRunner:
		spec.Open = between(rng, 2, 15)
		spec.FloatShares = int64(between(rng, 5_000_000, 20_000_000))
		spec.Spread = SpreadProfile{MinCents: 1, MaxCents: 5, FlushMult: 4.0}
		spec.BookMeanSize = between(rng, 100, 1500)
		spec.BookSizeSigma = spec.BookMeanSize * 0.5
		spec.LambdaMin = 0.5
		spec.LambdaMax = 30
		spec.Vol = between(rng, 0.06, 0.12)
		spec.GapPct = between(rng, 40, 80)
		spec.PrevClose = spec.Open / (1 + spec.GapPct/100)

	case PersLargeCap:
		spec.Open = between(rng, 80, 500)
		spec.FloatShares = int64(between(rng, 200_000_000, 2_000_000_000))
		spec.Spread = SpreadProfile{MinCents: 1, MaxCents: 2, FlushMult: 1.5}
		spec.BookMeanSize = between(rng, 500, 5000)
		spec.BookSizeSigma = spec.BookMeanSize * 0.3
		spec.LambdaMin = 1
		spec.LambdaMax = 5
		spec.Vol = between(rng, 0.005, 0.015)
		spec.GapPct = between(rng, -1, 1)
		spec.PrevClose = spec.Open / (1 + spec.GapPct/100)

	case PersMidCap:
		spec.Open = between(rng, 15, 80)
		spec.FloatShares = int64(between(rng, 20_000_000, 200_000_000))
		spec.Spread = SpreadProfile{MinCents: 1, MaxCents: 3, FlushMult: 2.0}
		spec.BookMeanSize = between(rng, 300, 2500)
		spec.BookSizeSigma = spec.BookMeanSize * 0.4
		spec.LambdaMin = 0.5
		spec.LambdaMax = 3
		spec.Vol = between(rng, 0.02, 0.04)
		gap := between(rng, 2, 6)
		if rng.Float64() < 0.5 {
			gap = -gap
		}
		spec.GapPct = gap
		spec.PrevClose = spec.Open / (1 + spec.GapPct/100)
	}

	return spec
}
