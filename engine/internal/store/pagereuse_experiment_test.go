//go:build pagereuse

package store

// Page-reuse plateau experiment — the pre-code gate for the 2026-07-12
// journal vacuum boot-path revision design (§10.1). NOT a regression test:
// it copies the real production DB and takes ~20-40 minutes.
//
//   go test -tags pagereuse -run TestPageReusePlateau ./internal/store -v -timeout 90m
//
// Hypothesis under test: with no VACUUM, the freelist pages freed by sealing
// day N are reused by day N+1's raw writes, so the file plateaus at its
// high-water mark instead of growing ~2 GB/day.
// Pass criterion: page_count after each cycle's write phase stays within
// 1.1x of the cycle-1 high-water mark.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

const pageReuseWorkDir = "/tmp/etape-pagereuse"

func TestPageReusePlateau(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(home, ".eTape", "etape.db")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("prod DB not found: %v", err)
	}

	if err := os.RemoveAll(pageReuseWorkDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pageReuseWorkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(pageReuseWorkDir, "etape.db")
	start := time.Now()
	if err := copyFileExp(src, dbPath); err != nil {
		t.Fatal(err)
	}
	t.Logf("copied prod DB (%s)", time.Since(start).Round(time.Second))

	et, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Prod DB holds raw days 2026-07-06..07-10; start "today" just after them.
	fake := clock.NewFake(time.Date(2026, 7, 11, 12, 0, 0, 0, et))
	s, err := Open(Options{Path: dbPath, Clock: fake})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	stats := func(label string) (pageCount, freeCount int64) {
		if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			t.Fatalf("%s: checkpoint: %v", label, err)
		}
		if err := s.db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
			t.Fatalf("%s: page_count: %v", label, err)
		}
		if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&freeCount); err != nil {
			t.Fatalf("%s: freelist_count: %v", label, err)
		}
		t.Logf("%-28s file %6.2f GB  free %6.2f GB (%4.1f%%)",
			label, gbExp(pageCount), gbExp(freeCount), 100*float64(freeCount)/float64(pageCount))
		return pageCount, freeCount
	}

	// Stash a realistic hot day's rows as the synthetic-write source BEFORE
	// sealing deletes them. 2026-07-09 is the biggest day (~1.94 GB, 2.3M rows).
	start = time.Now()
	if _, err := s.db.Exec(`CREATE TABLE raw_src AS
		SELECT seq, ts_exch, ts_recv, symbol, kind, seed, payload
		FROM journal WHERE day='2026-07-09'`); err != nil {
		t.Fatal(err)
	}
	var srcRows int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM raw_src").Scan(&srcRows); err != nil {
		t.Fatal(err)
	}
	t.Logf("stashed raw_src: %d rows (%s)", srcRows, time.Since(start).Round(time.Second))
	stats("baseline (+raw_src)")

	bootMaintenance := func(label string) {
		if _, err := s.PruneJournal(30); err != nil {
			t.Fatalf("%s: prune: %v", label, err)
		}
		s.Flush()
		start := time.Now()
		sum, err := s.SealJournalDays()
		if err != nil {
			t.Fatalf("%s: seal: %v", label, err)
		}
		s.Flush()
		t.Logf("%s: sealed %d day(s), %d rows, %d MB -> %d MB (%s)", label,
			sum.Days, sum.Rows, sum.BytesBefore>>20, sum.BytesAfter>>20,
			time.Since(start).Round(time.Second))
	}

	// Boot 0: mass-seal the 5 pre-existing raw days (the deploy-day scenario).
	bootMaintenance("boot 0 (mass seal)")
	stats("after mass seal")

	const cycles = 5
	const batchRows = 50_000
	var highWater int64
	for i := 1; i <= cycles; i++ {
		day := fake.Now().In(et).Format("2006-01-02")
		start := time.Now()
		for lo := int64(1); lo <= srcRows; lo += batchRows {
			if _, err := s.db.Exec(`INSERT INTO journal
				SELECT ?, seq, ts_exch, ts_recv, symbol, kind, seed, payload
				FROM raw_src WHERE rowid BETWEEN ? AND ?`,
				day, lo, lo+batchRows-1); err != nil {
				t.Fatalf("cycle %d: insert: %v", i, err)
			}
		}
		t.Logf("cycle %d: wrote day %s, %d rows (%s)", i, day, srcRows,
			time.Since(start).Round(time.Second))
		pc, _ := stats(fmt.Sprintf("cycle %d after write", i))
		if i == 1 {
			highWater = pc
		} else if float64(pc) > 1.1*float64(highWater) {
			t.Errorf("cycle %d: page_count %.2f GB exceeds 1.1x cycle-1 high-water %.2f GB — page reuse NOT holding",
				i, gbExp(pc), gbExp(highWater))
		}

		fake.Advance(24 * time.Hour)
		bootMaintenance(fmt.Sprintf("boot %d", i))
		stats(fmt.Sprintf("cycle %d after seal", i))
	}

	if !t.Failed() {
		t.Logf("PASS: file plateaued — high-water %.2f GB held within 1.1x across %d cycles", gbExp(highWater), cycles)
		s.Close()
		_ = os.RemoveAll(pageReuseWorkDir)
	} else {
		t.Logf("FAILED: work dir kept for inspection: %s", pageReuseWorkDir)
	}
}

func gbExp(pages int64) float64 { return float64(pages) * 4096 / 1e9 }

func copyFileExp(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
