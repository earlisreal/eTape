// This file implements the per-symbol price/regime process: a Markov
// regime-switching random walk with mean-reversion toward a slowly-wandering
// anchor, plus a runner-only halt detector that freezes price after a sharp
// move.
package synth

import (
	"math"
	"math/rand"
)

// Regime is the current market "mood" driving a symbol's price walk. Each
// personality has its own transition matrix (transMatrix) so runners spend
// more time in RegParabolic/RegFlush while large caps mostly sit in
// RegQuiet/RegChop.
type Regime uint8

const (
	RegQuiet Regime = iota
	RegChop
	RegTrendUp
	RegTrendDown
	RegParabolic
	RegFlush
	RegHalt
)

func (r Regime) String() string {
	switch r {
	case RegQuiet:
		return "QUIET"
	case RegChop:
		return "CHOP"
	case RegTrendUp:
		return "TREND_UP"
	case RegTrendDown:
		return "TREND_DOWN"
	case RegParabolic:
		return "PARABOLIC"
	case RegFlush:
		return "FLUSH"
	case RegHalt:
		return "HALT"
	}
	return "UNKNOWN"
}

// numRegimes is the width/height of transMatrix, kept in sync with the
// Regime enum above.
const numRegimes = 7

// haltWindowMs is both the trailing window runners scan for a >10% move and
// the freeze duration once a halt engages.
const haltWindowMs = 5 * 60_000

// maxStepChange is the hard ceiling on the magnitude of a single stepPrice
// call's fractional drift+noise change to Mid, regardless of dtSec, regime,
// or Vol. It exists to bound the rare-but-real case where an extreme
// rng.NormFloat64() draw, or a large dtSec (day-scale coarse history
// seeding), would otherwise send Mid to the price floor or an implausible
// multiple of Anchor in one call. ±50% is generous relative to normal
// small-dtSec live-path steps, which almost never approach it.
const maxStepChange = 0.5

// pricePoint is one sample in a priceState's rolling halt-detection window.
type pricePoint struct {
	tsMs int64
	px   float64
}

// priceState is the mutable per-symbol price-walk state. Mid is the
// simulated last/mid price; Anchor is a slowly-wandering long-run reference
// that Mid mean-reverts toward. win holds the trailing 5-minute samples
// runners use to detect a halt-triggering move; it stays nil for
// non-runners, which never populate it.
type priceState struct {
	Mid    float64
	Anchor float64
	Reg    Regime

	DwellLeftMs int64
	HaltUntilMs int64

	win []pricePoint
}

// newPriceState seeds a fresh price walk at the symbol's opening print. The
// initial DwellLeftMs of 0 means the first stepPrice call immediately draws
// a regime and dwell, since there was no real "dwell" before the sim started.
func newPriceState(spec SymbolSpec) *priceState {
	return &priceState{
		Mid:    spec.Open,
		Anchor: spec.Open,
		Reg:    RegChop,
	}
}

// Halted reports whether the symbol is currently frozen by a runner halt.
func (ps *priceState) Halted(nowMs int64) bool {
	return nowMs < ps.HaltUntilMs
}

// stepPrice advances ps by dtMs milliseconds, ending at nowMs. While halted
// it returns immediately without moving Mid. Otherwise it: counts down the
// regime dwell timer (drawing a new regime + dwell from transMatrix when it
// expires), applies drift+noise scaled by the regime and the symbol's Vol,
// mean-reverts Mid toward the slowly-wandering Anchor, snaps Mid to the
// cent, and — for runners only — checks the trailing halt window.
func stepPrice(rng *rand.Rand, spec SymbolSpec, ps *priceState, nowMs, dtMs int64) {
	if ps.Halted(nowMs) {
		ps.Reg = RegHalt
		return
	}

	ps.DwellLeftMs -= dtMs
	if ps.DwellLeftMs <= 0 {
		ps.Reg = nextRegime(rng, spec.Pers, ps.Reg)
		ps.DwellLeftMs = dwellDuration(rng, ps.Reg)
	}

	dtSec := float64(dtMs) / 1000
	drift := driftBps(ps.Reg) * spec.Vol
	noise := rng.NormFloat64() * spec.Vol * math.Sqrt(dtSec)
	change := (drift + noise) / 100
	if change > maxStepChange {
		change = maxStepChange
	} else if change < -maxStepChange {
		change = -maxStepChange
	}
	ps.Mid *= 1 + change

	// Exponential (not linear-Euler) mean-reversion: stable for any dtSec.
	// For small dtSec, exp(-rate*dtSec) ~= 1-rate*dtSec, so this matches the
	// original linear formula in the live-path small-step regime; for large
	// dtSec (e.g. Task 9's day-scale coarse history seeding) it converges
	// Mid smoothly to Anchor instead of overshooting past it.
	decay := math.Exp(-reversion(ps.Reg) * dtSec)
	ps.Mid = ps.Anchor + (ps.Mid-ps.Anchor)*decay
	ps.Anchor *= 1 + rng.NormFloat64()*0.0002

	ps.Mid = math.Round(ps.Mid*100) / 100
	if ps.Mid < 0.01 {
		ps.Mid = 0.01
	}

	if spec.Pers == PersRunner {
		detectHalt(spec, ps, nowMs)
	}
}

// detectHalt appends the current Mid to ps's rolling 5-minute window, evicts
// points older than the window, and — if the window's high/low spread
// exceeds 10% of its low — engages a 5-minute halt: Mid freezes, Reg forces
// to RegHalt, and the window (and dwell timer, so the next regime is drawn
// fresh the moment the halt lifts) reset.
func detectHalt(spec SymbolSpec, ps *priceState, nowMs int64) {
	ps.win = append(ps.win, pricePoint{tsMs: nowMs, px: ps.Mid})

	cutoff := nowMs - haltWindowMs
	kept := ps.win[:0]
	for _, p := range ps.win {
		if p.tsMs >= cutoff {
			kept = append(kept, p)
		}
	}
	ps.win = kept
	if len(ps.win) < 2 {
		return
	}

	lo, hi := ps.win[0].px, ps.win[0].px
	for _, p := range ps.win[1:] {
		if p.px < lo {
			lo = p.px
		}
		if p.px > hi {
			hi = p.px
		}
	}
	if lo > 0 && (hi-lo)/lo > 0.10 {
		ps.HaltUntilMs = nowMs + haltWindowMs
		ps.Reg = RegHalt
		ps.DwellLeftMs = 0
		ps.win = ps.win[:0]
	}
}

// nextRegime samples the next regime from pers's transition matrix row for
// the current regime cur.
func nextRegime(rng *rand.Rand, pers Personality, cur Regime) Regime {
	row := transMatrix(pers)[cur]
	roll := rng.Float64()
	var cum float64
	for i, p := range row {
		cum += p
		if roll < cum {
			return Regime(i)
		}
	}
	return Regime(len(row) - 1) // float rounding fallback
}

// dwellDuration draws how long (ms) a newly-entered regime lasts before the
// next transition roll. RegFlush dwells are tighter — flushes are sharp and
// short; everything else spans a few seconds to a couple of minutes.
func dwellDuration(rng *rand.Rand, reg Regime) int64 {
	if reg == RegFlush {
		return int64(between(rng, 2_000, 15_000))
	}
	return int64(between(rng, 3_000, 120_000))
}

// driftBps returns the per-step directional push for a regime, multiplied by
// the symbol's Vol (and divided by 100, alongside noise) in stepPrice.
// RegQuiet/RegChop/RegHalt have no directional bias; trends push steadily;
// RegParabolic/RegFlush push harder in their direction. Magnitudes are tuned
// so that even a full max-length dwell at the highest Vol used in this
// package's tests can't blow through TestStepPrice_BoundedDriftOverHours'
// 0.3x-3x band — see price_test.go.
func driftBps(reg Regime) float64 {
	switch reg {
	case RegTrendUp:
		return 0.02
	case RegTrendDown:
		return -0.02
	case RegParabolic:
		return 0.06
	case RegFlush:
		return -0.06
	default:
		return 0
	}
}

// reversion returns the regime's mean-reversion rate (roughly, fraction of
// the Anchor gap closed per second): strong in RegQuiet, weaker in RegChop,
// nearly absent while trending, parabolic, or flushing (regimes actively
// fighting reversion), and zero while halted.
func reversion(reg Regime) float64 {
	switch reg {
	case RegQuiet:
		return 0.1
	case RegChop:
		return 0.05
	case RegTrendUp, RegTrendDown:
		return 0.01
	case RegParabolic, RegFlush:
		return 0.005
	default: // RegHalt
		return 0
	}
}

// transMatrix returns pers's regime transition matrix: row = current
// regime, column = next regime (indices follow the Regime enum order), each
// row summing to 1. Runners get elevated RegParabolic/RegFlush weights and a
// leg structure (parabolic tends to relax into chop, then back into
// parabolic or flush); large caps mostly stay RegQuiet/RegChop with rare
// trends and negligible parabolic/flush.
//
// The Halt column is always 0: RegHalt is never entered via this random
// draw, only via detectHalt's explicit >10%-move check (which also sets
// HaltUntilMs — a Reg=RegHalt reached any other way would report Halted()
// as false and never freeze Mid). The Halt row is still meaningful: it's
// what a halt resumes into once HaltUntilMs elapses.
func transMatrix(pers Personality) [numRegimes][numRegimes]float64 {
	switch pers {
	case PersRunner:
		return [numRegimes][numRegimes]float64{
			// Quiet, Chop, TrendUp, TrendDown, Parabolic, Flush, Halt
			{0.20, 0.41, 0.15, 0.15, 0.05, 0.04, 0.00}, // Quiet
			{0.10, 0.36, 0.15, 0.15, 0.13, 0.11, 0.00}, // Chop
			{0.05, 0.21, 0.40, 0.05, 0.25, 0.04, 0.00}, // TrendUp
			{0.05, 0.21, 0.05, 0.40, 0.04, 0.25, 0.00}, // TrendDown
			{0.02, 0.21, 0.15, 0.03, 0.35, 0.24, 0.00}, // Parabolic
			{0.02, 0.21, 0.03, 0.15, 0.24, 0.35, 0.00}, // Flush
			{0.05, 0.31, 0.15, 0.15, 0.20, 0.14, 0.00}, // Halt (resuming)
		}
	case PersMidCap:
		return [numRegimes][numRegimes]float64{
			{0.35, 0.41, 0.10, 0.10, 0.02, 0.02, 0.00},
			{0.30, 0.41, 0.12, 0.12, 0.02, 0.03, 0.00},
			{0.10, 0.31, 0.45, 0.05, 0.05, 0.04, 0.00},
			{0.10, 0.31, 0.05, 0.45, 0.04, 0.05, 0.00},
			{0.03, 0.26, 0.25, 0.05, 0.30, 0.11, 0.00},
			{0.03, 0.26, 0.05, 0.25, 0.11, 0.30, 0.00},
			{0.10, 0.56, 0.10, 0.10, 0.07, 0.07, 0.00},
		}
	default: // PersLargeCap
		return [numRegimes][numRegimes]float64{
			{0.50, 0.36, 0.06, 0.06, 0.01, 0.01, 0.00},
			{0.35, 0.51, 0.06, 0.06, 0.01, 0.01, 0.00},
			{0.15, 0.36, 0.40, 0.05, 0.02, 0.02, 0.00},
			{0.15, 0.36, 0.05, 0.40, 0.02, 0.02, 0.00},
			{0.05, 0.36, 0.20, 0.05, 0.25, 0.09, 0.00},
			{0.05, 0.36, 0.05, 0.20, 0.09, 0.25, 0.00},
			{0.10, 0.60, 0.10, 0.10, 0.05, 0.05, 0.00},
		}
	}
}
