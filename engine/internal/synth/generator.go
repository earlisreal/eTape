// This file binds Tasks 1-5 (universe, price, book, ticks, bars) into one
// stateful Generator: a single seeded *rand.Rand drives every symbol's price
// walk, order book, and tick stream; StepTo advances that state to a given
// logical time and ET-midnight rollover closes out each trading day; Drain
// pulls a coalesced batch of feed.Events off the accumulated state without
// re-deriving anything. Every mutating/reading method takes Generator.mu, so
// the whole package's concurrency story funnels through this one file.
package synth

import (
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Coalescing thresholds for Drain: ticks and newly-closed bars are always
// drained immediately (no throttle - they're already batched events, not a
// per-update stream), but quotes and books get a minimum re-emit interval so
// a busy symbol doesn't spam a QuoteEvent/BookEvent on every single trade.
// barHeartbeatMs additionally throttles the *unchanged* in-progress-bar
// refresh (distinct from a newly-closed bar, which always fires immediately)
// so a live "still forming" candle keeps ticking on-screen even through a
// quiet stretch with no prints.
const (
	quoteThrottleMs = 300
	bookThrottleMs  = 150
	barHeartbeatMs  = 1000
)

// tickRingMs is how far back symRuntime.ticks retains prints; StepTo trims
// anything older on every call.
const tickRingMs = 2 * 60 * 60 * 1000

// symRuntime is the generator's complete per-symbol mutable state.
//
// Beyond the fields the task brief names explicitly, two bookkeeping fields
// are added because Drain's "ticks/bars accumulated since last drain,
// cleared afterward" semantics need a buffer that is distinct from the
// long-lived ~2h ticks ring (which RecentTicks serves off of and must
// survive across many Drain calls): pendingTicks and pendingBars. A third,
// lastBarMs, throttles the in-progress-bar heartbeat the same way
// lastBookMs/lastQuoteMs throttle their events; the brief's struct list
// covers book/quote's throttle fields but not bars', so this mirrors that
// established pattern for consistency.
type symRuntime struct {
	spec  SymbolSpec
	price *priceState
	book  *bookState
	sess  sessionAgg
	bar   barAgg

	day1m   map[int64]feed.Bar // closed 1m bars, keyed by bucket start ms
	dailies []feed.Bar         // finalized daily bars, oldest first

	ticks []feed.Tick // ring, trimmed to the trailing ~2h in StepTo

	prevClose float64
	lastSeq   int64

	dirtyBook, dirtyQuote   bool
	lastBookMs, lastQuoteMs int64

	pendingTicks []feed.Tick // since last Drain; cleared by Drain
	pendingBars  []feed.Bar  // newly-closed bars since last Drain; cleared by Drain
	lastBarMs    int64       // last time a Bars1mEvent was emitted for this symbol
}

// Generator is the single stateful simulator for the whole synthetic
// universe: one seeded *rand.Rand feeds every symbol's price walk, book, and
// tick stream. clk is only consulted once, by New, to seed lastStepMs/curDay
// from "now" - StepTo and Drain both take their logical time as an explicit
// nowMs argument from the caller (typically driven by that same clock, e.g.
// Task 7's Feed.Run loop) rather than reading g.clk themselves, so a single
// step is a pure function of (rng state, nowMs) and stays deterministic
// under clock.Fake. All access goes through mu.
type Generator struct {
	rng *rand.Rand
	clk clock.Clock
	mu  sync.Mutex

	syms  map[string]*symRuntime
	order []string // stable, sorted symbol iteration order (DrawUniverse's order)

	lastStepMs int64
	curDay     string // ET calendar day (YYYY-MM-DD) of lastStepMs
}

// New draws a fresh 12-symbol universe from seed and builds each symbol's
// runtime state at its opening print. lastStepMs/curDay are seeded from
// clk.Now() so the first StepTo call advances forward from "now", not from
// the epoch.
func New(seed int64, clk clock.Clock) *Generator {
	rng := rand.New(rand.NewSource(seed))
	specs := DrawUniverse(rng)

	nowMs := clk.Now().UnixMilli()

	g := &Generator{
		rng:        rng,
		clk:        clk,
		syms:       make(map[string]*symRuntime, len(specs)),
		order:      make([]string, 0, len(specs)),
		lastStepMs: nowMs,
		curDay:     etDay(nowMs),
	}

	for _, spec := range specs {
		ps := newPriceState(spec)
		g.syms[spec.Code] = &symRuntime{
			spec:      spec,
			price:     ps,
			book:      newBook(rng, spec, ps.Mid),
			prevClose: spec.PrevClose,
			day1m:     make(map[int64]feed.Bar),
		}
		g.order = append(g.order, spec.Code)
	}
	return g
}

// etDay returns ms's ET calendar day as a YYYY-MM-DD key, using the same
// America/New_York location (loaded once, package-level) the rest of the
// engine's session-boundary logic uses (session.Loc()), so this package's
// notion of "trading day" can't drift from everyone else's.
func etDay(ms int64) string {
	return time.UnixMilli(ms).In(session.Loc()).Format("2006-01-02")
}

// StepTo advances every symbol's price/book/tick/bar state from the
// generator's last step to nowMs, then handles ET-midnight rollover if
// nowMs's ET calendar day differs from curDay. A no-op if nowMs doesn't
// advance the clock (guards against duplicate/out-of-order calls perturbing
// rng state).
func (g *Generator) StepTo(nowMs int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	fromMs := g.lastStepMs
	if nowMs <= fromMs {
		return
	}

	// Decided once, outside the per-symbol loop: if curDay were updated
	// after the first symbol's rollover check, every subsequent symbol in
	// the same loop would see etDay(nowMs)==curDay already and silently
	// skip its own rollover.
	newDay := etDay(nowMs)
	rollover := newDay != g.curDay

	for _, code := range g.order {
		rt := g.syms[code]
		g.stepSymbol(rt, fromMs, nowMs)
		if rollover {
			g.rolloverSymbol(rt, fromMs, nowMs)
		}
	}

	g.lastStepMs = nowMs
	if rollover {
		g.curDay = newDay
	}
}

// stepSymbol advances rt's price walk over [fromMs, nowMs) and generates the
// window's ticks, folding each into rt's bar/session aggregates, the ~2h
// tick ring, and the per-drain pending buffers. A no-op beyond the price step
// if no ticks printed (halted symbol, or just an unlucky quiet window) -
// nothing else changed, so book/quote stay clean.
func (g *Generator) stepSymbol(rt *symRuntime, fromMs, nowMs int64) {
	dtMs := nowMs - fromMs
	stepPrice(g.rng, rt.spec, rt.price, nowMs, dtMs)

	ticks := genTicks(g.rng, rt.spec, rt.price, rt.book, &rt.sess, rt.spec.Code, fromMs, nowMs, rt.lastSeq+1)
	if len(ticks) == 0 {
		return
	}
	rt.lastSeq = ticks[len(ticks)-1].Seq

	for _, tk := range ticks {
		if closed := rt.bar.add(tk); closed != nil {
			rt.day1m[closed.BucketMs] = *closed
			rt.pendingBars = append(rt.pendingBars, *closed)
		}
	}

	rt.ticks = append(rt.ticks, ticks...)
	rt.ticks = trimTicksBefore(rt.ticks, nowMs-tickRingMs)
	rt.pendingTicks = append(rt.pendingTicks, ticks...)

	rt.dirtyBook = true
	rt.dirtyQuote = true
}

// trimTicksBefore drops ticks older than cutoff, compacting in place (the
// same style as price.go's detectHalt window trim) rather than reslicing, so
// no extra allocation is needed on the hot path.
func trimTicksBefore(ticks []feed.Tick, cutoff int64) []feed.Tick {
	kept := ticks[:0]
	for _, tk := range ticks {
		if tk.TsMs >= cutoff {
			kept = append(kept, tk)
		}
	}
	return kept
}

// rolloverSymbol closes out rt's trading day at the ET-midnight boundary
// crossed by [fromMs, nowMs): archives the session-to-date as a daily bar
// (if any trades printed), rolls prevClose forward, resets the session
// aggregate, redraws a fresh overnight gap for runners, and re-centers the
// book on whatever price the day is opening at. fromMs (still "yesterday" at
// the point StepTo calls this) labels the archived bar's bucket.
func (g *Generator) rolloverSymbol(rt *symRuntime, fromMs, nowMs int64) {
	switch {
	case rt.sess.hasOpen:
		rt.dailies = append(rt.dailies, feed.Bar{
			Symbol:   rt.spec.Code,
			BucketMs: session.DayMs(fromMs),
			O:        rt.sess.Open,
			H:        rt.sess.High,
			L:        rt.sess.Low,
			C:        rt.sess.Last,
			Volume:   rt.sess.Vol,
			Turnover: rt.sess.Turnover,
		})
		rt.prevClose = rt.sess.Last
	case len(rt.dailies) > 0:
		rt.prevClose = rt.dailies[len(rt.dailies)-1].C
	default:
		// No trades ever printed and no prior daily close on record (e.g. a
		// run shorter than one day in tests): fall back to the run's
		// original spec close rather than leaving prevClose stale.
		rt.prevClose = rt.spec.PrevClose
	}

	rt.sess = sessionAgg{}

	if rt.spec.Pers == PersRunner {
		g.kickRunnerGap(rt, nowMs)
	}

	// Re-center the book on the (possibly gapped) new price unconditionally:
	// without this, a runner's overnight jump would leave stale, far-off-mid
	// liquidity sitting in the ladder until enough trades organically
	// dragged it over via consume/replenish. Doing it for every personality
	// keeps the logic simple, and it also gives every symbol a "fresh
	// session" book rather than one carrying over stale wall placements from
	// the prior day.
	rt.book.rebuildAround(g.rng, rt.spec, rt.price.Mid, false)
	rt.dirtyBook = true
	rt.dirtyQuote = true
}

// kickRunnerGap redraws a fresh overnight gap for a runner symbol: the new
// day's anchor/mid move by a random +/- percentage (scaled off the same
// GapPct magnitude DrawUniverse used for the original opening gap) relative
// to the just-set prevClose, and the regime's dwell timer is forced to
// expire immediately. It then calls stepPrice with dtMs=0 purely so Task 2's
// own transition-matrix machinery (not reimplemented here) picks the day's
// opening regime — a real "kick" via the existing regime-draw mechanism,
// rather than this file hand-rolling a second one.
func (g *Generator) kickRunnerGap(rt *symRuntime, nowMs int64) {
	mag := between(g.rng, rt.spec.GapPct*0.5, rt.spec.GapPct*1.5)
	if g.rng.Float64() < 0.5 {
		mag = -mag
	}
	rt.price.Anchor = round2(rt.prevClose * (1 + mag/100))
	rt.price.Mid = rt.price.Anchor
	rt.price.HaltUntilMs = 0
	rt.price.win = rt.price.win[:0]
	rt.price.DwellLeftMs = 0
	stepPrice(g.rng, rt.spec, rt.price, nowMs, 0)
}

// Drain returns a coalesced batch of feed.Events for everything that changed
// since the last Drain call, in stable per-symbol order: any newly-closed
// bar (immediate) plus, on a ~1s heartbeat, the still-forming in-progress
// bar; all ticks accumulated since the last drain; a QuoteEvent if the quote
// changed and the 300ms throttle has elapsed; a BookEvent if the book
// changed and the 150ms throttle has elapsed. Dirty flags and the per-drain
// tick/bar buffers are cleared only for what was actually emitted, so a
// throttled update stays pending for the next call rather than being lost.
func (g *Generator) Drain(nowMs int64) []feed.Event {
	g.mu.Lock()
	defer g.mu.Unlock()

	var events []feed.Event
	for _, code := range g.order {
		rt := g.syms[code]

		var bars []feed.Bar
		bars = append(bars, rt.pendingBars...)
		heartbeatDue := nowMs-rt.lastBarMs >= barHeartbeatMs
		if inProg, ok := rt.bar.inProgress(code); ok && (len(rt.pendingBars) > 0 || heartbeatDue) {
			bars = append(bars, inProg)
		}
		if len(bars) > 0 {
			events = append(events, feed.Bars1mEvent{Bars: bars})
			rt.lastBarMs = nowMs
		}
		rt.pendingBars = nil

		if len(rt.pendingTicks) > 0 {
			events = append(events, feed.TicksEvent{Ticks: rt.pendingTicks})
			rt.pendingTicks = nil
		}

		if rt.dirtyQuote && nowMs-rt.lastQuoteMs >= quoteThrottleMs {
			events = append(events, feed.QuoteEvent{Quote: buildQuote(code, &rt.sess, rt.prevClose, nowMs)})
			rt.dirtyQuote = false
			rt.lastQuoteMs = nowMs
		}

		if rt.dirtyBook && nowMs-rt.lastBookMs >= bookThrottleMs {
			events = append(events, feed.BookEvent{Book: rt.book.snapshot(code, nowMs)})
			rt.dirtyBook = false
			rt.lastBookMs = nowMs
		}
	}
	return events
}

// Symbols returns the universe's symbol codes in stable sorted order.
func (g *Generator) Symbols() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.order))
	copy(out, g.order)
	return out
}

// Has reports whether code is a universe symbol.
func (g *Generator) Has(code string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.syms[code]
	return ok
}

// BookOf returns code's current book snapshot, timestamped at the
// generator's last step time.
func (g *Generator) BookOf(code string) (feed.Book, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rt, ok := g.syms[code]
	if !ok {
		return feed.Book{}, false
	}
	return rt.book.snapshot(code, g.lastStepMs), true
}

// QuoteOf returns code's current quote, timestamped at the generator's last
// step time.
func (g *Generator) QuoteOf(code string) (feed.Quote, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rt, ok := g.syms[code]
	if !ok {
		return feed.Quote{}, false
	}
	return buildQuote(code, &rt.sess, rt.prevClose, g.lastStepMs), true
}

// RecentTicks returns up to the last n ticks in code's ~2h ring, oldest
// first.
func (g *Generator) RecentTicks(code string, n int) []feed.Tick {
	g.mu.Lock()
	defer g.mu.Unlock()
	rt, ok := g.syms[code]
	if !ok || n <= 0 {
		return nil
	}
	if n > len(rt.ticks) {
		n = len(rt.ticks)
	}
	out := make([]feed.Tick, n)
	copy(out, rt.ticks[len(rt.ticks)-n:])
	return out
}

// CachedBars1m returns up to the last n closed 1m bars for code, oldest
// first. It never includes the current in-progress bar - that's what
// Drain's heartbeat/BarOf-style live path is for.
func (g *Generator) CachedBars1m(code string, n int) []feed.Bar {
	g.mu.Lock()
	defer g.mu.Unlock()
	rt, ok := g.syms[code]
	if !ok || n <= 0 || len(rt.day1m) == 0 {
		return nil
	}
	keys := make([]int64, 0, len(rt.day1m))
	for k := range rt.day1m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if n < len(keys) {
		keys = keys[len(keys)-n:]
	}
	out := make([]feed.Bar, len(keys))
	for i, k := range keys {
		out[i] = rt.day1m[k]
	}
	return out
}

// RankRow is one row of the synthetic movers ranking: RankRows sorts these
// by PctChange descending.
type RankRow struct {
	Code        string
	Last        float64
	PctChange   float64
	Volume      int64
	FloatShares int64
}

// RankRows returns every universe symbol's current rank row, sorted by
// PctChange (vs prevClose) descending.
func (g *Generator) RankRows() []RankRow {
	g.mu.Lock()
	defer g.mu.Unlock()

	rows := make([]RankRow, 0, len(g.order))
	for _, code := range g.order {
		rt := g.syms[code]
		last := rt.prevClose
		if rt.sess.hasOpen {
			last = rt.sess.Last
		}
		var pct float64
		if rt.prevClose != 0 {
			pct = (last - rt.prevClose) / rt.prevClose * 100
		}
		rows = append(rows, RankRow{
			Code:        code,
			Last:        last,
			PctChange:   pct,
			Volume:      rt.sess.Vol,
			FloatShares: rt.spec.FloatShares,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].PctChange > rows[j].PctChange })
	return rows
}

// Fundamentals is a synthetic symbol's static/slow-moving reference data:
// float, total shares outstanding, and 52-week high/low.
type Fundamentals struct {
	FloatShares int64
	SharesOut   int64
	High52Wk    float64
	Low52Wk     float64
}

// Fundamentals returns code's fundamentals snapshot. High52Wk/Low52Wk are
// computed strictly from archived daily bars (dailies) - today's live
// session high/low isn't folded in until the next ET-midnight rollover
// archives it, matching the "52wk hi/lo from dailies" brief wording. The
// synth universe has no separate notion of shares issued vs. float, so
// SharesOut is set equal to FloatShares (SymbolSpec only models the latter).
func (g *Generator) Fundamentals(code string) (Fundamentals, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rt, ok := g.syms[code]
	if !ok {
		return Fundamentals{}, false
	}

	var hi, lo float64
	for i, d := range rt.dailies {
		if i == 0 || d.H > hi {
			hi = d.H
		}
		if i == 0 || d.L < lo {
			lo = d.L
		}
	}

	return Fundamentals{
		FloatShares: rt.spec.FloatShares,
		SharesOut:   rt.spec.FloatShares,
		High52Wk:    hi,
		Low52Wk:     lo,
	}, true
}
