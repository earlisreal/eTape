package synth

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func assertBookInvariants(t *testing.T, b *bookState) {
	t.Helper()
	if len(b.bids) == 0 || len(b.asks) == 0 {
		t.Fatal("empty side")
	}
	if !(b.bids[0].Price < b.asks[0].Price) {
		t.Fatalf("crossed book: bid %.2f >= ask %.2f", b.bids[0].Price, b.asks[0].Price)
	}
	if !sort.SliceIsSorted(b.bids, func(i, j int) bool { return b.bids[i].Price > b.bids[j].Price }) {
		t.Error("bids not descending")
	}
	if !sort.SliceIsSorted(b.asks, func(i, j int) bool { return b.asks[i].Price < b.asks[j].Price }) {
		t.Error("asks not ascending")
	}
	for _, lv := range append(append([]level{}, b.bids...), b.asks...) {
		if lv.Size <= 0 {
			t.Errorf("non-positive size %d @ %.2f", lv.Size, lv.Price)
		}
	}
}

func TestBook_InvariantsAndConsumePromotes(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	s := spec(PersLargeCap)
	b := newBook(rng, s, 100)
	assertBookInvariants(t, b)

	askTouchBefore := b.asks[0].Price
	touchSize := b.asks[0].Size
	// consume the entire touch + into the next level
	px, filled := b.consume(feed.Buy, touchSize+1)
	if filled != touchSize+1 {
		t.Fatalf("filled %d, want %d", filled, touchSize+1)
	}
	if px < askTouchBefore {
		t.Errorf("buy VWAP %.4f below prior touch %.4f", px, askTouchBefore)
	}
	if b.asks[0].Price <= askTouchBefore {
		t.Errorf("touch not promoted: still %.2f", b.asks[0].Price)
	}
	assertBookInvariants(t, b)
}

func TestBook_ReplenishKeepsInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	s := spec(PersRunner)
	b := newBook(rng, s, 5)
	for i := 0; i < 200; i++ {
		b.consume(feed.Buy, b.asks[0].Size/2+1)
		b.replenish(rng, s, 5.10)
		assertBookInvariants(t, b)
	}
}

// TestBook_ConsumeExactDrainNeverEmptiesSide is a regression test for a bug
// where consuming exactly the sum of a side's remaining level sizes drained
// the last level to zero on the same loop iteration that found the side
// non-empty, so the loop exited (remaining == 0) without ever re-extending
// — leaving the touched side at length 0 and best() silently reporting a
// $0.00 touch for it.
func TestBook_ConsumeExactDrainNeverEmptiesSide(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	s := spec(PersLargeCap)
	b := newBook(rng, s, 100)

	var sum int64
	for _, lv := range b.bids {
		sum += lv.Size
	}

	px, filled := b.consume(feed.Sell, sum)
	if filled != sum {
		t.Fatalf("filled %d, want %d", filled, sum)
	}
	if px <= 0 {
		t.Fatalf("non-positive VWAP %.4f on exact-drain consume", px)
	}
	if len(b.bids) == 0 {
		t.Fatal("bids emptied by exact-drain consume")
	}
	assertBookInvariants(t, b)
}

// TestBook_ConsumeDeepSweepSynthesizesLevels forces a sweep well past the
// entire initial ladder (touchSize*20), driving consume through many
// extendSide calls, and checks the book still satisfies every invariant
// (bounded, sorted, non-empty, uncrossed) afterward.
func TestBook_ConsumeDeepSweepSynthesizesLevels(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	s := spec(PersRunner)
	b := newBook(rng, s, 5)

	touchSize := b.asks[0].Size
	qty := touchSize * 20
	px, filled := b.consume(feed.Buy, qty)
	if filled != qty {
		t.Fatalf("filled %d, want %d", filled, qty)
	}
	if px <= 0 {
		t.Fatalf("non-positive VWAP %.4f on deep-sweep consume", px)
	}
	if len(b.asks) == 0 {
		t.Fatal("asks emptied by deep-sweep consume")
	}
	assertBookInvariants(t, b)
}

// TestBook_ReplenishRecentersDriftedTouch is a regression test for the real
// mechanism behind 100% of Seed() boots hitting an implausible price (worst
// observed: $5.28 open -> $76,970.17 close, 14,578x) despite two rounds of
// (correct, but irrelevant) fixes to price.go's Mid/Anchor mechanics: a
// direct trace showed Mid staying sane near its true scale the entire time,
// while the BOOK's touch drifted arbitrarily far away and stayed there —
// bestBid pinned at $0.01, bestAsk at $19.32, for 60+ consecutive ticks,
// while Mid sat at $5.41.
//
// Root cause: topUp only rebuilds a side from mid when that side is
// COMPLETELY EMPTY (len(side)==0). consume's extendSide guarantees a side
// is never left empty (a separate, correct fix from an earlier task), so
// once a deep sweep has pushed the touch away from mid even once, topUp's
// rebuild-from-mid branch can never fire again — it only ever extends
// OUTWARD from the existing (stale) touch, walking further from mid with
// every subsequent sweep in the same direction. Nothing in the live path
// (stepSymbol) or the fine-pass seeder (seedIntraday) ever calls
// rebuildAround to recenter mid-run — that only happens once, at book
// construction and at day rollovers.
//
// Reproduces by repeatedly sweeping one side and calling replenish (exactly
// the consume-then-replenish sequence genTicks performs after every trade),
// with mid held fixed throughout, and asserting the touch never drifts more
// than a small multiple of the spread away from mid.
func TestBook_ReplenishRecentersDriftedTouch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := spec(PersRunner)
	mid := 5.43
	b := newBook(rng, s, mid)

	for i := 0; i < 200; i++ {
		qty := b.asks[0].Size + 1 // sweep past the touch every time
		b.consume(feed.Buy, qty)
		b.replenish(rng, s, mid)
		assertBookInvariants(t, b)

		bestBid, bestAsk := b.best()
		// The rebuild threshold in replenish compares the touch against its
		// own anchor (mid +/- halfSpread), not against mid directly, so the
		// true bound on distance-from-mid is halfSpread+maxTouchDrift (plus a
		// tiny epsilon: the exact worst case can land precisely at that bound
		// before the next replenish call rebuilds it).
		maxDrift := halfSpread(s, false) + maxTouchDrift(s) + 1e-9
		if math.Abs(bestBid-mid) > maxDrift || math.Abs(bestAsk-mid) > maxDrift {
			t.Fatalf("iter=%d: touch drifted too far from mid=%.4f: bestBid=%.4f bestAsk=%.4f (max drift %.4f)",
				i, mid, bestBid, bestAsk, maxDrift)
		}
	}
}

// TestBook_FixCrossedPreservesAskSortOrder is a regression test for a bug
// TestGenerator_StatisticalSanityAcrossSeedsAndPersonalities's full-ladder
// sweep caught ("asks not ascending"): fixCrossed's plain one-cent nudge to
// asks[0] (round2(bids[0].Price+0.01)) can itself land at or past
// asks[1].Price, silently breaking the ask side's ascending-sort invariant.
// Constructs the exact crossed scenario directly (bypassing consume/
// replenish's normal randomness) so this targets fixCrossed's own logic,
// not a statistical chance of hitting it.
func TestBook_FixCrossedPreservesAskSortOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := spec(PersLargeCap)
	b := &bookState{
		bids: []level{{Price: 10.02, Size: 100, Orders: 1}, {Price: 9.99, Size: 100, Orders: 1}},
		// A naive nudge would set asks[0] = round2(10.02+0.01) = 10.03,
		// strictly past the existing asks[1] (10.02) -- the bug this test
		// targets.
		asks: []level{{Price: 10.00, Size: 100, Orders: 1}, {Price: 10.02, Size: 100, Orders: 1}, {Price: 10.05, Size: 100, Orders: 1}},
	}

	b.fixCrossed(rng, s)

	assertBookInvariants(t, b)
	if b.bids[0].Price >= b.asks[0].Price {
		t.Fatalf("still crossed after fixCrossed: bid=%.4f ask=%.4f", b.bids[0].Price, b.asks[0].Price)
	}
}

// TestBook_ReplenishRespectsSpreadProfile is a regression test for a real
// product defect Task 11's statistical sanity sweep found: SpreadProfile's
// documented bound (spread normally within [MinCents,MaxCents], "occasionally
// flushing out" to MaxCents*FlushMult) was not actually enforced anywhere in
// the steady-state (post-construction) book model. replenish's only
// correction mechanism was a flat 10%-of-mid drift threshold -- for anything
// over a few dollars, that's dollars, not cents, so a symbol's spread could
// (and empirically, typically did) run 100-1000x over its documented bound.
// Sweeps consume+replenish for a large-cap symbol (the tightest documented
// profile, MinCents:1/MaxCents:2/FlushMult:1.5) and asserts the spread stays
// within a generous multiple of the profile's own flush-widened ceiling --
// not the exact ceiling itself (a single-tick spike immediately after a
// rebuild-triggering sweep is expected and fine), but nowhere near the old
// 100-1000x reality.
func TestBook_ReplenishRespectsSpreadProfile(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	s := spec(PersLargeCap)
	// spec()'s shared fixture hardcodes Spread{1,5,4} regardless of the
	// Pers argument (matching the runner profile, not large-cap) -- override
	// it explicitly here to actually exercise universe.go's real large-cap
	// numbers, per this test's own doc comment above.
	s.Spread = SpreadProfile{MinCents: 1, MaxCents: 2, FlushMult: 1.5}
	mid := 100.0
	b := newBook(rng, s, mid)

	flushCeilingDollars := float64(s.Spread.MaxCents) * s.Spread.FlushMult / 100
	// Generous margin over the documented ceiling -- this is a defect fix,
	// not a claim the model hits the exact documented bound at all times.
	const marginMult = 10.0
	maxAllowedSpread := flushCeilingDollars * marginMult

	for i := 0; i < 500; i++ {
		if i%2 == 0 {
			b.consume(feed.Buy, b.asks[0].Size+1)
		} else {
			b.consume(feed.Sell, b.bids[0].Size+1)
		}
		b.replenish(rng, s, mid)
		assertBookInvariants(t, b)

		bid, ask := b.best()
		if spread := ask - bid; spread > maxAllowedSpread {
			t.Fatalf("iter=%d: spread %.4f exceeds %.1fx the documented flush ceiling %.4f (bid=%.4f ask=%.4f)",
				i, spread, marginMult, flushCeilingDollars, bid, ask)
		}
	}
}
