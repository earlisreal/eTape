package md

import (
	"math"
	"math/rand"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

func mkBar(i int, c float64, v, buyV int64) Bar {
	return Bar{Symbol: "US.AAPL", TF: session.TF1m, BucketMs: t0Ms + int64(i)*60_000,
		O: c - 0.1, H: c + 0.2, L: c - 0.2, C: c, V: v, BuyV: buyV, SellV: v - buyV}
}

func TestEMAMatchesReference(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 3}}
	ca, err := newCalc(spec)
	if err != nil {
		t.Fatal(err)
	}
	closes := []float64{10, 11, 12, 13, 14}
	var got []float64
	for i, cl := range closes {
		b := mkBar(i, cl, 100, 60)
		for _, p := range ca.points(b) {
			if p.ok {
				got = append(got, p.value)
			}
		}
		ca.fold(b)
	}
	// period 3, alpha .5: seed SMA(10,11,12)=11; then .5*13+.5*11=12; .5*14+.5*12=13.
	want := []float64{11, 12, 13}
	if len(got) != len(want) {
		t.Fatalf("points = %v, want %v", got, want)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("points = %v, want %v", got, want)
		}
	}
}

func TestFormingPointsNeverCompound(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2}}
	pure, _ := newCalc(spec)
	noisy, _ := newCalc(spec)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		b := mkBar(i, 100+rng.Float64(), 100, 50)
		// The noisy calc previews many forming variants before the fold —
		// state must be unaffected.
		for j := 0; j < 5; j++ {
			forming := b
			forming.C += rng.Float64()
			noisy.points(forming)
		}
		pp, np := pure.points(b), noisy.points(b)
		if pp[0].value != np[0].value {
			t.Fatalf("bar %d: forming previews mutated state: %v vs %v", i, pp, np)
		}
		pure.fold(b)
		noisy.fold(b)
	}
}

// Streaming == batch: every type's streamed final point equals a fresh calc
// folding the prefix and previewing the bar.
func TestStreamingEqualsBatchRecompute(t *testing.T) {
	specs := []IndicatorSpec{
		{Type: IndVWAP}, {Type: IndSMA, Params: map[string]float64{"period": 5}},
		{Type: IndEMA, Params: map[string]float64{"period": 4}},
		{Type: IndMACD, Params: map[string]float64{"fast": 3, "slow": 6, "signal": 3}},
		{Type: IndVolume},
	}
	rng := rand.New(rand.NewSource(7))
	var bars []Bar
	px := 100.0
	for i := 0; i < 120; i++ {
		px += rng.Float64() - 0.5
		v := int64(rng.Intn(1000) + 1)
		bars = append(bars, mkBar(i, px, v, v/2))
	}
	for _, spec := range specs {
		spec.Symbol, spec.TF = "US.AAPL", session.TF1m
		streaming, _ := newCalc(spec)
		for i, b := range bars {
			sp := streaming.points(b)
			batch, _ := newCalc(spec)
			for _, prev := range bars[:i] {
				batch.fold(prev)
			}
			bp := batch.points(b)
			for k := range sp {
				if sp[k].ok != bp[k].ok || (sp[k].ok && math.Abs(sp[k].value-bp[k].value) > 1e-9) {
					t.Fatalf("%s bar %d slot %s: streaming %+v != batch %+v", spec.Type, i, sp[k].slot, sp[k], bp[k])
				}
			}
			streaming.fold(b)
		}
	}
}

func TestVWAPResetsAtDayBoundary(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndVWAP}
	ca, _ := newCalc(spec)
	day1 := mkBar(0, 100, 100, 50)
	ca.fold(day1)
	day2 := day1
	day2.BucketMs += 86_400_000 // next ET day
	day2.C, day2.O, day2.H, day2.L = 200, 199.9, 200.2, 199.8
	pts := ca.points(day2)
	tp := (day2.H + day2.L + day2.C) / 3
	if !pts[0].ok || math.Abs(pts[0].value-tp) > 1e-9 {
		t.Fatalf("VWAP after day reset = %+v, want fresh %g", pts[0], tp)
	}
}

func TestIndicatorLifecycleThroughCore(t *testing.T) {
	c, drain := runCore(t)
	// Two finalized 1m bars, then an EMA(2) instance seeds from history.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100, 500)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100, 102, 100, 101, 400)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(2, 101, 103, 101, 102, 300)}})
	c.EnsureIndicator("ema-1", IndicatorSpec{
		Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2},
	})
	// A live update to the forming bar streams a delta point.
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(2, 101, 103.5, 101, 103, 350)}})
	countEma := func(us []Update) (snaps, deltas int, lastSnap IndicatorUpdate) {
		for _, u := range us {
			if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "ema-1" {
				if iu.Snapshot {
					snaps++
					lastSnap = iu
				} else {
					deltas++
				}
			}
		}
		return
	}
	snaps, deltas, snap := countEma(drain())
	if snaps != 1 || snap.SeriesKey != "ema-1" || len(snap.Points) != 1 {
		t.Fatalf("snapshots=%d last=%+v, want 1 snapshot with 1 seeded point (bars 0-1 finalized; EMA(2) warm from bar 1)", snaps, snap)
	}
	if deltas == 0 {
		t.Fatal("no delta point for the forming-bar update")
	}
	c.ReleaseIndicator("ema-1")
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(3, 103, 104, 102, 103.5, 100)}})
	// drain() re-returns the full accumulated stream; ema-1 counts must be
	// frozen after the release.
	snaps2, deltas2, _ := countEma(drain())
	if snaps2 != snaps || deltas2 != deltas {
		t.Fatalf("released instance still emitting: snapshots %d->%d deltas %d->%d", snaps, snaps2, deltas, deltas2)
	}
}

// TestEnsureExistingIdAdoptsNewSpec is the "VWAP not showing" regression: the
// UI re-subscribes the SAME instanceId with a new (symbol, tf) on every chart
// symbol/timeframe switch (ChartController.resetForReload). ensure() used to
// ignore the new spec, leaving the instance computing the old symbol forever —
// its line then sat in the old symbol's price domain, invisible on the new
// chart's scale.
func TestEnsureExistingIdAdoptsNewSpec(t *testing.T) {
	c, drain := runCore(t)
	mk := func(sym string, i int, cl float64) feed.Bar {
		return feed.Bar{Symbol: sym, BucketMs: t0Ms + int64(i)*60_000, O: cl, H: cl + 1, L: cl - 1, C: cl, Volume: 500}
	}
	// Two symbols in different price domains; bucket i+1's arrival finalizes i.
	for i := 0; i < 4; i++ {
		c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{mk("US.AAPL", i, 190+float64(i))}})
		c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{mk("US.NVDA", i, 140+float64(i))}})
	}
	c.EnsureIndicator("vwap-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndVWAP})
	drain()

	// The chart switches to NVDA and re-subscribes the same instanceId.
	c.EnsureIndicator("vwap-1", IndicatorSpec{Symbol: "US.NVDA", TF: session.TF1m, Type: IndVWAP})
	var lastSnap IndicatorUpdate
	countVwap := func(us []Update) (n int) {
		for _, u := range us {
			if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "vwap-1" {
				n++
				if iu.Snapshot {
					lastSnap = iu
				}
			}
		}
		return
	}
	countVwap(drain())
	if len(lastSnap.Points) == 0 {
		t.Fatal("no snapshot after re-ensure with a new symbol")
	}
	// Typical price == close for these bars, equal volumes: NVDA VWAP after the
	// three finalized bars is (140+141+142)/3 = 141. The stale-AAPL value was 191.
	got := lastSnap.Points[len(lastSnap.Points)-1].Value
	if math.Abs(got-141) > 1e-9 {
		t.Fatalf("last snapshot point = %g, want 141 (NVDA domain); stale-spec value would be 191", got)
	}

	// Forward streaming must now follow NVDA (delta on its forming bar) and
	// have stopped following AAPL.
	before := countVwap(drain())
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{mk("US.NVDA", 3, 144)}})
	afterNvda := countVwap(drain())
	if afterNvda == before {
		t.Fatal("no delta streamed for the new symbol's forming bar")
	}
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{mk("US.AAPL", 3, 195)}})
	if got := countVwap(drain()); got != afterNvda {
		t.Fatalf("old symbol still streaming after respec: %d -> %d updates", afterNvda, got)
	}
}

// TestParamEditAfterRefInflation covers the UI's param-edit path
// (updateIndicator = Unsubscribe then Subscribe with the SAME instanceId): once
// a symbol switch has inflated refs past 1, the release no longer frees the id,
// so the re-subscribe hits ensure()'s existing-id branch — which must adopt the
// new params rather than silently keeping the old ones.
func TestParamEditAfterRefInflation(t *testing.T) {
	c, drain := runCore(t)
	for i := 0; i < 5; i++ {
		c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(i, 10+float64(i), 11+float64(i), 9+float64(i), 10+float64(i), 100)}})
	}
	spec := func(period float64) IndicatorSpec {
		return IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndSMA, Params: map[string]float64{"period": period}}
	}
	c.EnsureIndicator("sma-1", spec(2))
	c.EnsureIndicator("sma-1", spec(2)) // symbol-switch resubscribe: refs -> 2
	c.ReleaseIndicator("sma-1")         // param edit: remove...
	c.EnsureIndicator("sma-1", spec(4)) // ...then re-add, same id, new period
	var lastSnap IndicatorUpdate
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "sma-1" && iu.Snapshot {
			lastSnap = iu
		}
	}
	if len(lastSnap.Points) == 0 {
		t.Fatal("no snapshot after param re-ensure")
	}
	// Finalized closes are 10,11,12,13 (bucket 4 still forming). SMA(4) over
	// them = 11.5; a stale SMA(2) would end at 12.5.
	got := lastSnap.Points[len(lastSnap.Points)-1].Value
	if math.Abs(got-11.5) > 1e-9 {
		t.Fatalf("last snapshot point = %g, want SMA(4)=11.5; stale SMA(2) would be 12.5", got)
	}
}

func TestMACDSlotKeys(t *testing.T) {
	c, drain := runCore(t)
	for i := 0; i < 12; i++ {
		c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(i, 100+float64(i), 101+float64(i), 99+float64(i), 100.5+float64(i), 100)}})
	}
	c.EnsureIndicator("macd-1", IndicatorSpec{
		Symbol: "US.AAPL", TF: session.TF1m, Type: IndMACD,
		Params: map[string]float64{"fast": 3, "slow": 6, "signal": 3},
	})
	keys := map[string]bool{}
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "macd-1" && iu.Snapshot {
			keys[iu.SeriesKey] = true
		}
	}
	for _, want := range []string{"macd-1#macd", "macd-1#signal", "macd-1#hist"} {
		if !keys[want] {
			t.Fatalf("snapshot keys = %v, missing %s", keys, want)
		}
	}
}

// ---- Additional coverage beyond the brief's snippet ----

// TestSMAPeriodOneIsIdentity exercises SMA's window-trimming edge case at the
// smallest legal period: the "keep period-1 folded closes" invariant becomes
// "keep zero," so every preview must equal the bar's own close.
func TestSMAPeriodOneIsIdentity(t *testing.T) {
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndSMA, Params: map[string]float64{"period": 1}}
	ca, err := newCalc(spec)
	if err != nil {
		t.Fatal(err)
	}
	for i, cl := range []float64{10, 11, 12} {
		b := mkBar(i, cl, 100, 50)
		pts := ca.points(b)
		if !pts[0].ok || pts[0].value != cl {
			t.Fatalf("bar %d: SMA(1) preview = %+v, want ok with value %g", i, pts[0], cl)
		}
		ca.fold(b)
	}
}

// TestInvalidParamsRejected covers the catalog's parameter-validation
// rejection path: out-of-range and non-integer period/fast/slow/signal
// values must error out of newCalc without panicking, and ensure() must
// reject the same spec without creating an instance or corrupting the
// registry for subsequent valid ensures.
func TestInvalidParamsRejected(t *testing.T) {
	cases := []IndicatorSpec{
		{Type: IndEMA, Params: map[string]float64{"period": 0}},
		{Type: IndEMA, Params: map[string]float64{"period": 401}},
		{Type: IndEMA, Params: map[string]float64{"period": 2.5}},
		{Type: IndSMA, Params: map[string]float64{"period": -1}},
		{Type: IndMACD, Params: map[string]float64{"fast": 12, "slow": 0, "signal": 9}},
	}
	for _, spec := range cases {
		spec.Symbol, spec.TF = "US.AAPL", session.TF1m
		if _, err := newCalc(spec); err == nil {
			t.Fatalf("newCalc(%+v) = nil error, want rejection", spec)
		}
	}

	c, drain := runCore(t)
	c.EnsureIndicator("bad-1", cases[0])
	for _, u := range drain() { // must not panic and must not emit anything for bad-1
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "bad-1" {
			t.Fatalf("rejected spec still emitted: %+v", iu)
		}
	}
	// The registry must still work normally afterward.
	c.EnsureIndicator("good-1", IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2}})
	found := false
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "good-1" && iu.Snapshot {
			found = true
		}
	}
	if !found {
		t.Fatal("valid ensure after a rejected one produced no snapshot")
	}
}

// TestEnsureRefcountReemitsSnapshot covers rule 1's refcount path: a second
// ensure() of an already-live id must re-emit the stored snapshot (a new
// subscriber needs the series) rather than silently no-op, and the instance
// must survive exactly one matching release.
func TestEnsureRefcountReemitsSnapshot(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100, 500)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100, 102, 100, 101, 400)}})
	spec := IndicatorSpec{Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2}}
	c.EnsureIndicator("ema-shared", spec)
	c.EnsureIndicator("ema-shared", spec) // second subscriber, same id
	snaps := 0
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "ema-shared" && iu.Snapshot {
			snaps++
		}
	}
	if snaps != 2 {
		t.Fatalf("snapshots after two ensures = %d, want 2 (one per subscriber)", snaps)
	}
	c.ReleaseIndicator("ema-shared") // one of two refs released; instance must survive
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(2, 101, 103, 101, 102, 300)}})
	deltas := 0
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "ema-shared" && !iu.Snapshot {
			deltas++
		}
	}
	if deltas == 0 {
		t.Fatal("instance stopped emitting after releasing only one of two refs")
	}
}

// TestBackfillRewritesPastTriggersReseed covers rule 2's "old or repeated
// bucket" branch: a deep-history insert at an already-folded bucket must
// recompute the whole instance from finalizedBars and re-emit a snapshot,
// rather than silently ignoring the rewrite or corrupting streamed state.
func TestBackfillRewritesPastTriggersReseed(t *testing.T) {
	c, drain := runCore(t)
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(0, 100, 101, 99, 100, 500)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(1, 100, 102, 100, 101, 400)}})
	c.Feed(feed.Bars1mEvent{Bars: []feed.Bar{bar1m(2, 101, 103, 101, 102, 300)}})
	c.EnsureIndicator("ema-reseed", IndicatorSpec{
		Symbol: "US.AAPL", TF: session.TF1m, Type: IndEMA, Params: map[string]float64{"period": 2},
	})
	drain()
	// Deep-history seed rewrites bucket 0 (already folded) with a different
	// close — this must NOT be treated as a forward delta.
	c.SeedHistory1m("US.AAPL", []feed.Bar{bar1m(0, 100, 101, 99, 999, 500)})
	snaps := 0
	var lastSnap IndicatorUpdate
	for _, u := range drain() {
		if iu, ok := u.(IndicatorUpdate); ok && iu.InstanceID == "ema-reseed" && iu.Snapshot {
			snaps++
			lastSnap = iu
		}
	}
	if snaps == 0 {
		t.Fatal("rewriting a folded bucket produced no re-snapshot")
	}
	// EMA(2) seeded from bars [bucket0(C=999), bucket1(C=101)]: seed=999,
	// warm value at bucket1 = (999+101)/2 = 550.
	if len(lastSnap.Points) != 1 || math.Abs(lastSnap.Points[0].Value-550) > 1e-9 {
		t.Fatalf("re-snapshot after rewrite = %+v, want 1 point with value 550", lastSnap)
	}
}

// ---- EMA (one-shot, non-streaming) ----

func TestEMAFewerThanPeriodClosesNotOk(t *testing.T) {
	if _, ok := EMA([]float64{1, 2}, 3); ok {
		t.Fatal("want ok=false with fewer closes than period")
	}
}

func TestEMAExactlyPeriodClosesIsSMAOfWindow(t *testing.T) {
	closes := []float64{10, 20, 30}
	got, ok := EMA(closes, 3)
	if !ok {
		t.Fatal("want ok=true when len(closes) == period")
	}
	want := (10.0 + 20.0 + 30.0) / 3
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("EMA = %v, want %v (SMA of the full window)", got, want)
	}
}

// TestEMAMatchesStreamingFold checks EMA against the same closes/period/want
// as TestEMAMatchesReference above, confirming the one-shot helper produces
// the streaming calc's final value (13, the last point in that test).
func TestEMAMatchesStreamingFold(t *testing.T) {
	got, ok := EMA([]float64{10, 11, 12, 13, 14}, 3)
	if !ok {
		t.Fatal("want ok=true")
	}
	if math.Abs(got-13) > 1e-9 {
		t.Fatalf("EMA = %v, want 13 (matches emaCalc's final folded value)", got)
	}
}

func TestEMAKnownSequenceFixedExpectedValue(t *testing.T) {
	// period 4, alpha = 2/5 = .4: seed SMA(1,2,3,4)=2.5;
	// fold 5: .4*5+.6*2.5=3.5; fold 6: .4*6+.6*3.5=4.5.
	got, ok := EMA([]float64{1, 2, 3, 4, 5, 6}, 4)
	if !ok {
		t.Fatal("want ok=true")
	}
	if math.Abs(got-4.5) > 1e-9 {
		t.Fatalf("EMA = %v, want 4.5", got)
	}
}
