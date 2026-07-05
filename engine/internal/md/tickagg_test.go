package md

import (
	"reflect"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// Reuses t0Ms (09:30:00 ET) and tick() from core_test.go.

func TestTickAggWatermarkAndDelta(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)

	// Two ticks in bucket [09:30:00, 09:30:10).
	got := a.addTick(tick(1, 1_000, 100.0, 10, feed.Buy), false)
	if len(got) != 1 || !got[0].InProgress || got[0].O != 100.0 {
		t.Fatalf("first emission = %+v", got)
	}
	got = a.addTick(tick(2, 9_000, 99.5, 5, feed.Sell), false)
	b := got[len(got)-1]
	if b.H != 100.0 || b.L != 99.5 || b.C != 99.5 || b.V != 15 || b.BuyV != 10 || b.SellV != 5 || b.Ticks != 2 {
		t.Fatalf("in-progress bar = %+v", b)
	}

	// A tick in the NEXT bucket finalizes the first (watermark).
	got = a.addTick(tick(3, 12_000, 99.8, 3, feed.Neutral), false)
	if len(got) != 2 {
		t.Fatalf("emissions = %+v, want [final, in-progress]", got)
	}
	if got[0].InProgress || got[0].BucketMs != t0Ms || got[0].V != 15 {
		t.Fatalf("final bar = %+v", got[0])
	}
	if !got[1].InProgress || got[1].BucketMs != t0Ms+10_000 || got[1].BuyV != 0 || got[1].SellV != 0 {
		t.Fatalf("new in-progress bar = %+v (neutral tick adds no delta)", got[1])
	}
}

func TestTickAggSkipsEmptyBucketsAndDropsLate(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)
	a.addTick(tick(1, 0, 100, 1, feed.Buy), false)
	// Jump 35s: bucket 09:30:30. Only the open bucket finalizes — empty
	// buckets in between are never fabricated.
	got := a.addTick(tick(2, 35_000, 101, 1, feed.Buy), false)
	if len(got) != 2 || got[0].BucketMs != t0Ms || got[1].BucketMs != t0Ms+30_000 {
		t.Fatalf("emissions = %+v", got)
	}
	// A tick for the already-finalized first bucket is dropped.
	if got := a.addTick(tick(3, 5_000, 100.5, 1, feed.Buy), false); got != nil {
		t.Fatalf("late tick emitted %+v, want nothing", got)
	}
	if a.lateDrops() != 1 {
		t.Fatalf("lateDrops = %d, want 1", a.lateDrops())
	}
}

// Seed/live equivalence: the same tick stream produces identical bars whether
// it arrives as one seed burst or split across seed + live batches.
func TestTickAggSeedLiveEquivalence(t *testing.T) {
	ticks := []feed.Tick{
		tick(1, 500, 100, 10, feed.Buy), tick(2, 4_000, 100.2, 5, feed.Sell),
		tick(3, 11_000, 100.1, 8, feed.Buy), tick(4, 19_000, 100.4, 2, feed.Neutral),
		tick(5, 21_000, 100.3, 6, feed.Sell),
	}
	collect := func(splits ...[]feed.Tick) []Bar {
		a := newTickAgg("US.AAPL", session.TF10s)
		var finals []Bar
		for _, batch := range splits {
			for _, tk := range batch {
				for _, b := range a.addTick(tk, false) {
					if !b.InProgress {
						finals = append(finals, b)
					}
				}
			}
		}
		return finals
	}
	oneBurst := collect(ticks)
	split := collect(ticks[:2], ticks[2:])
	if !reflect.DeepEqual(oneBurst, split) {
		t.Fatalf("burst vs split finals differ:\n%+v\n%+v", oneBurst, split)
	}
}

func TestTickAggShadow1m(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF1m)
	a.addTick(tick(1, 1_000, 100, 10, feed.Buy), false)
	got := a.addTick(tick(2, 61_000, 101, 5, feed.Buy), false)
	if len(got) != 2 || got[0].BucketMs != t0Ms || got[0].TF != session.TF1m || got[0].InProgress {
		t.Fatalf("1m watermark emissions = %+v", got)
	}
}

func TestTickAggGapFlag(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)
	a.addTick(tick(1, 0, 100, 1, feed.Buy), false)
	got := a.addTick(tick(2, 12_000, 100, 1, feed.Buy), true) // first bucket after resync
	nb := got[len(got)-1]
	if !nb.Gap {
		t.Fatalf("post-resync bar not gap-flagged: %+v", nb)
	}
	// The flagged bar KEEPS its flag on later updates within the bucket...
	got = a.addTick(tick(3, 13_000, 100, 1, feed.Buy), false)
	if !got[len(got)-1].Gap {
		t.Fatal("gap flag lost on an update to the flagged bucket")
	}
	// ...and once the caller clears its pending state (gapFlag=false), the
	// next bucket is clean.
	got = a.addTick(tick(4, 22_000, 100, 1, feed.Buy), false)
	if got[len(got)-1].Gap {
		t.Fatal("gap flag leaked onto the following bucket")
	}
}

// Standing-policy addition: multiple buckets can be open at once when ticks
// arrive out of order within a burst — a tick for a LATER bucket opens it
// without touching anything, then a tick for an EARLIER (but still not-late)
// bucket opens alongside it, since only buckets strictly older than the
// arriving one are watermark-closed. The next tick for a still-later bucket
// must finalize both open buckets in one shot, oldest first (chronological
// order, not arrival order), and the gap flag must clear correctly across
// more than two buckets afterward.
func TestTickAggMultipleBucketsFinalizeAtOnceThenGapClears(t *testing.T) {
	a := newTickAgg("US.AAPL", session.TF10s)
	// First tick ever is for bucket t0+20s (B) — opens it, map was empty.
	a.addTick(tick(1, 20_000, 101, 1, feed.Buy), false)
	// Second tick is for the EARLIER bucket t0 (D). Not late (watermark is
	// still unset), and B is not older than D, so B stays open too: the map
	// now holds both D and B simultaneously.
	a.addTick(tick(2, 5_000, 100, 1, feed.Buy), false)
	// A tick for t0+40s (E) is newer than both D and B, so both finalize,
	// oldest first: D (t0) then B (t0+20s).
	got := a.addTick(tick(3, 45_000, 102, 1, feed.Buy), true)
	if len(got) != 3 {
		t.Fatalf("emissions = %+v, want 3 (two finals + one new in-progress)", got)
	}
	if got[0].BucketMs != t0Ms || got[0].InProgress {
		t.Fatalf("first final = %+v, want t0 (D) finalized first", got[0])
	}
	if got[1].BucketMs != t0Ms+20_000 || got[1].InProgress {
		t.Fatalf("second final = %+v, want t0+20s (B) finalized second", got[1])
	}
	if !got[2].InProgress || got[2].BucketMs != t0Ms+40_000 || !got[2].Gap {
		t.Fatalf("new in-progress = %+v, want t0+40s (E) gap-flagged", got[2])
	}

	// A later bucket, after the caller clears the pending gap flag, stays
	// clean — the flag doesn't leak past the bucket it was raised on.
	got = a.addTick(tick(4, 62_000, 103, 1, feed.Buy), false)
	if len(got) != 2 || got[len(got)-1].Gap {
		t.Fatalf("post-gap bucket = %+v, want clean (no gap)", got)
	}
}
