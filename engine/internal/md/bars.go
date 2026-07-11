package md

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Validation thresholds for K_1M vs tick-derived 1m (alarm, not blocker —
// K_1M wins for display). Tuned after Monday's live session.
const (
	mismatchPriceTol = 1e-6
	mismatchVolPct   = 0.02
	mismatchVolAbs   = 100
)

var cascadeTFs = []session.Timeframe{session.TF5m, session.TF15m, session.TF30m, session.TF60m}

// series is one symbol+timeframe bar sequence, ascending by BucketMs.
type series struct {
	bars []Bar
}

func (s *series) idx(bucketMs int64) int {
	return sort.Search(len(s.bars), func(i int) bool { return s.bars[i].BucketMs >= bucketMs })
}

func (s *series) get(bucketMs int64) *Bar {
	i := s.idx(bucketMs)
	if i < len(s.bars) && s.bars[i].BucketMs == bucketMs {
		return &s.bars[i]
	}
	return nil
}

// upsert inserts b in order or replaces the existing bar; reports change.
func (s *series) upsert(b Bar) bool {
	i := s.idx(b.BucketMs)
	if i < len(s.bars) && s.bars[i].BucketMs == b.BucketMs {
		if s.bars[i] == b {
			return false
		}
		s.bars[i] = b
		return true
	}
	s.bars = append(s.bars, Bar{})
	copy(s.bars[i+1:], s.bars[i:])
	s.bars[i] = b
	return true
}

func (s *series) last() *Bar {
	if len(s.bars) == 0 {
		return nil
	}
	return &s.bars[len(s.bars)-1]
}

func (s *series) maxBucket() int64 {
	if b := s.last(); b != nil {
		return b.BucketMs
	}
	return -1
}

// rangeBars returns bars with BucketMs in [from, to).
func (s *series) rangeBars(from, to int64) []Bar {
	lo := s.idx(from)
	hi := s.idx(to)
	return s.bars[lo:hi]
}

func (s *series) finalized() []Bar {
	out := make([]Bar, 0, len(s.bars))
	for _, b := range s.bars {
		if !b.InProgress {
			out = append(out, b)
		}
	}
	return out
}

// symbolBars is all bar state for one symbol.
type symbolBars struct {
	symbol string
	agg10  *tickAgg
	shadow *tickAgg

	series map[session.Timeframe]*series

	shadowFinals  map[int64]Bar  // tick-derived 1m finals: delta source + validation
	compared      map[int64]bool // per-bucket validation guard
	dailyOfficial map[int64]bool // K_DAY-seeded buckets: derivation must not touch
	gapPending    bool
	curDay        int64
}

// barEngine owns every derived bar series. It is called only from the Core's
// single writer goroutine — no locks, no clock, event timestamps only.
type barEngine struct {
	anchorSecs int64
	symbols    map[string]*symbolBars
}

func newBarEngine(anchorSecs int64) *barEngine {
	return &barEngine{anchorSecs: anchorSecs, symbols: make(map[string]*symbolBars)}
}

func (e *barEngine) sym(symbol string) *symbolBars {
	sb := e.symbols[symbol]
	if sb == nil {
		sb = &symbolBars{
			symbol:        symbol,
			agg10:         newTickAgg(symbol, session.TF10s),
			shadow:        newTickAgg(symbol, session.TF1m),
			series:        make(map[session.Timeframe]*series),
			shadowFinals:  make(map[int64]Bar),
			compared:      make(map[int64]bool),
			dailyOfficial: make(map[int64]bool),
		}
		for _, tf := range []session.Timeframe{session.TF10s, session.TF1m, session.TF5m,
			session.TF15m, session.TF30m, session.TF60m, session.TFDay, session.TFWeek, session.TFMonth} {
			sb.series[tf] = &series{}
		}
		e.symbols[symbol] = sb
	}
	return sb
}

func (e *barEngine) markGaps() {
	for _, sb := range e.symbols {
		sb.gapPending = true
	}
}

func (e *barEngine) finalizedBars(symbol string, tf session.Timeframe) []Bar {
	sb := e.symbols[symbol]
	if sb == nil {
		return nil
	}
	return sb.series[tf].finalized()
}

// applyTicks drives the 10s series (displayed) and the shadow 1m (internal:
// validation + delta). Ticks arrive deduped from the core.
func (e *barEngine) applyTicks(c *Core, ticks []feed.Tick) {
	if len(ticks) == 0 {
		return
	}
	sb := e.sym(ticks[0].Symbol)
	for _, t := range ticks {
		if day := session.DayMs(t.TsMs); day != sb.curDay {
			sb.curDay = day
			sb.shadowFinals = make(map[int64]Bar)
			sb.compared = make(map[int64]bool)
		}
		for _, b := range sb.agg10.addTick(t, sb.gapPending) {
			if b.Gap && b.InProgress {
				sb.gapPending = false
			}
			sb.series[session.TF10s].upsert(b)
			c.barOut(b)
		}
		for _, b := range sb.shadow.addTick(t, false) {
			if b.InProgress {
				e.mergeShadowDelta(c, sb, b, false)
			} else {
				sb.shadowFinals[b.BucketMs] = b
				e.mergeShadowDelta(c, sb, b, true)
				e.validate(c, sb, b.BucketMs)
			}
		}
	}
}

// mergeShadowDelta copies BuyV/SellV/Ticks from a shadow bar into the
// matching authoritative 1m bar. final=false only touches an in-progress
// auth bar (a finalized bar's delta is settled by the shadow final).
func (e *barEngine) mergeShadowDelta(c *Core, sb *symbolBars, shadow Bar, final bool) {
	ab := sb.series[session.TF1m].get(shadow.BucketMs)
	if ab == nil || (!final && !ab.InProgress) {
		return
	}
	if ab.BuyV == shadow.BuyV && ab.SellV == shadow.SellV && ab.Ticks == shadow.Ticks {
		return
	}
	ab.BuyV, ab.SellV, ab.Ticks = shadow.BuyV, shadow.SellV, shadow.Ticks
	c.barOut(*ab)
	e.cascade(c, sb, ab.BucketMs)
}

// apply1m upserts authoritative K_1M bars (push or cache seed) with the
// watermark rule and drives everything derived from 1m.
func (e *barEngine) apply1m(c *Core, bars []feed.Bar) {
	if len(bars) == 0 {
		return
	}
	sb := e.sym(bars[0].Symbol)
	oneM := sb.series[session.TF1m]
	for _, raw := range bars {
		nb := Bar{
			Symbol: raw.Symbol, TF: session.TF1m, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
			InProgress: true,
		}
		e.fillDelta(sb, &nb)
		last := oneM.last()
		finalizedPrev := int64(-1)
		switch {
		case last == nil || nb.BucketMs > last.BucketMs:
			if last != nil && last.InProgress {
				last.InProgress = false
				c.barOut(*last)
				finalizedPrev = last.BucketMs
			}
			oneM.upsert(nb)
			c.barOut(nb)
		case nb.BucketMs == last.BucketMs:
			nb.InProgress = last.InProgress
			if oneM.upsert(nb) {
				c.barOut(nb)
			}
		default: // older bucket: seed overlap / history — always finalized
			nb.InProgress = false
			if oneM.upsert(nb) {
				c.barOut(nb)
				e.validate(c, sb, nb.BucketMs)
			}
		}
		if finalizedPrev >= 0 {
			e.validate(c, sb, finalizedPrev)
			// The finalized bar's HIGHER buckets must recompute too — the new
			// bucket may have crossed a 5m/15m/... boundary, and only
			// next-bucket evidence (now present) can flip them to final.
			e.cascade(c, sb, finalizedPrev)
		}
		e.cascade(c, sb, nb.BucketMs)
		e.deriveDaily(c, sb, nb.BucketMs)
	}
}

// fillDelta seeds a fresh auth 1m bar's delta fields from the shadow.
func (e *barEngine) fillDelta(sb *symbolBars, b *Bar) {
	if sf, ok := sb.shadowFinals[b.BucketMs]; ok {
		b.BuyV, b.SellV, b.Ticks = sf.BuyV, sf.SellV, sf.Ticks
		return
	}
	if ob := sb.shadow.openBar(b.BucketMs); ob != nil {
		b.BuyV, b.SellV, b.Ticks = ob.BuyV, ob.SellV, ob.Ticks
	}
}

// seedHist1mTFs are the timeframes seedHistory1m can touch (1m itself, its
// intraday cascade, and everything deriveDaily/deriveWM fold up to) — the
// full set snapshotted once the seed loop finishes.
var seedHist1mTFs = []session.Timeframe{
	session.TF1m, session.TF5m, session.TF15m, session.TF30m, session.TF60m,
	session.TFDay, session.TFWeek, session.TFMonth,
}

// seedDailyTFs are the timeframes seedDaily can touch.
var seedDailyTFs = []session.Timeframe{session.TFDay, session.TFWeek, session.TFMonth}

// seedOlder1mTFs are the intraday timeframes deepened by an older-1m chunk.
// Daily/week/month are excluded: older 1m always lands inside the official-
// daily-covered range (>=2016), where deriveDaily no-ops on dailyOfficial
// buckets, so no D/W/M bar mutates.
var seedOlder1mTFs = []session.Timeframe{
	session.TF1m, session.TF5m, session.TF15m, session.TF30m, session.TF60m,
}

// seedHistory1m inserts deep-history 1m bars (all finalized) without
// disturbing the live forming bar, then re-derives everything once per bar.
// Per-bar emission is suppressed for the whole batch (c.seeding): a deep seed
// can span tens of thousands of bars once cascaded, which would overflow
// Core's drop-on-full updates channel if emitted per-bar. One BarSnapshot per
// touched timeframe, plus one indicator reseed per attached instance, is
// emitted instead once the batch is fully applied.
func (e *barEngine) seedHistory1m(c *Core, symbol string, bars []feed.Bar) {
	if len(bars) == 0 {
		return
	}
	sb := e.sym(symbol)
	oneM := sb.series[session.TF1m]
	forming := int64(-1)
	if lb := oneM.last(); lb != nil && lb.InProgress {
		forming = lb.BucketMs
	}
	c.seeding = true
	for _, raw := range bars {
		if raw.BucketMs == forming {
			continue // the live stream owns the forming bar
		}
		nb := Bar{
			Symbol: symbol, TF: session.TF1m, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
		}
		e.fillDelta(sb, &nb)
		if oneM.upsert(nb) {
			c.barOut(nb)
			e.cascade(c, sb, nb.BucketMs)
			e.deriveDaily(c, sb, nb.BucketMs)
		}
	}
	c.seeding = false
	e.emitSeedSnapshots(c, sb, seedHist1mTFs)
	c.inds.reseedSymbol(c, symbol)
}

// seedOlder1m upserts a strictly-older 1m chunk, cascades to 5m/15m/30m/60m,
// and emits one BarPrepend per intraday TF carrying ONLY the newly-added older
// bars (constant per-chunk wire cost). Per-bar emission is suppressed for the
// whole batch (c.seeding); indicators reseed once at the end.
//
// "Newly added" = bars older than each TF's previous earliest bucket. Chunks
// are whole trading days and intraday buckets are session-anchored (never span
// days), so the previously-earliest 5m-60m bucket cannot be mutated by an older
// chunk — the strict "< prevOldest" filter captures exactly the new bars.
func (e *barEngine) seedOlder1m(c *Core, symbol string, bars []feed.Bar) {
	if len(bars) == 0 {
		return
	}
	sb := e.sym(symbol)
	oneM := sb.series[session.TF1m]

	// Capture each intraday TF's earliest bucket before seeding.
	prevOldest := make(map[session.Timeframe]int64, len(seedOlder1mTFs))
	for _, tf := range seedOlder1mTFs {
		s := sb.series[tf]
		if len(s.bars) > 0 {
			prevOldest[tf] = s.bars[0].BucketMs
		} else {
			prevOldest[tf] = math.MaxInt64
		}
	}

	forming := int64(-1)
	if lb := oneM.last(); lb != nil && lb.InProgress {
		forming = lb.BucketMs
	}
	c.seeding = true
	for _, raw := range bars {
		if raw.BucketMs == forming {
			continue
		}
		nb := Bar{
			Symbol: symbol, TF: session.TF1m, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
		}
		e.fillDelta(sb, &nb)
		if oneM.upsert(nb) {
			c.barOut(nb) // suppressed while seeding
			e.cascade(c, sb, nb.BucketMs)
			e.deriveDaily(c, sb, nb.BucketMs) // no-ops on dailyOfficial buckets
		}
	}
	c.seeding = false

	// Emit the new-older prefix per intraday TF (series ascending → prefix < prevOldest).
	for _, tf := range seedOlder1mTFs {
		s := sb.series[tf]
		var newer []Bar
		for _, b := range s.bars {
			if b.BucketMs >= prevOldest[tf] {
				break
			}
			newer = append(newer, b)
		}
		if len(newer) > 0 {
			c.emit(BarPrepend{Symbol: symbol, TF: tf, Bars: newer})
		}
	}
	c.inds.reseedSymbol(c, symbol)
}

// seedTicksTFs are the timeframes a session-ticks seed can touch: agg10
// populates TF10s directly, and the shadow 1m aggregator's finalized bars
// merge their delta into an existing authoritative TF1m bar (mergeShadowDelta)
// which in turn cascades to TF5m-TF60m. A bare tick seed with no authoritative
// 1m bars yet only ever touches TF10s, but list the full intraday set anyway:
// emitSeedSnapshots' empty-series skip makes the extra entries free, and this
// keeps the result correct if an authoritative 1m seed lands before the tick
// seed (out of the recommended order), letting mergeShadowDelta's cascade
// reach 5m-60m within this same seeding window.
var seedTicksTFs = []session.Timeframe{
	session.TF10s, session.TF1m, session.TF5m, session.TF15m, session.TF30m, session.TF60m,
}

// emitTickSeedSnapshots emits the seedTicksTFs snapshot set for symbol, the
// SeedSessionTicks counterpart to seedHistory1m/seedDaily's emitSeedSnapshots
// call.
func (e *barEngine) emitTickSeedSnapshots(c *Core, symbol string) {
	if sb := e.symbols[symbol]; sb != nil {
		e.emitSeedSnapshots(c, sb, seedTicksTFs)
	}
}

// emitSeedSnapshots emits one BarSnapshot per non-empty timeframe in tfs, the
// lossless replacement for seedHistory1m/seedDaily's per-bar emissions.
func (e *barEngine) emitSeedSnapshots(c *Core, sb *symbolBars, tfs []session.Timeframe) {
	for _, tf := range tfs {
		s := sb.series[tf]
		if len(s.bars) == 0 {
			continue
		}
		c.emit(BarSnapshot{Symbol: sb.symbol, TF: tf, Bars: append([]Bar(nil), s.bars...)})
	}
}

// cascade recomputes the higher intraday bars containing the 1m bucket m.
func (e *barEngine) cascade(c *Core, sb *symbolBars, m int64) {
	oneM := sb.series[session.TF1m]
	for _, tf := range cascadeTFs {
		span, _ := session.IntradaySpanSecs(tf)
		spanMs := span * 1000
		hb := session.BucketStartMsAnchored(m, tf, e.anchorSecs)
		members := oneM.rangeBars(hb, hb+spanMs)
		if len(members) == 0 {
			continue
		}
		nb := foldBars(sb.symbol, tf, hb, members)
		nb.InProgress = oneM.maxBucket() < hb+spanMs // next-bucket evidence
		if sb.series[tf].upsert(nb) {
			c.barOut(nb)
		}
	}
}

// deriveDaily maintains the live derived daily bar (always in-progress —
// official K_DAY bars are fetched, never aggregated, and replace it).
func (e *barEngine) deriveDaily(c *Core, sb *symbolBars, m int64) {
	day := session.BucketStartMs(m, session.TFDay)
	if sb.dailyOfficial[day] {
		return
	}
	members := sb.series[session.TF1m].rangeBars(day, day+86_400_000)
	if len(members) == 0 {
		return
	}
	nb := foldBars(sb.symbol, session.TFDay, day, members)
	nb.InProgress = true
	if sb.series[session.TFDay].upsert(nb) {
		c.barOut(nb)
		e.deriveWM(c, sb, day)
	}
}

// seedDaily upserts official (auction-priced, forward-adjusted) daily bars.
// Like seedHistory1m, per-bar emission is suppressed for the whole batch;
// one BarSnapshot per touched timeframe (+ one indicator reseed per attached
// instance) replaces it once the batch is applied.
func (e *barEngine) seedDaily(c *Core, symbol string, bars []feed.Bar) {
	if len(bars) == 0 {
		return
	}
	sb := e.sym(symbol)
	c.seeding = true
	for _, raw := range bars {
		nb := Bar{
			Symbol: symbol, TF: session.TFDay, BucketMs: raw.BucketMs,
			O: raw.O, H: raw.H, L: raw.L, C: raw.C, V: raw.Volume,
		}
		sb.dailyOfficial[raw.BucketMs] = true
		if sb.series[session.TFDay].upsert(nb) {
			c.barOut(nb)
			e.deriveWM(c, sb, raw.BucketMs)
		}
	}
	c.seeding = false
	e.emitSeedSnapshots(c, sb, seedDailyTFs)
	c.inds.reseedSymbol(c, symbol)
}

// deriveWM recomputes the weekly and monthly bars containing day.
func (e *barEngine) deriveWM(c *Core, sb *symbolBars, day int64) {
	daily := sb.series[session.TFDay]
	newest := daily.maxBucket()
	for _, tf := range []session.Timeframe{session.TFWeek, session.TFMonth} {
		hb := session.BucketStartMs(day, tf)
		var members []Bar
		anyInProgress := false
		for _, d := range daily.bars {
			if session.BucketStartMs(d.BucketMs, tf) == hb {
				members = append(members, d)
				anyInProgress = anyInProgress || d.InProgress
			}
		}
		if len(members) == 0 {
			continue
		}
		nb := foldBars(sb.symbol, tf, hb, members)
		nb.InProgress = anyInProgress || session.BucketStartMs(newest, tf) == hb
		if sb.series[tf].upsert(nb) {
			c.barOut(nb)
		}
	}
}

// foldBars aggregates ordered constituent bars into one bar of tf at bucket.
func foldBars(symbol string, tf session.Timeframe, bucket int64, members []Bar) Bar {
	nb := Bar{
		Symbol: symbol, TF: tf, BucketMs: bucket,
		O: members[0].O, H: members[0].H, L: members[0].L, C: members[len(members)-1].C,
	}
	for _, mb := range members {
		nb.H = math.Max(nb.H, mb.H)
		nb.L = math.Min(nb.L, mb.L)
		nb.V += mb.V
		nb.BuyV += mb.BuyV
		nb.SellV += mb.SellV
		nb.Ticks += mb.Ticks
	}
	return nb
}

// validate compares finalized authoritative vs shadow 1m for one bucket,
// once. Divergence is an alarm (MismatchUpdate), never a blocker.
func (e *barEngine) validate(c *Core, sb *symbolBars, bucketMs int64) {
	if c.seeding {
		return
	}
	if sb.compared[bucketMs] {
		return
	}
	shadow, ok := sb.shadowFinals[bucketMs]
	if !ok {
		return
	}
	auth := sb.series[session.TF1m].get(bucketMs)
	if auth == nil || auth.InProgress {
		return
	}
	sb.compared[bucketMs] = true
	var details []string
	price := func(name string, a, b float64) {
		if math.Abs(a-b) > mismatchPriceTol {
			details = append(details, fmt.Sprintf("%s kline=%g tick=%g", name, a, b))
		}
	}
	price("O", auth.O, shadow.O)
	price("H", auth.H, shadow.H)
	price("L", auth.L, shadow.L)
	price("C", auth.C, shadow.C)
	dv := auth.V - shadow.V
	if dv < 0 {
		dv = -dv
	}
	volTol := int64(float64(auth.V) * mismatchVolPct)
	if volTol < mismatchVolAbs {
		volTol = mismatchVolAbs
	}
	if dv > volTol {
		details = append(details, fmt.Sprintf("V kline=%d tick=%d", auth.V, shadow.V))
	}
	if len(details) > 0 {
		c.emit(MismatchUpdate{Symbol: sb.symbol, BucketMs: bucketMs, Detail: strings.Join(details, "; ")})
	}
}
