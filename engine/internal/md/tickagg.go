package md

import (
	"sort"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// tickAgg builds bars of one timeframe from a tick stream — the Go port of
// prototypes/tick_to_10s_bars.py (verified live 2026-07-03): bucket by
// EXCHANGE timestamp, finalize on next-bucket evidence (watermark), track
// buy/sell volume delta. Burst-tolerant: cache seeding replays the exact
// same path as live pushes.
type tickAgg struct {
	symbol string
	tf     session.Timeframe

	open           map[int64]*Bar // in-progress buckets
	finalizedAfter int64          // newest finalized bucket; older ticks are late
	late           uint64
}

func newTickAgg(symbol string, tf session.Timeframe) *tickAgg {
	return &tickAgg{symbol: symbol, tf: tf, open: make(map[int64]*Bar), finalizedAfter: -1}
}

func (a *tickAgg) lateDrops() uint64 { return a.late }

// openBar returns the in-progress bar for bucketMs, or nil.
func (a *tickAgg) openBar(bucketMs int64) *Bar { return a.open[bucketMs] }

// addTick returns the emissions caused by t, in order: zero or more FINAL
// bars (watermark-closed buckets), then the in-progress bar for t's bucket.
// gapFlag marks the first NEW bucket opened after a resync.
func (a *tickAgg) addTick(t feed.Tick, gapFlag bool) []Bar {
	bucket := session.BucketStartMs(t.TsMs, a.tf)
	if a.finalizedAfter >= 0 && bucket <= a.finalizedAfter {
		a.late++
		return nil
	}

	var out []Bar
	b, exists := a.open[bucket]
	if !exists {
		// Watermark: a tick for a new bucket closes all earlier open buckets,
		// oldest first. Empty buckets in between are never fabricated.
		var older []int64
		for k := range a.open {
			if k < bucket {
				older = append(older, k)
			}
		}
		sort.Slice(older, func(i, j int) bool { return older[i] < older[j] })
		for _, k := range older {
			fin := *a.open[k]
			fin.InProgress = false
			delete(a.open, k)
			if k > a.finalizedAfter {
				a.finalizedAfter = k
			}
			out = append(out, fin)
		}
		b = &Bar{
			Symbol: a.symbol, TF: a.tf, BucketMs: bucket,
			O: t.Price, H: t.Price, L: t.Price,
			InProgress: true, Gap: gapFlag,
		}
		a.open[bucket] = b
	}

	if t.Price > b.H {
		b.H = t.Price
	}
	if t.Price < b.L {
		b.L = t.Price
	}
	b.C = t.Price
	b.V += t.Volume
	b.Ticks++
	switch t.Dir {
	case feed.Buy:
		b.BuyV += t.Volume
	case feed.Sell:
		b.SellV += t.Volume
	}
	out = append(out, *b)
	return out
}
