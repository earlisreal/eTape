package store

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
)

func TestPruneJournalByDay(t *testing.T) {
	// Clock "now" = 2026-07-06 12:00 ET. Retention 2 days keeps 07-05, 07-06;
	// drops 07-01. Archives untouched.
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, mustLoc(t))
	s, err := Open(Options{Path: t.TempDir() + "/r.db", Clock: clock.NewFake(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	day := func(y, m, d int) int64 {
		return time.Date(y, time.Month(m), d, 10, 0, 0, 0, mustLoc(t)).UnixMilli()
	}
	// Comments on their own lines so gofmt has no trailing-comment block to align.
	s.RecordEvent(feed.ConnUpEvent{}, day(2026, 7, 1)) // old: pruned
	s.RecordEvent(feed.ConnUpEvent{}, day(2026, 7, 5)) // kept
	s.RecordEvent(feed.ConnUpEvent{}, day(2026, 7, 6)) // kept
	// Bar archives are never pruned.
	s.ArchiveDaily(feed.Bar{Symbol: "US.AAPL", BucketMs: day(2026, 1, 1), C: 1})
	s.Flush()

	deleted, err := s.PruneJournal(2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	days, _ := s.JournalDays()
	if len(days) != 2 || days[0] != "2026-07-05" || days[1] != "2026-07-06" {
		t.Fatalf("remaining days = %v, want [2026-07-05 2026-07-06]", days)
	}
	daily, _ := s.ReadDailyBars("US.AAPL")
	if len(daily) != 1 {
		t.Fatalf("archive pruned! got %d daily bars, want 1", len(daily))
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
