// This file implements the tick engine: a Poisson trade-arrival process that
// samples inter-arrival gaps from lambda(personality, regime), picks a
// momentum-correlated aggressor direction, executes each print against the
// live order book (Task 3), and rolls the results into a running session
// aggregate (open/high/low/last/volume/turnover) shared with the bar builder
// (Task 5).
package synth

import (
	"math"
	"math/rand"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// sessionAgg is the running session summary genTicks folds each printed tick
// into: first-tick Open, running High/Low/Last, and cumulative Vol/Turnover.
// hasOpen distinguishes "no ticks yet" from a real print at index 0, since a
// zero-value Open is otherwise indistinguishable from an actual $0 print.
type sessionAgg struct {
	Open, High, Low, Last float64
	Vol                   int64
	Turnover              float64
	hasOpen               bool
}

// update folds one printed tick into sess: the first tick sets Open/High/Low,
// every tick updates High/Low/Last and accumulates Vol/Turnover.
func (sess *sessionAgg) update(tk feed.Tick) {
	if !sess.hasOpen {
		sess.Open = tk.Price
		sess.High = tk.Price
		sess.Low = tk.Price
		sess.hasOpen = true
	}
	if tk.Price > sess.High {
		sess.High = tk.Price
	}
	if tk.Price < sess.Low {
		sess.Low = tk.Price
	}
	sess.Last = tk.Price
	sess.Vol += tk.Volume
	sess.Turnover += tk.Turnover
}

// neutralProb is the fraction of prints that cross as an inside NEUTRAL trade
// at the current midpoint rather than sweeping the lit book.
const neutralProb = 0.10

// blockProb is the fraction of prints that come in as an institutional block
// (1k-10k shares) rather than the usual retail-sized lognormal print.
const blockProb = 0.05

// retailSizeMean/retailSizeSigma parameterize drawSize's lognormal component:
// a 100-share-biased retail print size, personality-independent (book depth,
// not trade size, is what varies per symbol).
const retailSizeMean = 100.0
const retailSizeSigma = 60.0

// lambda returns the Poisson arrival intensity (prints/second) for spec's
// personality in regime reg, interpolated across the symbol's
// [LambdaMin, LambdaMax] range: calm regimes sit near LambdaMin, momentum
// regimes (trends) sit mid-range, and PARABOLIC/FLUSH bursts hit LambdaMax.
// RegHalt always returns 0 — halted symbols print nothing.
func lambda(spec SymbolSpec, reg Regime) float64 {
	if reg == RegHalt {
		return 0
	}
	span := spec.LambdaMax - spec.LambdaMin
	switch reg {
	case RegQuiet:
		return spec.LambdaMin
	case RegChop:
		return spec.LambdaMin + span*0.2
	case RegTrendUp, RegTrendDown:
		return spec.LambdaMin + span*0.5
	case RegParabolic, RegFlush:
		return spec.LambdaMax
	default:
		return spec.LambdaMin
	}
}

// drawSize samples one print's size: usually a lognormal retail-sized print
// biased around 100 shares, occasionally (blockProb) a flat 1k-10k
// institutional block instead.
func drawSize(rng *rand.Rand) int64 {
	if rng.Float64() < blockProb {
		return int64(between(rng, 1_000, 10_000))
	}
	return lognormalSize(rng, retailSizeMean, retailSizeSigma)
}

// buyProb returns the probability a non-neutral print is a buy for the given
// regime: trending/parabolic regimes skew the aggressor mix in their
// direction, everything else (including chop/quiet/halt) stays balanced.
func buyProb(reg Regime) float64 {
	switch reg {
	case RegTrendUp, RegParabolic:
		return 0.7
	case RegTrendDown, RegFlush:
		return 0.3
	default:
		return 0.5
	}
}

// pickDirection samples one print's aggressor side: neutralProb of the time
// it's an inside NEUTRAL cross, otherwise Buy/Sell weighted by the regime's
// momentum bias (buyProb).
func pickDirection(rng *rand.Rand, reg Regime) feed.Direction {
	if rng.Float64() < neutralProb {
		return feed.Neutral
	}
	if rng.Float64() < buyProb(reg) {
		return feed.Buy
	}
	return feed.Sell
}

// genTicks samples a Poisson trade-arrival process over [fromMs, toMs) at
// intensity lambda(spec, ps.Reg) — a single regime is assumed for the whole
// call, which is why this is only called over short windows — and returns
// the resulting prints oldest-first with Seq monotonically increasing from
// seqBase. ps.Halted is checked once up front: a halted symbol emits nothing
// and lambda is never even consulted (per priceState's contract, Halted —
// not Reg == RegHalt — is the only correct freeze check). Each arrival picks
// a direction, draws a size, executes against b (NEUTRAL prints cross at the
// book's current midpoint without touching either side; Buy/Sell sweep the
// book via b.consume), replenishes b back toward ps.Mid, and folds the
// resulting print into sess.
func genTicks(rng *rand.Rand, spec SymbolSpec, ps *priceState, b *bookState, sess *sessionAgg, symbol string, fromMs, toMs, seqBase int64) []feed.Tick {
	if ps.Halted(fromMs) {
		return nil
	}
	lam := lambda(spec, ps.Reg)
	if lam <= 0 {
		// Defensive only: Halted() above already suppresses the one regime
		// (RegHalt) that makes lambda 0, so this guards against a spinning
		// -log(x)/0 = +Inf gap loop if that contract is ever violated rather
		// than actually being reachable today.
		return nil
	}

	var ticks []feed.Tick
	seq := seqBase
	toMsF := float64(toMs)
	tMs := float64(fromMs)
	for {
		gapSec := -math.Log(rng.Float64()) / lam
		tMs += gapSec * 1000
		if tMs >= toMsF {
			break
		}
		tsMs := int64(tMs)

		dir := pickDirection(rng, ps.Reg)
		size := drawSize(rng)

		var execPrice float64
		var filled int64
		if dir == feed.Neutral {
			bid, ask := b.best()
			execPrice = round2((bid + ask) / 2)
			filled = size
		} else {
			execPrice, filled = b.consume(dir, size)
		}
		if filled <= 0 {
			// consume is documented to always fill a positive-qty request
			// (Task 3's never-empty-side guarantee); skip rather than emit a
			// bogus zero-volume/zero-turnover print if that ever changes.
			continue
		}

		tk := feed.Tick{
			Symbol:   symbol,
			Seq:      seq,
			TsMs:     tsMs,
			Price:    execPrice,
			Volume:   filled,
			Turnover: execPrice * float64(filled),
			Dir:      dir,
			RecvTsMs: tsMs,
		}
		seq++
		ticks = append(ticks, tk)
		sess.update(tk)

		b.replenish(rng, spec, ps.Mid)
	}
	return ticks
}
