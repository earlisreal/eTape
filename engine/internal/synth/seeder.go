// This file implements the boot-time history seeder: fast-running the
// Task 1-6 model in logical time (no clk, no sleeps) to populate a fresh
// Generator with ~1y of warm history before the live Feed.Run loop starts,
// so the UI never opens on an empty chart/tape.
//
// stepPrice/genTicks vs StepTo/stepSymbol: Task 6's stepSymbol clamps its
// per-call dtMs to maxStepDtMs (5 minutes) specifically to bound the
// *live* path's price-noise/tick-volume blast radius for the rare case of a
// real-world gap (a suspended process resuming). That clamp is wrong for
// this file's job: the seeder's strides are driven at whatever granularity
// each pass below needs (a minute for the fine pass, a minute-scale
// sub-step for the coarse pass - see seedDailyHistory's doc comment for why
// "one stepPrice call per day" turned out not to be viable at all,
// regardless of the clamp), and clamping them to 5 minutes would corrupt
// that. So Seed and its helpers call the lower-level, unclamped
// stepPrice/genTicks (Tasks 2/4) directly, never StepTo/stepSymbol.
package synth

import (
	"math/rand"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// seedHistoryDays is the length of the coarse, daily-granularity backdrop
// (step 1): stepPrice sub-stepped at seedDaySubstepMs granularity, no ticks.
// seedTrailingDays is the length of the fine, minute-granularity window
// (step 2) immediately preceding "now": real stepPrice+genTicks calls at
// 1-minute strides, so the most recent history is tick-accurate rather than
// a coarse approximation. The two passes are back-to-back, non-overlapping
// windows (coarse covers everything strictly older than seedTrailingDays
// ago; fine covers the last seedTrailingDays plus however much of "today"
// has elapsed) - each calendar day gets exactly one daily bar, from exactly
// one pass, so no ArchiveDaily call is ever repeated for the same bucket.
const (
	seedHistoryDays  = 365
	seedTrailingDays = 3
)

// seedRTHSecs/seedAvgPrintSize parameterize the coarse pass's synthetic daily
// Volume/Turnover (step 1 has no genTicks pass to derive real ones from):
// a rough "average prints/sec across the session, at a rough blended
// print size" estimate. It only needs to look plausible on a volume-bar
// chart, not be a faithful backtest of the tick model.
const (
	seedRTHSecs      = 6.5 * 3600
	seedAvgPrintSize = 150.0
)

// seedDaySubstepMs is seedDailyHistory's stepPrice stride - see that
// function's doc comment for why a single whole-day call turned out not to
// be viable at all (independent of the reversion formula's shape) and why
// this specific granularity was chosen over a coarser one.
const seedDaySubstepMs = barMs

// SeedStore is the store surface Seed writes warm history through -
// satisfied by *store.Store (ArchiveDaily/ArchiveBar1m: store/bars.go;
// RecordEvent: store/journal.go; Flush: store/store.go).
type SeedStore interface {
	ArchiveDaily(feed.Bar)
	ArchiveBar1m(feed.Bar)
	RecordEvent(ev feed.Event, recvMs int64)
	Flush()
}

// Seed fast-runs every symbol's model in logical time up to nowMs and writes
// the result to st, leaving the generator itself positioned exactly at
// nowMs so the live Feed.Run loop that starts right after this call
// continues with no seam (no price/book jump, no re-simulated overlap, no
// backlog of "new" events for the first Drain to flood out).
//
// Locking: Seed takes g.mu for its entire duration, matching every other
// exported Generator method's discipline even though — being called once at
// boot, strictly before Feed.Run starts stepping the same generator — there
// is no actual concurrent access to guard against yet. Held once up front
// rather than per-symbol/per-pass because Seed is a single logical
// operation from the caller's point of view (uihub/wiring shouldn't be able
// to observe a partially-seeded generator), and because nothing here calls
// back into another exported Generator method that would double-lock.
//
// Passes (see the brief):
//  1. ~1y of daily bars per symbol, coarse day-granularity (each day itself
//     sub-stepped at minute granularity - see seedDailyHistory), ending
//     just before the fine window starts.
//  2. The trailing ~seedTrailingDays days plus however much of "today" has
//     elapsed, at real minute/tick granularity - continuing the exact same
//     price/book state pass 1 left off at - emitting closed 1m bars and
//     rolling daily bars at each day crossing.
//  3. The live ~2h tick ring pass 2 leaves behind is journaled verbatim
//     (batched per symbol) so warmStart can rebuild today's 10s series from
//     the journal alone, the same way it would after a real restart.
//  4. st.Flush().
func (g *Generator) Seed(st SeedStore, nowMs int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	loc := session.Loc()
	todayMidnight := time.UnixMilli(session.DayMs(nowMs)).In(loc)
	fineStart := todayMidnight.AddDate(0, 0, -seedTrailingDays)
	coarseStart := fineStart.AddDate(0, 0, -seedHistoryDays)

	fineStartMs := fineStart.UnixMilli()
	coarseStartMs := coarseStart.UnixMilli()

	for _, code := range g.order {
		rt := g.syms[code]

		g.seedDailyHistory(rt, coarseStartMs, fineStartMs, st)

		// The coarse pass only ever moves rt.price; rt.book has sat
		// untouched since New() (still centered on spec.Open). Re-center it
		// on wherever a year of coarse drift left price.Mid before the fine
		// pass starts trading against it - otherwise genTicks' first minute
		// would sweep a book with prices unrelated to the current mid.
		rt.book.rebuildAround(g.rng, rt.spec, rt.price.Mid, false)

		g.seedIntraday(rt, fineStartMs, nowMs, st)
		seedRecentTicksJournal(rt, nowMs, st)
	}

	g.lastStepMs = nowMs
	g.curDay = etDay(nowMs)

	st.Flush()
}

// seedDailyHistory walks rt's price model one calendar day at a time from
// fromMs to toMs (both exact ET-midnight boundaries), archiving one daily
// bar per day, tracked from a real sub-day stepPrice walk rather than a
// single whole-day call.
//
// History: the original design (Task 9) called stepPrice exactly once per
// day, with dtMs spanning the whole day (~86,400,000ms). That turned out to
// be unfixable at the call-site level of "just pick a better reversion
// formula": price.go's reversion term (whatever its shape) is a per-second
// rate calibrated for the live path's small, frequent steps, where regime
// transitions, drift, and noise accumulate stochastically over thousands of
// ticks. A single call spanning a whole day forces exactly one regime draw
// (DwellLeftMs, capped at 120s/15s-for-Flush, is always deeply negative
// after subtracting a day's dtMs) to be treated as if it had persisted,
// uninterrupted, for the entire day. Under the original linear-Euler
// reversion this overshot wildly (the bug Task 9 originally reported);
// under the corrected exponential-decay reversion (stable for any dtSec) it
// instead converges Mid smoothly and completely to Anchor within that one
// call - decay = exp(-reversion(reg)*86400) underflows to ~0 for every
// regime, so every day's Close silently became Anchor regardless of what
// drift/noise/regime happened that day, erasing runners' "occasional prior
// spike day" character entirely (a different failure mode, same root cause:
// evaluating this model at a granularity it was never calibrated for).
//
// The fix is a calling-pattern fix, not a price.go fix: sub-step each day at
// seedDaySubstepMs (1 minute, i.e. barMs) granularity, tracking Open (first
// sub-step's starting Mid)/High/Low (running max/min of Mid across every
// sub-step)/Close (last sub-step's ending Mid) for real, instead of
// leaping straight from one day's Close to the next. seedDaySubstepMs was
// chosen empirically, not from the "stays well below saturation for every
// regime" framing (that's unreachable: Quiet's reversion rate is fast
// enough - decay < 0.005 by 60s - that even a 1-minute stride already
// nearly-fully converges it, and getting Quiet's decay to a "meaningful"
// residual would need sub-10s strides, ~365x more calls for no real
// benefit: correctly, "quiet" IS supposed to hug Anchor closely). What
// actually matters for spike-day character is the *slow*-reverting
// regimes runners spend real time in (Trend: 0.01/s; Parabolic/Flush,
// 0.005/s - the regimes elevated drift/lambda already make "spikes"): at
// 60s, their decay is 0.55/0.74 respectively - a real, visible residual
// that a coarser stride (e.g. 15-60 minutes) loses almost entirely (decay
// 0.011 down to ~0 even for Parabolic/Flush by 15 minutes) - reproducing
// this same bug at a coarser scale. 1-minute substeps keep that signal
// while staying cheap (no genTicks at this granularity - see
// seedDayVolume): see task-9-report.md's "Fix round" section for the
// decay-by-stride table and the empirical spike-day verification.
func (g *Generator) seedDailyHistory(rt *symRuntime, fromMs, toMs int64, st SeedStore) {
	loc := session.Loc()
	cur := time.UnixMilli(fromMs).In(loc)
	end := time.UnixMilli(toMs)
	ps := rt.price

	for cur.Before(end) {
		next := cur.AddDate(0, 0, 1)
		dayStartMs := cur.UnixMilli()
		dayEndMs := next.UnixMilli()

		open := ps.Mid
		hi, lo := open, open
		for sub := dayStartMs; sub < dayEndMs; {
			subNext := sub + seedDaySubstepMs
			if subNext > dayEndMs {
				subNext = dayEndMs
			}
			stepPrice(g.rng, rt.spec, ps, subNext, subNext-sub)
			if ps.Mid > hi {
				hi = ps.Mid
			}
			if ps.Mid < lo {
				lo = ps.Mid
			}
			sub = subNext
		}
		closePx := ps.Mid

		vol, turn := seedDayVolume(g.rng, rt.spec, (hi+lo)/2)

		bar := feed.Bar{
			Symbol:   rt.spec.Code,
			BucketMs: dayStartMs,
			O:        round2(open),
			H:        round2(hi),
			L:        round2(lo),
			C:        round2(closePx),
			Volume:   vol,
			Turnover: turn,
		}
		rt.dailies = append(rt.dailies, bar)
		st.ArchiveDaily(bar)
		rt.prevClose = closePx

		cur = next
	}
}

// seedDayVolume synthesizes a plausible daily Volume/Turnover from spec's
// Poisson-arrival parameters (lambda(spec, reg) in tick.go) blended across a
// full RTH session, rather than actually running genTicks for the day (which
// step 1 deliberately never does - see this file's header comment). avgPx
// prices the resulting turnover.
func seedDayVolume(rng *rand.Rand, spec SymbolSpec, avgPx float64) (vol int64, turnover float64) {
	avgLambda := (spec.LambdaMin + spec.LambdaMax) / 2
	vol = int64(avgLambda * seedRTHSecs * seedAvgPrintSize * between(rng, 0.6, 1.4))
	if vol < 1 {
		vol = 1
	}
	turnover = avgPx * float64(vol)
	return vol, turnover
}

// seedIntraday continues rt's price/book state (left off wherever
// seedDailyHistory's coarse walk and the subsequent book rebuild put it)
// from fromMs to nowMs at real minute granularity: each 1-minute (or, for
// the final partial bucket, shorter) stride is an unclamped stepPrice call
// followed by a real genTicks call over that same window, folded into the
// bar/session aggregates and the ~2h tick ring exactly the way stepSymbol
// folds a live step - just without stepSymbol's dtMs clamp (irrelevant here;
// every stride is already well under it) and without touching the
// pendingTicks/pendingBars buffers (there is no Drain call to feed yet, and
// leaving them empty is what keeps the first real Drain after boot from
// flooding out this whole warm-history pass as if it just happened live).
// Every ET-midnight crossed while striding through nowMs gets a real
// rollover via the existing rolloverSymbol (session-to-date archive, gap
// kick for runners, book re-center) - this is what makes the trailing
// seedTrailingDays' daily bars tick-derived rather than the coarse walk's
// synthesized approximation. The final, "today so far" stride is
// intentionally left as an in-progress bar in rt.bar, never archived -
// exactly the live state Drain's heartbeat path expects to find.
func (g *Generator) seedIntraday(rt *symRuntime, fromMs, nowMs int64, st SeedStore) {
	curDayKey := etDay(fromMs)
	cur := fromMs

	for cur < nowMs {
		next := cur + barMs
		if next > nowMs {
			next = nowMs
		}
		dtMs := next - cur

		stepPrice(g.rng, rt.spec, rt.price, next, dtMs)

		ticks := genTicks(g.rng, rt.spec, rt.price, rt.book, &rt.sess, rt.spec.Code, cur, next, rt.lastSeq+1)
		if len(ticks) > 0 {
			rt.lastSeq = ticks[len(ticks)-1].Seq
			for _, tk := range ticks {
				if closed := rt.bar.add(tk); closed != nil {
					rt.day1m[closed.BucketMs] = *closed
					st.ArchiveBar1m(*closed)
				}
			}
			rt.ticks = append(rt.ticks, ticks...)
			rt.ticks = trimTicksBefore(rt.ticks, nowMs-tickRingMs)
		}

		if newDayKey := etDay(next); newDayKey != curDayKey {
			preLen := len(rt.dailies)
			g.rolloverSymbol(rt, cur, next)
			if len(rt.dailies) > preLen {
				st.ArchiveDaily(rt.dailies[len(rt.dailies)-1])
			}
			curDayKey = newDayKey
		}

		cur = next
	}
}

// seedRecentTicksJournal journals rt's live ~2h tick ring (already trimmed
// to exactly that window by seedIntraday, the same way stepSymbol trims it
// on the live path) as one Seed=true TicksEvent batch, so warmStart can
// rebuild today's 10s series from the journal alone after a restart. The
// slice is copied rather than handed over directly: rt.ticks keeps being
// appended to and trimmed in place by the live StepTo path the moment
// Feed.Run starts, and RecordEvent's write is async (store/journal.go's
// writer goroutine), so sharing the backing array would race the live
// path's in-place mutation against the store's JSON encode of this event.
// Symbols with nothing printed in the trailing window (a halt covering the
// whole 2h - vanishingly rare, but possible) are skipped rather than
// journaling a symbol-less empty batch.
func seedRecentTicksJournal(rt *symRuntime, nowMs int64, st SeedStore) {
	if len(rt.ticks) == 0 {
		return
	}
	ticks := make([]feed.Tick, len(rt.ticks))
	copy(ticks, rt.ticks)
	st.RecordEvent(feed.TicksEvent{Ticks: ticks, Seed: true}, nowMs)
}
