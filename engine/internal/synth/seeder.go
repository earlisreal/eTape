// This file implements the boot-time history seeder: fast-running the
// Task 1-6 model in logical time (no clk, no sleeps) to populate a fresh
// Generator with ~1y of warm history before the live Feed.Run loop starts,
// so the UI never opens on an empty chart/tape.
//
// stepPrice/genTicks vs StepTo/stepSymbol: Task 6's stepSymbol clamps its
// per-call dtMs to maxStepDtMs (5 minutes) specifically to bound the
// *live* path's price-noise/tick-volume blast radius for the rare case of a
// real-world gap (a suspended process resuming). That clamp is wrong for
// this file's job: the seeder's day/minute-granularity strides are
// intentionally large (a full day, or a full minute), and clamping them to
// 5 minutes would silently truncate a "day" of simulated history down to 5
// minutes repeated over and over. So Seed and its helpers call the
// lower-level, unclamped stepPrice/genTicks (Tasks 2/4) directly, never
// StepTo/stepSymbol, and drive them at whatever granularity each pass below
// needs.
package synth

import (
	"math"
	"math/rand"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// seedHistoryDays is the length of the coarse, daily-granularity backdrop
// (step 1): a single stepPrice call per calendar day, no ticks. seedTrailingDays
// is the length of the fine, minute-granularity window (step 2) immediately
// preceding "now": real stepPrice+genTicks calls at 1-minute strides, so the
// most recent history is tick-accurate rather than a coarse approximation.
// The two passes are back-to-back, non-overlapping windows (coarse covers
// everything strictly older than seedTrailingDays ago; fine covers the last
// seedTrailingDays plus however much of "today" has elapsed) - each
// calendar day gets exactly one daily bar, from exactly one pass, so no
// ArchiveDaily call is ever repeated for the same bucket.
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
//  1. ~1y of daily bars per symbol, coarse day-granularity, ending just
//     before the fine window starts.
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
// bar per day. Each day is exactly one unclamped stepPrice call spanning the
// whole day (intentionally not chunked into smaller steps - see this file's
// header comment) - runners' elevated Parabolic/Flush transition weights
// (price.go's transMatrix) make an occasional day's single draw land in one
// of those regimes, which is what gives runners their "occasional prior
// spike day" character with no extra special-casing needed here. Since
// there is no genTicks pass at this granularity, High/Low/Volume/Turnover
// are synthesized (seedDayRange/seedDayVolume) rather than derived from
// prints.
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
		stepPrice(g.rng, rt.spec, ps, dayEndMs, dayEndMs-dayStartMs)
		closePx := ps.Mid

		hi, lo := seedDayRange(g.rng, open, closePx, rt.spec.Vol)
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

// seedDayRange synthesizes a plausible daily High/Low around a day's
// open/close (there is no intraday walk to derive real extrema from at this
// granularity): High/Low always bracket both Open and Close by construction,
// widened by a random fraction of the day's own O-C move (or, if the day
// happened to close flat, a small fraction of vol-scaled price) so the bar
// doesn't look like a suspiciously flat wick-less line on a chart.
func seedDayRange(rng *rand.Rand, open, closePx, vol float64) (hi, lo float64) {
	hi = math.Max(open, closePx)
	lo = math.Min(open, closePx)

	spread := hi - lo
	if spread <= 0 {
		spread = hi * vol / 100
	}
	extra := spread * between(rng, 0.1, 0.6)
	hi += extra * rng.Float64()
	lo -= extra * rng.Float64()
	if lo < priceFloor {
		lo = priceFloor
	}
	return hi, lo
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
