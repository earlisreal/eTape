package store

import (
	"strings"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

func openAtClock(t *testing.T, now time.Time) *Store {
	t.Helper()
	s, err := Open(Options{Path: t.TempDir() + "/test.db", Clock: clock.NewFake(now), FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedWasteRows(t *testing.T, s *Store, n int) {
	t.Helper()
	detail := strings.Repeat("x", 2048)
	for i := 0; i < n; i++ {
		if _, err := s.db.Exec("INSERT INTO sys_events(ts, kind, detail) VALUES (?, ?, ?)", i, "waste", detail); err != nil {
			t.Fatalf("seedWasteRows: %v", err)
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
	vacuumBackstopFloor = 6 << 20
	vacuumAdviseFreeBytes = 4 << 20

	below := SizeStats{PageSize: 1 << 20, PageCount: 8, FreelistPages: 5}
	if below.NeedsBackstopVacuum() {
		t.Fatal("should not trip backstop below floor")
	}
	above := SizeStats{PageSize: 1 << 20, PageCount: 8, FreelistPages: 7}
	if !above.NeedsBackstopVacuum() {
		t.Fatal("should trip backstop above floor")
	}
	half := SizeStats{PageSize: 1 << 20, PageCount: 20, FreelistPages: 9}
	if half.NeedsBackstopVacuum() {
		t.Fatal("half-file rule should dominate the floor here")
	}
	if !above.AdviseVacuum() {
		t.Fatal("7 MiB free should advise")
	}
	if !below.AdviseVacuum() {
		t.Fatal("5 MiB free should advise")
	}
}

func TestVacuumReclaimsFreePages(t *testing.T) {
	st := openAtClock(t, clock.System{}.Now())
	seedWasteRows(t, st, 5000)
	st.Flush()
	if _, err := st.db.Exec("DELETE FROM sys_events WHERE kind='waste'"); err != nil {
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
