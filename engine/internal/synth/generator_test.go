package synth

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

// timeMs converts an epoch-ms timestamp to a time.Time, for seeding
// clock.Fake in tests.
func timeMs(ms int64) time.Time { return time.UnixMilli(ms) }

// eventsEqual compares two feed.Event values by concrete type and field
// values. feed.Event has no cross-type "==" guarantee (several event
// payloads embed slices), so this is a manual type switch rather than a
// direct comparison.
func eventsEqual(a, b feed.Event) bool {
	switch av := a.(type) {
	case feed.TicksEvent:
		bv, ok := b.(feed.TicksEvent)
		if !ok || av.Seed != bv.Seed || len(av.Ticks) != len(bv.Ticks) {
			return false
		}
		for i := range av.Ticks {
			if av.Ticks[i] != bv.Ticks[i] {
				return false
			}
		}
		return true
	case feed.QuoteEvent:
		bv, ok := b.(feed.QuoteEvent)
		return ok && av == bv
	case feed.BookEvent:
		bv, ok := b.(feed.BookEvent)
		if !ok || av.Seed != bv.Seed || av.Book.Symbol != bv.Book.Symbol || av.Book.TsMs != bv.Book.TsMs {
			return false
		}
		return bookLevelsEqual(av.Book.Bids, bv.Book.Bids) && bookLevelsEqual(av.Book.Asks, bv.Book.Asks)
	case feed.Bars1mEvent:
		bv, ok := b.(feed.Bars1mEvent)
		if !ok || av.Seed != bv.Seed || len(av.Bars) != len(bv.Bars) {
			return false
		}
		for i := range av.Bars {
			if av.Bars[i] != bv.Bars[i] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func bookLevelsEqual(a, b []feed.BookLevel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGenerator_Deterministic_ByteIdentical(t *testing.T) {
	run := func() []feed.Event {
		start := int64(1_700_000_000_000)
		fk := clock.NewFake(timeMs(start))
		g := New(123, fk)
		var out []feed.Event
		for i := 0; i < 300; i++ {
			fk.Advance(200 * time.Millisecond) // Advance takes a Duration; 200 alone = 200ns
			now := start + int64(i+1)*200
			g.StepTo(now)
			out = append(out, g.Drain(now)...)
		}
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("event count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !eventsEqual(a[i], b[i]) { // helper: compare by concrete type + fields
			t.Fatalf("event %d differs", i)
		}
	}
}

func TestGenerator_UniverseOnly(t *testing.T) {
	g := New(1, clock.NewFake(timeMs(0)))
	if g.Has("US.NOTREAL") {
		t.Error("generator claims a non-universe symbol")
	}
	if len(g.Symbols()) != 12 {
		t.Fatalf("want 12 symbols, got %d", len(g.Symbols()))
	}
}

// TestGenerator_DrainDoesNotDoubleCountTicks runs many StepTo/Drain cycles
// and checks every (symbol, seq) pair is emitted at most once - i.e. the
// per-drain pending-tick buffer is never re-drained and StepTo never
// re-generates a window it already covered.
func TestGenerator_DrainDoesNotDoubleCountTicks(t *testing.T) {
	start := int64(1_700_000_000_000)
	g := New(42, clock.NewFake(timeMs(start)))
	seen := map[string]map[int64]bool{}
	now := start
	total := 0
	for i := 0; i < 2000; i++ {
		now += 200
		g.StepTo(now)
		for _, ev := range g.Drain(now) {
			te, ok := ev.(feed.TicksEvent)
			if !ok {
				continue
			}
			for _, tk := range te.Ticks {
				if seen[tk.Symbol] == nil {
					seen[tk.Symbol] = map[int64]bool{}
				}
				if seen[tk.Symbol][tk.Seq] {
					t.Fatalf("duplicate seq %d for %s", tk.Seq, tk.Symbol)
				}
				seen[tk.Symbol][tk.Seq] = true
				total++
			}
		}
	}
	if total == 0 {
		t.Fatal("no ticks emitted across 2000 steps - test isn't exercising anything")
	}
}

// TestGenerator_BigJumpDoesNotCorruptState calls StepTo with nowMs several
// days ahead of the last step in one call (e.g. the process was asleep, or
// the demo sat idle) and checks every symbol still reports a sane quote and
// a well-formed, uncrossed book afterward, and that normal stepping resumes
// cleanly.
func TestGenerator_BigJumpDoesNotCorruptState(t *testing.T) {
	start := int64(1_700_000_000_000)
	g := New(7, clock.NewFake(timeMs(start)))
	g.StepTo(start + 200)
	g.Drain(start + 200)

	preMid := map[string]float64{}
	for _, code := range g.Symbols() {
		preMid[code] = g.syms[code].price.Mid
	}

	// 30h: comfortably crosses at least one ET-midnight boundary (proving
	// the multi-day-gap collapse-to-one-rollover path, not just "no time
	// passed") without the Poisson tick volume of a multi-day jump making
	// this test slow. Verified this scenario does cross exactly one
	// rollover (2023-11-14 -> 2023-11-15) and leaves every symbol's session
	// freshly reset (hasOpen=false) right after, which is exactly the state
	// that used to make QuoteOf/Drain report an all-zero quote.
	future := start + 30*3600*1000 + 12345
	g.StepTo(future)
	g.Drain(future)

	for _, code := range g.Symbols() {
		q, ok := g.QuoteOf(code)
		if !ok {
			t.Fatalf("missing quote for %s", code)
		}
		if q.Last <= 0 || q.PrevClose <= 0 {
			t.Fatalf("%s: bad quote after big jump: %+v", code, q)
		}
		if q.Last != q.PrevClose {
			// This scenario crosses a rollover and generates no further
			// trades afterward, so QuoteOf must be reporting the
			// hasOpen=false prevClose fallback exactly, not a stale/zeroed
			// session value.
			t.Errorf("%s: Last=%v want == PrevClose=%v immediately after the jump's rollover", code, q.Last, q.PrevClose)
		}
		b, ok := g.BookOf(code)
		if !ok || len(b.Bids) == 0 || len(b.Asks) == 0 {
			t.Fatalf("%s: empty book after big jump", code)
		}
		if b.Bids[0].Price >= b.Asks[0].Price {
			t.Fatalf("%s: crossed book after big jump: bid=%v ask=%v", code, b.Bids[0].Price, b.Asks[0].Price)
		}

		// Loose integration-level sanity band: legitimate behavior here is
		// at most a runner's overnight-gap kick (up to ~120%, i.e. ~2.2x)
		// plus a small clamped-dtMs noise contribution on top - nowhere
		// close to this band. The unclamped bug (fixed in this same round)
		// produced single-step ratios from ~0.0002x to ~950x for this exact
		// scenario's seed, which this band catches with huge margin.
		post := g.syms[code].price.Mid
		if post > preMid[code]*10 || post < preMid[code]/10 {
			t.Errorf("%s: Mid moved from %.4f to %.4f (%.2fx) across the big-jump step, want within [1/10x, 10x]", code, preMid[code], post, post/preMid[code])
		}
	}

	// Confirm normal stepping resumes cleanly (nothing left the generator
	// wedged, e.g. a negative dtMs or a never-clearing dirty flag).
	now := future
	for i := 0; i < 50; i++ {
		now += 200
		g.StepTo(now)
		g.Drain(now)
	}
}

// TestGenerator_StepSymbolClampsLargeDtMs directly unit-tests stepSymbol's
// dtMs clamp (bypassing StepTo/rollover entirely, so the assertion isn't
// confounded by kickRunnerGap's intentionally larger overnight-gap moves):
// a single call spanning a real 30h gap must not feed stepPrice/genTicks an
// unclamped dtMs. Before the fix, this exact scenario (seed=3) produced
// single-step Mid ratios from ~0.0002x to ~950x; the clamp bounds every
// symbol to a small, realistic multiple of its pre-call price.
func TestGenerator_StepSymbolClampsLargeDtMs(t *testing.T) {
	g := New(3, clock.NewFake(timeMs(1_700_000_000_000)))
	for _, code := range g.Symbols() {
		rt := g.syms[code]
		pre := rt.price.Mid

		g.stepSymbol(rt, 0, 30*3600*1000) // 30h in one call, no intermediate steps

		post := rt.price.Mid
		if post <= 0 {
			t.Fatalf("%s: non-positive Mid after one large-dtMs step: %v", code, post)
		}
		if post > pre*2 || post < pre/2 {
			t.Errorf("%s: Mid moved from %.4f to %.4f (%.4fx) in a single 30h-dtMs step, want within [0.5x, 2x] - dtMs clamp not bounding the price-noise blast radius", code, pre, post, post/pre)
		}
	}
}

// TestGenerator_QuoteFallsBackToPrevCloseRightAfterRollover steps the clock
// across a real ET-midnight boundary in small (200ms) increments - the
// normal live-loop cadence, not a big jump - and checks that the instant a
// rollover fires, QuoteOf reports Last == PrevClose (not the zeroed sess the
// bug used to leak) for every symbol, since no trade has printed in the new
// session yet.
func TestGenerator_QuoteFallsBackToPrevCloseRightAfterRollover(t *testing.T) {
	start := time.Date(2023, 11, 13, 23, 59, 0, 0, time.FixedZone("EST", -5*3600)).UnixMilli()
	g := New(11, clock.NewFake(timeMs(start)))

	now := start
	rolled := false
	for i := 0; i < 400 && !rolled; i++ { // 80s, enough to cross ET midnight
		now += 200
		g.StepTo(now)
		g.Drain(now)
		rolled = g.curDay != etDay(start)
	}
	if !rolled {
		t.Fatal("expected a rollover to occur")
	}

	for _, code := range g.Symbols() {
		if g.syms[code].sess.hasOpen {
			// Vanishingly unlikely at seed=11 (a trade would have to print
			// in the same sub-200ms instant the rollover fires), but if it
			// ever does happen this isn't the state the bug affects.
			continue
		}
		q, ok := g.QuoteOf(code)
		if !ok {
			t.Fatalf("%s: missing quote", code)
		}
		if q.Last == 0 {
			t.Errorf("%s: QuoteOf.Last == 0 immediately after rollover, want prevClose fallback", code)
		}
		if q.Last != q.PrevClose {
			t.Errorf("%s: QuoteOf.Last = %v, want == PrevClose %v immediately after rollover (no trade yet)", code, q.Last, q.PrevClose)
		}
	}
}

// TestGenerator_MultiSeedInvariants re-runs the step loop across several
// seeds (beyond the brief's fixed seed=123) and checks book/rank/fundamentals
// invariants hold for all of them, not just the one seed the byte-identical
// test happens to use.
func TestGenerator_MultiSeedInvariants(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 999, 123456} {
		start := int64(1_700_000_000_000)
		g := New(seed, clock.NewFake(timeMs(start)))
		now := start
		for i := 0; i < 500; i++ {
			now += 250
			g.StepTo(now)
			g.Drain(now)
		}

		rows := g.RankRows()
		if len(rows) != 12 {
			t.Fatalf("seed %d: want 12 rank rows, got %d", seed, len(rows))
		}
		for i := 1; i < len(rows); i++ {
			if rows[i-1].PctChange < rows[i].PctChange {
				t.Fatalf("seed %d: rank rows not sorted desc: %+v vs %+v", seed, rows[i-1], rows[i])
			}
		}
		for _, code := range g.Symbols() {
			b, ok := g.BookOf(code)
			if !ok {
				t.Fatalf("seed %d: %s missing book", seed, code)
			}
			if len(b.Bids) == 0 || len(b.Asks) == 0 || b.Bids[0].Price >= b.Asks[0].Price {
				t.Fatalf("seed %d: %s bad book: %+v", seed, code, b)
			}
			f, ok := g.Fundamentals(code)
			if !ok || f.FloatShares <= 0 {
				t.Fatalf("seed %d: %s bad fundamentals: %+v", seed, code, f)
			}
		}
	}
}

// TestGenerator_RolloverArchivesDailyBar steps the clock across an ET
// calendar-day boundary and checks the rollover actually fires exactly once
// per crossing: curDay flips and every symbol gains exactly one daily bar.
func TestGenerator_RolloverArchivesDailyBar(t *testing.T) {
	// 2023-11-13 23:59:00 EST, 80s before ET midnight.
	start := time.Date(2023, 11, 13, 23, 59, 0, 0, time.FixedZone("EST", -5*3600)).UnixMilli()
	g := New(11, clock.NewFake(timeMs(start)))

	preDailies := map[string]int{}
	for _, code := range g.Symbols() {
		preDailies[code] = len(g.syms[code].dailies)
	}

	now := start
	crossed := false
	for i := 0; i < 400; i++ { // 400 * 200ms = 80s, enough to cross midnight
		now += 200
		g.StepTo(now)
		g.Drain(now)
		if g.curDay != etDay(start) {
			crossed = true
		}
	}
	if !crossed {
		t.Fatal("expected ET-midnight rollover to have happened")
	}
	for _, code := range g.Symbols() {
		post := len(g.syms[code].dailies)
		if post != preDailies[code]+1 {
			t.Errorf("%s: dailies grew by %d, want 1 (pre=%d post=%d)", code, post-preDailies[code], preDailies[code], post)
		}
	}
}

// TestGenerator_ConcurrentAccessNoRace drives StepTo/Drain from one writer
// goroutine while several reader goroutines hammer every accessor
// concurrently, to exercise mu's concurrent paths under `go test -race`.
func TestGenerator_ConcurrentAccessNoRace(t *testing.T) {
	start := int64(1_700_000_000_000)
	g := New(5, clock.NewFake(timeMs(start)))

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		now := start
		for i := 0; i < 300; i++ {
			now += 200
			g.StepTo(now)
			g.Drain(now)
		}
	}()

	readerDone := make(chan struct{})
	for r := 0; r < 4; r++ {
		go func() {
			defer func() { readerDone <- struct{}{} }()
			for i := 0; i < 500; i++ {
				for _, code := range g.Symbols() {
					g.Has(code)
					g.BookOf(code)
					g.QuoteOf(code)
					g.RecentTicks(code, 10)
					g.CachedBars1m(code, 5)
					g.Fundamentals(code)
				}
				g.RankRows()
			}
		}()
	}
	for r := 0; r < 4; r++ {
		<-readerDone
	}
	<-writerDone
}
