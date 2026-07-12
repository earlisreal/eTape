package store

import (
	"strings"
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

// seedRawDay inserts n raw journal rows for day directly via journalInsertSQL
// (same pattern seal_test.go uses), bypassing RecordEvent's seq-cache bookkeeping
// since these tests only need rows to exist, not to be recorded live.
func seedRawDay(t *testing.T, s *Store, day string, n int) {
	t.Helper()
	payload := strings.Repeat("x", 256) // large enough that bulk delete produces a real freelist
	for i := 0; i < n; i++ {
		if _, err := s.db.Exec(journalInsertSQL, day, i+1, recvBase+int64(i), recvBase+int64(i),
			"AAPL", "tick", 0, payload); err != nil {
			t.Fatalf("seedRawDay: %v", err)
		}
	}
}

func TestSizeStatsBytes(t *testing.T) {
	s := SizeStats{PageSize: 4096, PageCount: 100, FreelistPages: 10}
	if got := s.FileBytes(); got != 4096*100 {
		t.Fatalf("FileBytes=%d", got)
	}
	if got := s.FreeBytes(); got != 4096*10 {
		t.Fatalf("FreeBytes=%d", got)
	}
}

func TestBackstopAndAdvisePredicates(t *testing.T) {
	orig1, orig2 := vacuumBackstopFloor, vacuumAdviseFreeBytes
	t.Cleanup(func() { vacuumBackstopFloor, vacuumAdviseFreeBytes = orig1, orig2 })
	vacuumBackstopFloor = 6 << 20 // 6 MiB
	vacuumAdviseFreeBytes = 4 << 20

	// file 8 MiB, free 5 MiB: threshold = max(6MiB, 4MiB) = 6MiB → below → no backstop.
	below := SizeStats{PageSize: 1 << 20, PageCount: 8, FreelistPages: 5}
	if below.NeedsBackstopVacuum() {
		t.Fatal("should not trip backstop below floor")
	}
	// file 8 MiB, free 7 MiB: 7 > 6 → backstop.
	above := SizeStats{PageSize: 1 << 20, PageCount: 8, FreelistPages: 7}
	if !above.NeedsBackstopVacuum() {
		t.Fatal("should trip backstop above floor")
	}
	// file 20 MiB, free 9 MiB: threshold = max(6, 10) = 10MiB → below.
	half := SizeStats{PageSize: 1 << 20, PageCount: 20, FreelistPages: 9}
	if half.NeedsBackstopVacuum() {
		t.Fatal("half-file rule should dominate the floor here")
	}
	if !above.AdviseVacuum() { // 7 MiB free > 4 MiB advise threshold
		t.Fatal("7 MiB free should advise")
	}
	if !below.AdviseVacuum() { // 5 MiB free > 4 MiB advise threshold (below the 6 MiB BACKSTOP, still above ADVISE)
		t.Fatal("5 MiB free should advise")
	}
}

func TestVacuumReclaimsFreePages(t *testing.T) {
	st := openAtClock(t, time.Date(2026, 7, 11, 12, 0, 0, 0, mustLoc(t)))
	seedRawDay(t, st, "2026-07-08", 5000)
	st.Flush()
	if _, err := st.db.Exec("DELETE FROM journal WHERE day='2026-07-08'"); err != nil {
		t.Fatal(err)
	}
	pre, _ := st.SizeStats()
	if pre.FreeBytes() == 0 {
		t.Skip("no freelist accumulated; page churn too small on this platform")
	}
	if err := st.Vacuum(); err != nil {
		t.Fatal(err)
	}
	post, _ := st.SizeStats()
	if post.FreeBytes() >= pre.FreeBytes() {
		t.Fatalf("vacuum did not reclaim: pre=%d post=%d", pre.FreeBytes(), post.FreeBytes())
	}
}

func TestPendingSealDays(t *testing.T) {
	st := openAtClock(t, time.Date(2026, 7, 11, 12, 0, 0, 0, mustLoc(t)))
	seedRawDay(t, st, "2026-07-08", 10)
	seedRawDay(t, st, "2026-07-09", 10)
	seedRawDay(t, st, "2026-07-11", 10) // today (ET) — must be excluded
	st.Flush()
	days, err := st.PendingSealDays()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2026-07-08" || days[1] != "2026-07-09" {
		t.Fatalf("pending=%v want [07-08 07-09]", days)
	}
}
