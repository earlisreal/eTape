package md

import (
	"sort"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// TestReconstructFromJournalMatchesLiveAggregation is the end-to-end parity
// proof for the whole persist-10s-bars branch: ticks recorded into a real
// SQLite journal via Store.RecordEvent, then read back via
// Store.ReadJournalTicks and replayed through Core.SeedSessionTicks, must
// produce byte-for-byte the same TF10s bar series as the same ticks arriving
// live through Core.Feed. Tasks 1-3 each proved one leg of this chain in
// isolation (store round trip, SeedSessionTicks aggregation, backfill
// wiring); this test proves the whole chain glued together end to end.
func TestReconstructFromJournalMatchesLiveAggregation(t *testing.T) {
	// Eight ticks, one per 10s bucket (seq 1-8), mirroring
	// TestSeedSessionTicksThenLiveContinues's layout. Split into a seed batch
	// (seq 1-5) and a live batch (seq 3-8) with a seq 3-5 seed/push overlap.
	dirs := []feed.Direction{feed.Buy, feed.Sell, feed.Buy, feed.Sell, feed.Buy, feed.Sell, feed.Buy, feed.Sell}
	all := make([]feed.Tick, 8)
	for i := 0; i < 8; i++ {
		all[i] = tick(int64(i+1), int64(i)*10_000, 100+float64(i), int64(10+i), dirs[i])
	}
	seedEvent := feed.TicksEvent{Seed: true, Ticks: append([]feed.Tick{}, all[:5]...)} // seq 1-5
	liveEvent := feed.TicksEvent{Ticks: append([]feed.Tick{}, all[2:8]...)}            // seq 3-8 (3-5 overlap)

	// Record both batches into a real temp-file SQLite store, same day
	// partition as t0Ms.
	s, err := store.Open(store.Options{
		Path:          t.TempDir() + "/reconstruct.db",
		Clock:         clock.NewFake(time.UnixMilli(0)),
		FlushInterval: time.Hour, // drive flushing explicitly via s.Flush()
	})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	s.RecordEvent(seedEvent, t0Ms)
	s.RecordEvent(liveEvent, t0Ms+1)
	s.Flush()

	// Live path: feed the same two batches, in the same order, through one
	// Core via the normal Core.Feed path.
	live, liveDrain := runCore(t)
	live.Feed(seedEvent)
	live.Feed(liveEvent)
	liveUpdates := liveDrain()
	liveBars := collectBars(liveUpdates, session.TF10s)
	if len(liveBars) == 0 {
		t.Fatal("live path emitted no TF10s BarUpdates")
	}
	liveFinal := make(map[int64]Bar, len(liveBars))
	for _, b := range liveBars {
		liveFinal[b.BucketMs] = b // last write per bucket wins, matches series.upsert
	}
	liveSorted := make([]Bar, 0, len(liveFinal))
	for _, b := range liveFinal {
		liveSorted = append(liveSorted, b)
	}
	sort.Slice(liveSorted, func(i, j int) bool { return liveSorted[i].BucketMs < liveSorted[j].BucketMs })

	// Reconstruction path: read the journal back and seed a second,
	// independent Core — this is the actual glue under test.
	journaled, err := s.ReadJournalTicks("US.AAPL", t0Ms)
	if err != nil {
		t.Fatalf("ReadJournalTicks: %v", err)
	}
	if len(journaled) != 11 {
		t.Fatalf("journaled ticks = %d, want 11 (5 seed [seq 1-5] + 6 live [seq 3-8], seq 3-5 duplicated verbatim)", len(journaled))
	}

	recon, reconDrain := runCore(t)
	recon.SeedSessionTicks("US.AAPL", journaled)
	reconBars := snapshotBars(reconDrain(), "US.AAPL", session.TF10s)

	// Byte-for-byte parity: same bucket count, same Bar value per bucket in
	// order. Bar (engine/internal/md/update.go) is a plain comparable struct
	// (all scalar fields, no slices/maps), so == compares every field:
	// BucketMs, O, H, L, C, V, BuyV, SellV, Ticks, InProgress, Gap, Symbol, TF.
	if len(reconBars) != len(liveSorted) {
		t.Fatalf("reconstruction TF10s bars = %d, want %d (live)\nrecon: %+v\nlive:  %+v",
			len(reconBars), len(liveSorted), reconBars, liveSorted)
	}
	for i := range liveSorted {
		if reconBars[i] != liveSorted[i] {
			t.Fatalf("bucket %d mismatch:\n recon: %+v\n live:  %+v", i, reconBars[i], liveSorted[i])
		}
	}

	// Sanity: exactly 8 buckets (seq 1-8, one tick per 10s bucket), matching
	// the reference count in TestSeedSessionTicksThenLiveContinues.
	if len(reconBars) != 8 {
		t.Fatalf("final TF10s bucket count = %d, want 8", len(reconBars))
	}
}
