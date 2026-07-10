package md

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func bar1m(offMin int, o, h, l, cl float64, v int64) feed.Bar {
	return feed.Bar{Symbol: "US.AAPL", BucketMs: t0Ms + int64(offMin)*60_000, O: o, H: h, L: l, C: cl, Volume: v}
}

// collectBars filters BarUpdates for one timeframe out of drained updates.
func collectBars(us []Update, tf session.Timeframe) []Bar {
	var out []Bar
	for _, u := range us {
		if bu, ok := u.(BarUpdate); ok && bu.Bar.TF == tf {
			out = append(out, bu.Bar)
		}
	}
	return out
}

// snapshotBars returns the Bars of the last BarSnapshot for (symbol, tf) in
// us (a seedHistory1m/seedDaily emission), or nil if none was emitted.
func snapshotBars(us []Update, symbol string, tf session.Timeframe) []Bar {
	var out []Bar
	for _, u := range us {
		if bs, ok := u.(BarSnapshot); ok && bs.Symbol == symbol && bs.TF == tf {
			out = bs.Bars
		}
	}
	return out
}

func TestAuth1mWatermarkFinalizes(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101.5, 99, 101, 1500)}}) // same bucket refresh
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 101, 102, 100.8, 101.9, 900)}})
	bars := collectBars(drain(), session.TF1m)
	if len(bars) != 4 { // in-progress, refresh, finalized(0), in-progress(1)
		t.Fatalf("1m updates = %d (%+v), want 4", len(bars), bars)
	}
	if bars[2].InProgress || bars[2].H != 101.5 {
		t.Fatalf("finalized bar = %+v, want final with refreshed H", bars[2])
	}
	if !bars[3].InProgress || bars[3].BucketMs != t0Ms+60_000 {
		t.Fatalf("new forming bar = %+v", bars[3])
	}
}

func TestSeedOverlapIsIdempotent(t *testing.T) {
	c, drain := runCore(t)
	seed := []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000), bar1m(1, 100.5, 102, 100, 101.5, 800)}
	c.Feed(feed.Bars1mEvent{Bars: seed, Seed: true})
	first := len(collectBars(drain(), session.TF1m))
	// The same bars again (reconnect re-seed): no value changed → no emission
	// for bar 0; bar 1 is the forming last, refresh emits are acceptable but
	// values must not change.
	c.Feed(feed.Bars1mEvent{Bars: seed, Seed: true})
	again := collectBars(drain()[first:], session.TF1m)
	for _, b := range again {
		if b.BucketMs == t0Ms && b.H != 101 {
			t.Fatalf("re-seed mutated a finalized bar: %+v", b)
		}
	}
}

func TestCascadeAnchoredAggregation(t *testing.T) {
	c, drain := runCore(t)
	// Five 1m bars 09:30..09:34 → one 5m bucket [09:30, 09:35); the 09:35 bar
	// finalizes it.
	var bars []feed.Bar
	for i := 0; i < 5; i++ {
		bars = append(bars, bar1m(i, 100+float64(i), 100.5+float64(i), 99.5+float64(i), 100.2+float64(i), 100))
	}
	c.Feed(feed.Bars1mEvent{Bars: bars})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(5, 105, 106, 104, 105.5, 50)}})
	fives := collectBars(drain(), session.TF5m)
	if len(fives) == 0 {
		t.Fatal("no 5m bars emitted")
	}
	last := fives[len(fives)-1]
	final5 := fives[len(fives)-2]
	if final5.InProgress || final5.BucketMs != t0Ms || final5.O != 100 || final5.C != 104.2 || final5.V != 500 {
		t.Fatalf("finalized 5m = %+v", final5)
	}
	if !last.InProgress || last.BucketMs != t0Ms+5*60_000 {
		t.Fatalf("forming 5m = %+v", last)
	}
}

func TestShadowDeltaMergesIntoAuth1m(t *testing.T) {
	c, drain := runCore(t)
	// Ticks build the shadow 1m for [09:30,09:31): buy 30, sell 10.
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(1, 1_000, 100, 30, feed.Buy), tick(2, 30_000, 100.1, 10, feed.Sell),
	}})
	// Authoritative K_1M bar for the same bucket arrives.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 100.2, 99.9, 100.1, 45)}})
	bars := collectBars(drain(), session.TF1m)
	got := bars[len(bars)-1]
	if got.BuyV != 30 || got.SellV != 10 {
		t.Fatalf("auth 1m delta = buy %d sell %d, want 30/10", got.BuyV, got.SellV)
	}
}

func TestMismatchEmitsOnDivergence(t *testing.T) {
	c, drain := runCore(t)
	// Shadow finalizes bucket 0 via a tick in bucket 1.
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{
		tick(1, 1_000, 100, 10, feed.Buy), tick(2, 61_000, 100.5, 5, feed.Buy),
	}})
	// Authoritative bar for bucket 0 disagrees on close and volume, then
	// finalizes via bucket 1's bar.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 100.9, 100, 100.9, 500)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100.9, 101, 100.5, 100.6, 200)}})
	var mismatches []MismatchUpdate
	for _, u := range drain() {
		if mu, ok := u.(MismatchUpdate); ok {
			mismatches = append(mismatches, mu)
		}
	}
	if len(mismatches) != 1 || mismatches[0].BucketMs != t0Ms {
		t.Fatalf("mismatches = %+v, want exactly one for bucket 0", mismatches)
	}
}

func TestDerivedDailyAndOfficialReplacement(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000)}})
	dailies := collectBars(drain(), session.TFDay)
	if len(dailies) == 0 || !dailies[len(dailies)-1].InProgress {
		t.Fatalf("derived daily = %+v, want in-progress", dailies)
	}
	day := session.BucketStartMs(t0Ms, session.TFDay)
	c.SeedDaily("US.AAPL", []feed.Bar{{Symbol: "US.AAPL", BucketMs: day, O: 99.8, H: 101.2, L: 98.9, C: 100.7, Volume: 5_000_000}})
	official := snapshotBars(drain(), "US.AAPL", session.TFDay)
	last := official[len(official)-1]
	if last.InProgress || last.O != 99.8 || last.V != 5_000_000 {
		t.Fatalf("official daily = %+v", last)
	}
	// Scope the next assertion to updates emitted AFTER the official seed:
	// runCore's drain() accumulates all history, so the pre-seed derived
	// daily (O=100) would otherwise falsely trip the overwrite check.
	mark := len(drain())
	// Further 1m updates must NOT overwrite the official bar.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100.5, 100.6, 100.4, 100.5, 10)}})
	for _, b := range collectBars(drain()[mark:], session.TFDay) {
		if b.BucketMs == day && b.O != 99.8 {
			t.Fatalf("official daily overwritten by derivation: %+v", b)
		}
	}
}

func TestWeeklyDerivedFromDaily(t *testing.T) {
	c, drain := runCore(t)
	// Mon + Tue official dailies of week 2026-07-06.
	mon := session.BucketStartMs(t0Ms, session.TFDay)
	c.SeedDaily("US.AAPL", []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: mon, O: 100, H: 105, L: 99, C: 104, Volume: 1000},
		{Symbol: "US.AAPL", BucketMs: mon + 86_400_000, O: 104, H: 107, L: 103, C: 106, Volume: 1200},
	})
	weeks := snapshotBars(drain(), "US.AAPL", session.TFWeek)
	w := weeks[len(weeks)-1]
	if w.O != 100 || w.H != 107 || w.C != 106 || w.V != 2200 {
		t.Fatalf("weekly = %+v", w)
	}
	if !w.InProgress {
		t.Fatal("current week must be in-progress (newest daily is inside it)")
	}
}

func TestGapFlagAfterResync(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(1, 1_000, 100, 1, feed.Buy)}})
	c.Feed(feed.ResyncedEvent{})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(2, 25_000, 100.5, 1, feed.Buy)}}) // new 10s bucket
	tens := collectBars(drain(), session.TF10s)
	last := tens[len(tens)-1]
	if !last.Gap {
		t.Fatalf("first 10s bar after resync not gap-flagged: %+v", last)
	}
}

// --- Additional coverage beyond the brief's snippet ---

// TestGapFlagClearsAfterFirstBucket verifies rule 7's clear step: only the
// FIRST newly-opened 10s bucket after a resync carries Gap; subsequent new
// buckets do not.
func TestGapFlagClearsAfterFirstBucket(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(1, 1_000, 100, 1, feed.Buy)}})
	c.Feed(feed.ResyncedEvent{})
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(2, 25_000, 100.5, 1, feed.Buy)}}) // first new bucket → gap
	c.Feed(feed.TicksEvent{Ticks: []feed.Tick{tick(3, 45_000, 100.6, 1, feed.Buy)}}) // next new bucket → no gap
	tens := collectBars(drain(), session.TF10s)
	// Bars in the second new bucket (offset 40_000ms) must not be gap-flagged.
	secondBucket := t0Ms + 40_000
	sawSecond := false
	for _, b := range tens {
		if b.BucketMs == secondBucket {
			sawSecond = true
			if b.Gap {
				t.Fatalf("second new 10s bucket after resync still gap-flagged: %+v", b)
			}
		}
	}
	if !sawSecond {
		t.Fatal("expected a 10s bar for the second post-resync bucket")
	}
	// And the first new bucket (offset 20_000ms) must be gap-flagged.
	firstBucket := t0Ms + 20_000
	sawGap := false
	for _, b := range tens {
		if b.BucketMs == firstBucket && b.Gap {
			sawGap = true
		}
	}
	if !sawGap {
		t.Fatal("first post-resync 10s bucket was not gap-flagged")
	}
}

// TestSeedHistory1mFinalizedAndCascades verifies deep-history 1m seeding:
// bars insert as finalized (never in-progress) and cascade to higher tfs.
func TestSeedHistory1mFinalizedAndCascades(t *testing.T) {
	c, drain := runCore(t)
	var bars []feed.Bar
	for i := 0; i < 5; i++ {
		bars = append(bars, bar1m(i, 100+float64(i), 100.5+float64(i), 99.5+float64(i), 100.2+float64(i), 100))
	}
	c.SeedHistory1m("US.AAPL", bars)
	us := drain()
	oneM := snapshotBars(us, "US.AAPL", session.TF1m)
	if len(oneM) != 5 {
		t.Fatalf("history 1m snapshot bars = %d, want 5", len(oneM))
	}
	for _, b := range oneM {
		if b.InProgress {
			t.Fatalf("history 1m bar must be finalized: %+v", b)
		}
	}
	fives := snapshotBars(us, "US.AAPL", session.TF5m)
	if len(fives) == 0 {
		t.Fatal("history seed did not cascade to 5m")
	}
	last5 := fives[len(fives)-1]
	if last5.O != 100 || last5.C != 104.2 || last5.V != 500 {
		t.Fatalf("cascaded 5m from history = %+v", last5)
	}
}

// TestSeedHistory1mPreservesFormingBar verifies rule: seed must not overwrite
// the live forming (in-progress) 1m bar.
func TestSeedHistory1mPreservesFormingBar(t *testing.T) {
	c, drain := runCore(t)
	// A live forming bar for bucket 0.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000)}})
	_ = drain()
	// History re-seed that includes the same forming bucket with different
	// values must be ignored for the forming bucket.
	c.SeedHistory1m("US.AAPL", []feed.Bar{bar1m(0, 1, 2, 0.5, 1.5, 42)})
	oneM := snapshotBars(drain(), "US.AAPL", session.TF1m)
	for _, b := range oneM {
		if b.BucketMs == t0Ms && (b.O != 100 || b.V != 1000) {
			t.Fatalf("history seed clobbered the live forming bar: %+v", b)
		}
	}
}

// TestMonthlyDerivedFromDaily verifies rule 5 for the monthly timeframe.
func TestMonthlyDerivedFromDaily(t *testing.T) {
	c, drain := runCore(t)
	mon := session.BucketStartMs(t0Ms, session.TFDay)
	c.SeedDaily("US.AAPL", []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: mon, O: 100, H: 105, L: 99, C: 104, Volume: 1000},
		{Symbol: "US.AAPL", BucketMs: mon + 86_400_000, O: 104, H: 107, L: 103, C: 106, Volume: 1200},
	})
	months := snapshotBars(drain(), "US.AAPL", session.TFMonth)
	m := months[len(months)-1]
	if m.O != 100 || m.H != 107 || m.C != 106 || m.V != 2200 {
		t.Fatalf("monthly = %+v", m)
	}
	if !m.InProgress {
		t.Fatal("current month must be in-progress (newest daily is inside it)")
	}
}

// TestSeedSessionTicksBuildsSnapshotWithoutFlood verifies that
// SeedSessionTicks rebuilds a correct TF10s series from a batch of persisted
// ticks while emitting only the final BarSnapshot — zero per-tick BarUpdates
// for TF10s (the flood-prevention proof for a session-ticks reconstruction).
func TestSeedSessionTicksBuildsSnapshotWithoutFlood(t *testing.T) {
	c, drain := runCore(t)
	ticks := []feed.Tick{
		tick(1, 0, 100, 10, feed.Buy),
		tick(2, 3_000, 101, 5, feed.Sell),
		tick(3, 12_000, 102, 7, feed.Buy),  // new 10s bucket -> finalizes bucket 0
		tick(4, 25_000, 103, 3, feed.Sell), // new 10s bucket -> finalizes bucket 10s
	}
	c.SeedSessionTicks("US.AAPL", ticks)
	us := drain()

	if flood := collectBars(us, session.TF10s); len(flood) != 0 {
		t.Fatalf("SeedSessionTicks emitted %d per-bar TF10s BarUpdate(s), want 0: %+v", len(flood), flood)
	}
	tens := snapshotBars(us, "US.AAPL", session.TF10s)
	if len(tens) != 3 {
		t.Fatalf("TF10s snapshot bars = %d, want 3: %+v", len(tens), tens)
	}
	b0, b1, b2 := tens[0], tens[1], tens[2]
	if b0.BucketMs != t0Ms || b0.O != 100 || b0.H != 101 || b0.L != 100 || b0.C != 101 ||
		b0.V != 15 || b0.BuyV != 10 || b0.SellV != 5 || b0.Ticks != 2 || b0.InProgress {
		t.Fatalf("bucket0 = %+v, want finalized O100/H101/L100/C101/V15/Buy10/Sell5/Ticks2", b0)
	}
	if b1.BucketMs != t0Ms+10_000 || b1.O != 102 || b1.C != 102 || b1.V != 7 || b1.BuyV != 7 || b1.InProgress {
		t.Fatalf("bucket10s = %+v, want finalized O102/C102/V7/Buy7", b1)
	}
	if b2.BucketMs != t0Ms+20_000 || b2.O != 103 || b2.V != 3 || b2.SellV != 3 || !b2.InProgress {
		t.Fatalf("bucket20s = %+v, want in-progress O103/V3/Sell3", b2)
	}
}

// TestSeedSessionTicksDedupsBySeq verifies a journal seed+push overlap (a
// duplicate seq within the seeded batch) is counted only once, exactly like
// the live dedup in applyTicks.
func TestSeedSessionTicksDedupsBySeq(t *testing.T) {
	c, drain := runCore(t)
	ticks := []feed.Tick{
		tick(1, 0, 100, 10, feed.Buy),
		tick(2, 3_000, 101, 5, feed.Sell),
		tick(2, 3_000, 101, 5, feed.Sell), // duplicate seq
		tick(3, 12_000, 102, 7, feed.Buy), // finalizes bucket 0
	}
	c.SeedSessionTicks("US.AAPL", ticks)
	tens := snapshotBars(drain(), "US.AAPL", session.TF10s)
	if len(tens) != 2 {
		t.Fatalf("TF10s snapshot bars = %d, want 2: %+v", len(tens), tens)
	}
	if b0 := tens[0]; b0.V != 15 || b0.Ticks != 2 {
		t.Fatalf("bucket0 = %+v, want V=15 Ticks=2 (duplicate seq=2 counted once)", b0)
	}
}

// TestSeedSessionTicksThenLiveContinues verifies seed-then-live continuity:
// seeding seq 1-5 then feeding live seq 3-8 through the normal Core.Feed path
// (3-5 deduped away as seed/push overlap) must land on exactly the same final
// TF10s state as aggregating seq 1-8 once in a single SeedSessionTicks call.
func TestSeedSessionTicksThenLiveContinues(t *testing.T) {
	dirs := []feed.Direction{feed.Buy, feed.Sell, feed.Buy, feed.Sell, feed.Buy, feed.Sell, feed.Buy, feed.Sell}
	all := make([]feed.Tick, 8)
	for i := 0; i < 8; i++ {
		// One tick per 10s bucket: seq i+1, bucket t0Ms+i*10s.
		all[i] = tick(int64(i+1), int64(i)*10_000, 100+float64(i), int64(10+i), dirs[i])
	}

	// Reference: aggregate all 8 ticks once via a single seed call.
	ref, refDrain := runCore(t)
	ref.SeedSessionTicks("US.AAPL", all)
	refBars := snapshotBars(refDrain(), "US.AAPL", session.TF10s)
	if len(refBars) != 8 {
		t.Fatalf("reference TF10s bars = %d, want 8: %+v", len(refBars), refBars)
	}

	// Actual: seed seq 1-5, then feed seq 3-8 live (3-5 must dedup away).
	c, drain := runCore(t)
	c.SeedSessionTicks("US.AAPL", all[:5])
	seedBars := snapshotBars(drain(), "US.AAPL", session.TF10s)
	c.Feed(feed.TicksEvent{Ticks: all[2:8]})
	liveBars := collectBars(drain(), session.TF10s)

	got := make(map[int64]Bar, len(seedBars))
	for _, b := range seedBars {
		got[b.BucketMs] = b
	}
	for _, b := range liveBars {
		got[b.BucketMs] = b // last write per bucket wins, matching series.upsert
	}
	if len(got) != len(refBars) {
		t.Fatalf("final TF10s bucket count = %d, want %d", len(got), len(refBars))
	}
	for _, want := range refBars {
		have, ok := got[want.BucketMs]
		if !ok || have != want {
			t.Fatalf("bucket %d = %+v, want %+v (seed-then-live must equal a single seq 1-8 pass)", want.BucketMs, have, want)
		}
	}
}

// TestSeedSessionTicksSuppressesMismatchDuringSeeding verifies that shadow-1m
// finalization inside a session-ticks seed does not fire MismatchUpdate even
// when it disagrees with an already-present authoritative 1m bar — a restart
// reconstruction must not re-litigate a divergence that live validation
// already had (or will have) its say on.
func TestSeedSessionTicksSuppressesMismatchDuringSeeding(t *testing.T) {
	c, drain := runCore(t)
	// Authoritative, finalized 1m bar for bucket 0 that will disagree with
	// what the tick-derived shadow computes below (O/C/V all differ).
	c.SeedHistory1m("US.AAPL", []feed.Bar{bar1m(0, 100, 100.9, 100, 100.9, 500)})
	_ = drain()

	ticks := []feed.Tick{
		tick(1, 1_000, 105, 50, feed.Buy),  // shadow 1m bucket 0: O=C=105, V=50
		tick(2, 61_000, 106, 10, feed.Buy), // next 1m bucket -> finalizes shadow bucket 0
	}
	c.SeedSessionTicks("US.AAPL", ticks)
	for _, u := range drain() {
		if mu, ok := u.(MismatchUpdate); ok {
			t.Fatalf("MismatchUpdate emitted during seeding: %+v", mu)
		}
	}
}

// TestSeedSessionTicksEmptyIsNoOp verifies nil and empty tick batches produce
// no emitted updates and do not panic.
func TestSeedSessionTicksEmptyIsNoOp(t *testing.T) {
	c, drain := runCore(t)
	c.SeedSessionTicks("US.AAPL", nil)
	c.SeedSessionTicks("US.AAPL", []feed.Tick{})
	if us := drain(); len(us) != 0 {
		t.Fatalf("empty SeedSessionTicks emitted %d update(s), want 0: %+v", len(us), us)
	}
}

// TestSeedSessionTicksExcludesTapeAndMark guards the branch's most
// safety-critical invariant: a restart reconstruction from journaled ticks
// must never touch the tape ring or push a stale last-trade price into
// execution. SeedSessionTicks must emit zero TapeUpdate and push zero Mark,
// even for a non-empty, multi-bucket batch that would trigger both on the
// live Core.Feed path.
func TestSeedSessionTicksExcludesTapeAndMark(t *testing.T) {
	c, drain := runCore(t)
	ticks := []feed.Tick{
		tick(1, 0, 100, 10, feed.Buy),
		tick(2, 3_000, 101, 5, feed.Sell),
		tick(3, 12_000, 102, 7, feed.Buy),  // new 10s bucket -> finalizes bucket 0
		tick(4, 25_000, 103, 3, feed.Sell), // new 10s bucket -> finalizes bucket 10s
	}
	c.SeedSessionTicks("US.AAPL", ticks)
	for _, u := range drain() {
		if tu, ok := u.(TapeUpdate); ok {
			t.Fatalf("SeedSessionTicks emitted a TapeUpdate: %+v", tu)
		}
	}
	select {
	case m := <-c.Marks():
		t.Fatalf("Mark leaked during SeedSessionTicks: %+v", m)
	default:
	}
}

// TestFinalizedBarsAccessor verifies the indicator-seeding accessor returns
// only finalized bars for a timeframe. Uses a non-running Core so the engine
// is driven entirely on this goroutine (no Run goroutine to race with).
func TestFinalizedBarsAccessor(t *testing.T) {
	c := New(Config{}) // not started: emits buffer, nothing reads concurrently
	e := newBarEngine(session.AnchorSecsDefault)
	e.apply1m(c, []feed.Bar{bar1m(0, 100, 101, 99, 100.5, 1000)})
	e.apply1m(c, []feed.Bar{bar1m(1, 101, 102, 100.8, 101.9, 900)})
	fin := e.finalizedBars("US.AAPL", session.TF1m)
	if len(fin) != 1 || fin[0].BucketMs != t0Ms || fin[0].InProgress {
		t.Fatalf("finalizedBars = %+v, want only the finalized bucket 0", fin)
	}
	if e.finalizedBars("US.UNKNOWN", session.TF1m) != nil {
		t.Fatal("finalizedBars for unknown symbol should be nil")
	}
}
