package synth

import (
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
