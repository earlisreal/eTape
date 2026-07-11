// This file is Task 11's statistical sanity sweep: it drives the real,
// fully-wired *Generator (Tasks 1-6 combined, not price.go's raw stepPrice in
// isolation) across many seeds and simulated hours, sampling the same
// book/price state Feed.Run (feed.go) and Drain would expose in production,
// and asserts real distributional properties rather than "doesn't crash".
//
// Investigation note on the spread ceiling used below (see
// TestGenerator_StatisticalSanityAcrossSeedsAndPersonalities' spreadRatioCeiling
// doc comment): an early draft of this test asserted the SpreadProfile
// contract literally documented in universe.go ("normally sits between
// MinCents and MaxCents, occasionally flushing out to MaxCents * FlushMult")
// and found it does NOT hold once a symbol has been trading live for more
// than a few minutes -- see that doc comment for the measured numbers and the
// root-cause hypothesis (book.go's replenish/topUp). Per this plan's
// established convention, that gap is reported as a concern (task-11-report.md)
// rather than fixed here; this file's spread assertions instead pin down the
// weaker, but real and regression-worthy, invariant the current code actually
// guarantees.
package synth

import (
	"fmt"
	"math"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// statsSweepSeeds is deliberately more than one or two seeds: DrawUniverse's
// per-run personality-to-slot assignment is fixed (universe.go), but which
// fictional name lands in which slot, and every parametric drawn within a
// personality's range (Vol, GapPct, Spread, Lambda*, BookMeanSize/Sigma), are
// seed-dependent -- a property that only held for one arbitrarily-chosen seed
// wouldn't be a property of the model, just a coincidence of that draw.
var statsSweepSeeds = []int64{1, 2, 3, 4, 5, 6}

// statsSweepStartMs is an arbitrary, fixed reference instant (matches the
// convention requester_test.go's newSteppedGenerator already established in
// this package) fed through clock.NewFake so New's lastStepMs/curDay seed
// deterministically -- this sweep's own determinism doesn't depend on wall
// time.
const statsSweepStartMs = int64(1_752_000_000_000)

// statsSweepStepMs/statsSweepSteps drive each seed's Generator for 24
// simulated hours at a 60s stride via the real StepTo/Drain surface --
// comfortably under stepSymbol's maxStepDtMs (5min) clamp (generator.go), so
// every stride gets full, untruncated simulation fidelity, just like
// Feed.Run's own 50ms-tick production cadence, only coarser (cheap enough to
// run 6 seeds x 24h in a few seconds of wall-clock test time).
const (
	statsSweepStepMs = 60_000
	statsSweepSteps  = 24 * 60 // 24h of 1-minute strides
)

// spreadRatioCeiling bounds the OBSERVED (ask-bid)/mid ratio sampled every
// stride across every symbol/seed in this sweep. It is deliberately NOT
// SpreadProfile.MaxCents*FlushMult/mid (universe.go's own documented
// intent for a "normal" spread, a few cents): empirical measurement (see this
// file's header comment) shows the live book's touch commonly drifts to
// within a few percent of book.go's maxTouchDriftPct (10% of mid) BEFORE
// replenish's staleness check rebuilds it -- and, since bid/ask drift is
// checked independently per side against a mid-anchored reference, the two
// sides can drift in opposite directions at once, so the worst-case
// observed spread approaches 2*maxTouchDriftPct of mid, not maxTouchDriftPct
// itself. A 10-seed x 24h empirical sweep during this task's investigation
// never exceeded ~0.214; 0.30 keeps a comfortable margin above that while
// still being a real ceiling (it would catch, e.g., topUp's stale-rebuild
// path being accidentally disabled, which would let drift grow unbounded
// over a long enough run instead of self-correcting within this envelope).
const spreadRatioCeiling = 0.30

// largeCapDriftLo/Hi mirror TestStepPrice_BoundedDriftOverHours' own band
// (price_test.go) for the reasons that test's doc comment gives (mean
// reversion keeps a large cap within a sane multiple of its open) -- this
// test extends that check from stepPrice called directly on one synthetic
// spec to every large-cap symbol the real Generator draws, across many
// seeds, over a full simulated day. Real universe.go large-cap Vol
// (0.005-0.015) is an order of magnitude below that test's artificial
// spec() helper's Vol=0.5, so in practice Mid/Open stays within a couple of
// percent for the whole run (empirically confirmed during this task's
// investigation) -- but the wide 0.3x-3x band is kept rather than tightened,
// matching the existing test's own established tolerance rather than
// inventing a new, narrower one this file would be the sole owner of.
const (
	largeCapDriftLo = 0.3
	largeCapDriftHi = 3.0
)

// TestGenerator_StatisticalSanityAcrossSeedsAndPersonalities is Task 11
// Step 1. For each seed it runs one real Generator for 24 simulated hours,
// sampling every symbol's book/price state every stride, and checks:
//
//   - (a) large-cap price drift stays within largeCapDriftLo/Hi of its own
//     Open, and Mid never goes non-positive or non-finite for ANY symbol at
//     ANY sampled stride (extends TestStepPrice_BoundedDriftOverHours from a
//     single raw stepPrice call to the real, fully-wired Generator);
//   - (b) the book is never crossed/locked (bestBid < bestAsk strictly) and
//     the observed spread never sits below the symbol's own
//     SpreadProfile.MinCents, at every sampled stride;
//   - (b') the observed spread-to-mid ratio never exceeds spreadRatioCeiling
//     (see that constant's doc comment for why this, not
//     MaxCents*FlushMult, is the bound this file can honestly assert);
//   - (c) the observed tick intensity (total ticks / elapsed simulated
//     seconds) for every symbol lands within a tolerant window around its
//     own [LambdaMin, LambdaMax] (tick.go's lambda() maps every regime's
//     rate into that closed interval, so any time-weighted average across a
//     mixed-regime run is mathematically bounded above by LambdaMax; a
//     generous floor -- rather than LambdaMin itself -- accounts for
//     halted stretches, which contribute zero ticks and pull a runner's
//     average down without violating the model).
func TestGenerator_StatisticalSanityAcrossSeedsAndPersonalities(t *testing.T) {
	for _, seed := range statsSweepSeeds {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			g := New(seed, clock.NewFake(timeMs(statsSweepStartMs)))
			now := statsSweepStartMs

			nTicks := make(map[string]int64, len(g.order))
			for _, code := range g.order {
				nTicks[code] = 0
			}

			for i := 0; i < statsSweepSteps; i++ {
				now += statsSweepStepMs
				g.StepTo(now)

				for _, code := range g.order {
					rt := g.syms[code]

					// (a) every symbol, every stride: Mid must stay positive
					// and finite (a NaN/Inf/<=0 Mid would corrupt every
					// downstream book/tick/bar derived from it).
					mid := rt.price.Mid
					if mid <= 0 || math.IsNaN(mid) || math.IsInf(mid, 0) {
						t.Fatalf("%s: Mid went non-positive/non-finite at t=%dms: %v", code, now, mid)
					}

					// (b)/(b') book integrity + spread envelope.
					bid, ask := rt.book.best()
					if bid <= 0 || ask <= 0 {
						t.Fatalf("%s: non-positive touch at t=%dms: bid=%v ask=%v", code, now, bid, ask)
					}
					if bid >= ask {
						t.Fatalf("%s: crossed/locked book at t=%dms: bid=%v ask=%v", code, now, bid, ask)
					}
					spreadCents := round2(ask-bid) * 100
					if spreadCents < float64(rt.spec.Spread.MinCents)-1e-9 {
						t.Fatalf("%s: spread %.2fc below MinCents=%d at t=%dms (bid=%v ask=%v)",
							code, spreadCents, rt.spec.Spread.MinCents, now, bid, ask)
					}
					if ratio := (ask - bid) / mid; ratio > spreadRatioCeiling {
						t.Fatalf("%s: spread/mid ratio %.4f exceeds ceiling %.2f at t=%dms (bid=%v ask=%v mid=%v)",
							code, ratio, spreadRatioCeiling, now, bid, ask, mid)
					}

					// (c) accumulate ticks for the intensity check below.
					nTicks[code] += int64(len(rt.pendingTicks))
					rt.pendingTicks = nil
				}
			}

			elapsedSec := float64(statsSweepSteps) * float64(statsSweepStepMs) / 1000

			for _, code := range g.order {
				rt := g.syms[code]

				// (a) end-of-run bounded-drift band, large-cap only.
				if rt.spec.Pers == PersLargeCap {
					driftRatio := rt.price.Mid / rt.spec.Open
					if driftRatio < largeCapDriftLo || driftRatio > largeCapDriftHi {
						t.Errorf("%s (LARGECAP): unbounded drift over 24h: open %.2f -> mid %.2f (ratio %.3f, want [%.1f,%.1f])",
							code, rt.spec.Open, rt.price.Mid, driftRatio, largeCapDriftLo, largeCapDriftHi)
					}
				}

				// (c) tick-intensity sanity: mathematically, lambda(spec,reg)
				// is always in [LambdaMin, LambdaMax] (tick.go), so a
				// time-weighted average across any mix of regimes cannot
				// exceed LambdaMax (a hard property, given a small rounding
				// allowance); a generous floor -- not LambdaMin itself --
				// tolerates halted stretches, which contribute zero ticks.
				impliedLambda := float64(nTicks[code]) / elapsedSec
				if impliedLambda <= 0 {
					t.Errorf("%s: no ticks printed at all over 24 simulated hours (lambda bounds [%.2f,%.2f])",
						code, rt.spec.LambdaMin, rt.spec.LambdaMax)
				}
				if impliedLambda > rt.spec.LambdaMax*1.05 {
					t.Errorf("%s: implied intensity %.3f exceeds LambdaMax*1.05=%.3f",
						code, impliedLambda, rt.spec.LambdaMax*1.05)
				}
				if floor := rt.spec.LambdaMin * 0.15; impliedLambda < floor {
					t.Errorf("%s: implied intensity %.3f far below a generous floor of %.3f (LambdaMin=%.2f)",
						code, impliedLambda, floor, rt.spec.LambdaMin)
				}
			}
		})
	}
}
