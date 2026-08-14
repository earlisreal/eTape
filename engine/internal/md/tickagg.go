package md

import (
	"sort"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// tickBucket keeps eligibility dimensions separate until the bar snapshot is
// materialized. Ineligible Reported Prints never reach this state.
type tickBucket struct {
	symbol   string
	tf       session.Timeframe
	bucketMs int64
	gap      bool

	hasAnchor bool
	anchor    float64
	hasRange  bool
	rangeHigh float64
	rangeLow  float64
	hasLast   bool
	firstLast float64
	last      float64

	v     int64
	buyV  int64
	sellV int64
	ticks int32
}

// tickAgg builds bars of one timeframe from a stamped tick stream. Buckets
// use exchange timestamps and close on next-bucket evidence; the same path is
// used for live pushes and cache reconstruction.
type tickAgg struct {
	symbol string
	tf     session.Timeframe

	open           map[int64]*tickBucket
	finalizedAfter int64
	late           uint64
	trustedClose   float64
	hasTrusted     bool
	trustedCloseTs int64
}

func newTickAgg(symbol string, tf session.Timeframe) *tickAgg {
	return &tickAgg{symbol: symbol, tf: tf, open: make(map[int64]*tickBucket), finalizedAfter: -1}
}

func (a *tickAgg) lateDrops() uint64 { return a.late }

func (a *tickAgg) seedAnchor(price, fallback float64) {
	a.seedAnchorAt(price, fallback, 0)
}

// seedAnchorAt installs a trusted prior close from a quote/history source.
// Older history must not replace a live/current anchor; a zero timestamp is
// accepted only before a timestamped anchor exists.
func (a *tickAgg) seedAnchorAt(price, fallback float64, tsMs int64) {
	if price <= 0 {
		price = fallback
	}
	if price <= 0 {
		return
	}
	if a.hasTrusted && a.trustedCloseTs > 0 && (tsMs == 0 || tsMs < a.trustedCloseTs) {
		return
	}
	a.trustedClose = price
	a.hasTrusted = true
	if tsMs > 0 {
		a.trustedCloseTs = tsMs
	}
	for _, b := range a.open {
		if !b.hasAnchor {
			b.anchor, b.hasAnchor = price, true
		}
	}
}

// openBar returns a materialized in-progress bar for bucketMs, or nil when
// the bucket has no trustworthy price anchor yet.
func (a *tickAgg) openBar(bucketMs int64) *Bar {
	b := a.open[bucketMs]
	if b == nil {
		return nil
	}
	snapshot, ok := a.snapshot(b, true)
	if !ok {
		return nil
	}
	return &snapshot
}

// addTick returns zero or more finalized bars followed by the changed
// in-progress bar. An accepted but fully ineligible Reported Print can still
// provide next-bucket evidence, but it cannot create a bar or change one.
func (a *tickAgg) addTick(t feed.Tick, gapFlag bool) []Bar {
	bucket := session.BucketStartMs(t.TsMs, a.tf)
	if a.finalizedAfter >= 0 && bucket <= a.finalizedAfter {
		a.late++
		return nil
	}

	var out []Bar
	if _, exists := a.open[bucket]; !exists {
		var older []int64
		for k := range a.open {
			if k < bucket {
				older = append(older, k)
			}
		}
		sort.Slice(older, func(i, j int) bool { return older[i] < older[j] })
		for _, k := range older {
			fin, ok := a.snapshot(a.open[k], false)
			if !ok {
				// Keep an unanchored bucket open. A later quote/history anchor may
				// make it materializable; advancing the watermark past it would
				// otherwise force an ineligible price or zero into OHLC.
				break
			}
			out = append(out, fin)
			delete(a.open, k)
			if k > a.finalizedAfter {
				a.finalizedAfter = k
			}
		}
	}

	if !t.RangeEligible && !t.LastEligible && !t.VolumeEligible {
		return out
	}
	b := a.open[bucket]
	if b == nil {
		b = &tickBucket{
			symbol: a.symbol, tf: a.tf, bucketMs: bucket, gap: gapFlag,
			anchor: a.trustedClose, hasAnchor: a.hasTrusted,
		}
		a.open[bucket] = b
	}

	if t.RangeEligible {
		if !b.hasRange {
			b.rangeHigh, b.rangeLow, b.hasRange = t.Price, t.Price, true
		} else {
			if t.Price > b.rangeHigh {
				b.rangeHigh = t.Price
			}
			if t.Price < b.rangeLow {
				b.rangeLow = t.Price
			}
		}
	}
	if t.LastEligible {
		if !b.hasLast {
			b.firstLast, b.hasLast = t.Price, true
		}
		b.last = t.Price
		a.trustedClose, a.hasTrusted = t.Price, true
		if t.TsMs > 0 {
			a.trustedCloseTs = t.TsMs
		}
	}
	if t.VolumeEligible {
		b.v += t.Volume
		b.ticks++
		switch t.Dir {
		case feed.Buy:
			b.buyV += t.Volume
		case feed.Sell:
			b.sellV += t.Volume
		}
	}
	if snapshot, ok := a.snapshot(b, true); ok {
		out = append(out, snapshot)
	}
	return out
}

func (a *tickAgg) snapshot(b *tickBucket, inProgress bool) (Bar, bool) {
	anchor := b.anchor
	hasAnchor := b.hasAnchor
	if !b.hasLast && !hasAnchor {
		return Bar{}, false
	}

	var o, h, l, c float64
	if b.hasLast {
		o, c = b.firstLast, b.last
		h, l = o, o
	} else {
		o, h, l, c = anchor, anchor, anchor, anchor
	}
	if b.hasRange {
		if b.rangeHigh > h {
			h = b.rangeHigh
		}
		if b.rangeLow < l {
			l = b.rangeLow
		}
	}
	if hasAnchor && !b.hasLast {
		if anchor > h {
			h = anchor
		}
		if anchor < l {
			l = anchor
		}
	}
	return Bar{
		Symbol: b.symbol, TF: b.tf, BucketMs: b.bucketMs,
		O: o, H: h, L: l, C: c,
		V: b.v, BuyV: b.buyV, SellV: b.sellV, Ticks: b.ticks,
		InProgress: inProgress, Gap: b.gap,
		VolumeOnly: b.ticks > 0 && !b.hasRange && !b.hasLast,
	}, true
}
