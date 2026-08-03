package store

import (
	"testing"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestArchive1mUpsertAndRead(t *testing.T) {
	s := open(t)
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 1000, O: 10, H: 11, L: 9, C: 10.5, Volume: 100})
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 2000, O: 10.5, H: 12, L: 10, C: 11.8, Volume: 200})
	// Re-finalize the first bucket with corrected values — must REPLACE, not duplicate.
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 1000, O: 10, H: 11.5, L: 9, C: 11, Volume: 150})
	s.Flush()

	got, err := s.ReadBars1m("US.AAPL", 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("bars = %d, want 2 (upsert, no dupes)", len(got))
	}
	if got[0].BucketMs != 1000 || got[0].C != 11 || got[0].Volume != 150 {
		t.Fatalf("bucket 1000 not replaced: %+v", got[0])
	}
	if got[1].BucketMs != 2000 {
		t.Fatalf("ordering wrong: %+v", got)
	}
	// Range filter excludes bucket 2000.
	only1, err := s.ReadBars1m("US.AAPL", 0, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if len(only1) != 1 || only1[0].BucketMs != 1000 {
		t.Fatalf("range filter wrong: %+v", only1)
	}
}

func TestReadRecentBars1mLimitsAndReturnsAscending(t *testing.T) {
	s := open(t)
	for i := int64(1); i <= 5; i++ {
		s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: i * 1000, C: float64(i)})
	}
	s.Flush()
	got, err := s.ReadRecentBars1m("US.AAPL", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].BucketMs != 3000 || got[1].BucketMs != 4000 || got[2].BucketMs != 5000 {
		t.Fatalf("recent bars = %+v, want newest three ascending", got)
	}
}

func TestReadRecentBars10sAndKnownSymbol(t *testing.T) {
	s := open(t)
	for i := int64(1); i <= 4; i++ {
		s.ArchiveBar10s(feed.Bar{Symbol: "US.NVDA", BucketMs: i * 1000, C: float64(i)})
	}
	s.Flush()
	got, err := s.ReadRecentBars10s("US.NVDA", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BucketMs != 3000 || got[1].BucketMs != 4000 {
		t.Fatalf("recent 10s bars = %+v, want newest two ascending", got)
	}
	if !s.HasArchivedSymbol("US.NVDA") || s.HasArchivedSymbol("US.UNKNOWN") {
		t.Fatal("archived-symbol lookup returned wrong result")
	}
}

func TestArchive10sUpsertAndRead(t *testing.T) {
	s := open(t)
	s.ArchiveBar10s(feed.Bar{Symbol: "US.AAPL", BucketMs: 1000, O: 10, H: 11, L: 9, C: 10.5, Volume: 100})
	s.ArchiveBar10s(feed.Bar{Symbol: "US.AAPL", BucketMs: 2000, O: 10.5, H: 12, L: 10, C: 11.8, Volume: 200})
	s.Flush()

	got, err := s.ReadBars10s("US.AAPL", 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("bars = %d, want 2", len(got))
	}
	if got[0].BucketMs != 1000 {
		t.Fatalf("ordering wrong: %+v", got)
	}
	// Re-finalize — must REPLACE, not duplicate.
	s.ArchiveBar10s(feed.Bar{Symbol: "US.AAPL", BucketMs: 1000, O: 10, H: 11.5, L: 9, C: 11, Volume: 150})
	s.Flush()
	got, err = s.ReadBars10s("US.AAPL", 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("bars = %d, want 2 after upsert", len(got))
	}
	if got[0].C != 11 || got[0].Volume != 150 {
		t.Fatalf("bucket 1000 not replaced: %+v", got[0])
	}
}

func TestPruneBars10sBefore(t *testing.T) {
	s := open(t)
	for _, b := range []feed.Bar{
		{Symbol: "US.AAPL", BucketMs: 999},
		{Symbol: "US.AAPL", BucketMs: 1000},
		{Symbol: "US.NVDA", BucketMs: 2000},
	} {
		s.ArchiveBar10s(b)
	}
	s.ArchiveBar1m(feed.Bar{Symbol: "US.AAPL", BucketMs: 500})

	rows, err := s.PruneBars10sBefore(1000)
	if err != nil || rows != 1 {
		t.Fatalf("PruneBars10sBefore = rows %d, err %v; want 1, nil", rows, err)
	}
	if got, err := s.ReadBars10s("US.AAPL", 0, 2000); err != nil || len(got) != 1 || got[0].BucketMs != 1000 {
		t.Fatalf("remaining AAPL bars = %+v, err %v", got, err)
	}
	if rows, err = s.PruneBars10sBefore(1000); err != nil || rows != 0 {
		t.Fatalf("second prune = rows %d, err %v; want 0, nil", rows, err)
	}
	if got, err := s.ReadBars1m("US.AAPL", 0, 1000); err != nil || len(got) != 1 {
		t.Fatalf("1m bars changed by 10s prune: %+v, err %v", got, err)
	}
}

func TestArchiveDailyReadAll(t *testing.T) {
	s := open(t)
	s.ArchiveDaily(feed.Bar{Symbol: "US.AAPL", BucketMs: 200, O: 1, H: 2, L: 1, C: 2, Volume: 9})
	s.ArchiveDaily(feed.Bar{Symbol: "US.AAPL", BucketMs: 100, O: 1, H: 2, L: 1, C: 2, Volume: 8})
	s.Flush()
	got, err := s.ReadDailyBars("US.AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BucketMs != 100 || got[1].BucketMs != 200 {
		t.Fatalf("daily read not ascending: %+v", got)
	}
}

func TestArchiveRangeWritesBarsAndCoverageAtomically(t *testing.T) {
	s := open(t)
	bars := []feed.Bar{
		{Symbol: "US.NFLX", BucketMs: 1100, C: 1},
		{Symbol: "US.NFLX", BucketMs: 1200, C: 2},
	}
	if err := s.ArchiveRange("US.NFLX", "1m", 1000, 2000, bars); err != nil {
		t.Fatal(err)
	}
	covered, err := s.RangeCovered("US.NFLX", "1m", 1000, 2000)
	if err != nil || !covered {
		t.Fatalf("covered=%v err=%v", covered, err)
	}
	covered, err = s.RangeCovered("US.NFLX", "1m", 900, 2000)
	if err != nil || covered {
		t.Fatalf("unexpected wider coverage=%v err=%v", covered, err)
	}
	got, err := s.ReadBars1m("US.NFLX", 0, 3000)
	if err != nil || len(got) != 2 {
		t.Fatalf("bars=%+v err=%v", got, err)
	}
	if err := s.ArchiveRange("US.IPO", "1d", 100, 200, nil); err != nil {
		t.Fatal(err)
	}
	covered, err = s.RangeCovered("US.IPO", "1d", 100, 200)
	if err != nil || !covered {
		t.Fatalf("empty range covered=%v err=%v", covered, err)
	}
}

func TestMissingRangesMergesCoverageAndFindsGaps(t *testing.T) {
	s := open(t)
	if err := s.ArchiveRange("US.GAP", "1m", 100, 199, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveRange("US.GAP", "1m", 200, 250, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveRange("US.GAP", "1m", 300, 350, nil); err != nil {
		t.Fatal(err)
	}
	gaps, err := s.MissingRanges("US.GAP", "1m", 50, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 3 || gaps[0].FromMs != 50 || gaps[0].ToMs != 99 ||
		gaps[1].FromMs != 251 || gaps[1].ToMs != 299 || gaps[2].FromMs != 351 || gaps[2].ToMs != 400 {
		t.Fatalf("gaps = %+v", gaps)
	}
}
