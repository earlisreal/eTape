package store

import (
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

// PruneJournal deletes journal rows older than retentionDays trading days
// (bar archives are kept forever). retentionDays <= 0 is a no-op. The cutoff
// day is inclusive-keep: rows on or after it are retained.
//
// This is the one sanctioned exception to the single-writer-goroutine
// pattern: it calls s.db.Exec directly instead of routing through s.writes.
// It is a boot-time-only maintenance op that runs before the high-frequency
// journal stream starts (i.e. before any RecordEvent calls begin), so it
// cannot race the writer goroutine. Do not convert this to the async pattern.
func (s *Store) PruneJournal(retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoffMs := s.clk.Now().AddDate(0, 0, -retentionDays).UnixMilli()
	cutoffDay := time.UnixMilli(session.DayMs(cutoffMs)).In(session.Loc()).Format("2006-01-02")
	res, err := s.db.Exec("DELETE FROM journal WHERE day < ?", cutoffDay)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	cres, err := s.db.Exec("DELETE FROM journal_chunks WHERE day < ?", cutoffDay)
	if err != nil {
		return 0, err
	}
	nc, _ := cres.RowsAffected()
	s.AppendSysEvent("retention", fmt.Sprintf(
		"pruned %d journal rows + %d sealed chunks before %s (retention %dd)", n, nc, cutoffDay, retentionDays))
	return n, nil
}

// vacuumFreelistThreshold: reclaim disk when free pages exceed ~64 MB. Prune
// and seal delete large row spans but SQLite keeps the freed pages in the file;
// VACUUM is the only thing that returns them to the OS.
const vacuumFreelistThreshold = 64 << 20

// VacuumIfNeeded runs VACUUM when the freelist exceeds vacuumFreelistThreshold,
// reporting whether it ran. Like PruneJournal, it touches s.db directly and is
// a boot-time-only maintenance op: call it before the feed producer starts and
// after Flush() has drained queued writes, so no writer transaction races the
// VACUUM (which needs exclusive access).
func (s *Store) VacuumIfNeeded() (bool, error) {
	var freeCount, pageSize int64
	if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&freeCount); err != nil {
		return false, err
	}
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return false, err
	}
	if freeCount*pageSize <= vacuumFreelistThreshold {
		return false, nil
	}
	if _, err := s.db.Exec("VACUUM"); err != nil {
		return false, err
	}
	return true, nil
}

// Thresholds for the boot-path maintenance decisions. Package vars (not consts)
// so tests can shrink them; deliberately NOT config keys — see the design spec.
var (
	vacuumAdviseFreeBytes int64 = 4 << 30 // post-maintenance free above this → advisory hint
	vacuumBackstopFloor   int64 = 6 << 30 // pre-maintenance free above max(floor, file/2) → backstop
)

// vacuumBackstopThreshold is the pre-maintenance free-byte level above which the
// boot path runs an anomaly-backstop VACUUM: max(floor, half the file). A normal
// day's ~2.2 GB of seal-freed pages appears only AFTER this boot's prune/seal, so
// the pre-maintenance freelist is ≈ 0 in every normal scenario and can never trip
// this — only genuine cross-day reuse failure accumulates here.
func vacuumBackstopThreshold(fileBytes int64) int64 {
	if h := fileBytes / 2; h > vacuumBackstopFloor {
		return h
	}
	return vacuumBackstopFloor
}

// SizeStats is the DB's physical size profile (PRAGMA page_size/page_count/
// freelist_count).
type SizeStats struct{ PageSize, PageCount, FreelistPages int64 }

func (st SizeStats) FileBytes() int64 { return st.PageSize * st.PageCount }
func (st SizeStats) FreeBytes() int64 { return st.PageSize * st.FreelistPages }

// NeedsBackstopVacuum reports whether PRE-maintenance free space indicates
// cross-day page-reuse failure (anomalous bloat). Pass the pre-prune snapshot.
func (st SizeStats) NeedsBackstopVacuum() bool {
	return st.FreeBytes() > vacuumBackstopThreshold(st.FileBytes())
}

// AdviseVacuum reports whether POST-maintenance free space is high enough to
// suggest a manual `etape -vacuum` (advisory only; reabsorbed by daily churn
// otherwise). Pass the post-seal snapshot.
func (st SizeStats) AdviseVacuum() bool { return st.FreeBytes() > vacuumAdviseFreeBytes }

// SizeStats reads the current physical size profile via three PRAGMAs.
func (s *Store) SizeStats() (SizeStats, error) {
	var st SizeStats
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&st.PageSize); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&st.PageCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRow("PRAGMA freelist_count").Scan(&st.FreelistPages); err != nil {
		return st, err
	}
	return st, nil
}

// Vacuum runs an unconditional VACUUM. Boot-time-only / no-live-producer
// contract, identical to PruneJournal: call it before the feed producer starts
// and after Flush() has drained queued writes (VACUUM needs exclusive access).
func (s *Store) Vacuum() error {
	_, err := s.db.Exec("VACUUM")
	return err
}

// JournalFootprint returns the sealed-chunk byte total and the raw (unsealed)
// journal row count — the two numbers the per-boot storage telemetry reports.
func (s *Store) JournalFootprint() (chunkBytes, rawRows int64, err error) {
	if err = s.db.QueryRow("SELECT COALESCE(SUM(LENGTH(body)),0) FROM journal_chunks").Scan(&chunkBytes); err != nil {
		return
	}
	err = s.db.QueryRow("SELECT COUNT(*) FROM journal").Scan(&rawRows)
	return
}

// PendingSealDays returns exactly the days SealJournalDays would compress on this
// boot (distinct raw days strictly older than the current ET day). Used by the
// boot path to size the "preparing journal" banner before the blocking seal.
// Reuses the same day boundary as SealJournalDays (dayKey + daysToSeal) so the
// count can never disagree with what the seal actually does.
func (s *Store) PendingSealDays() ([]string, error) {
	return s.daysToSeal(dayKey(s.clk.Now().UnixMilli()))
}

// humanBytes renders a byte count as a one-decimal GB (or whole MB below 1 GB)
// string. Uses DECIMAL units (÷1e9 / ÷1e6) to match the spec's reported figures
// and the page-reuse experiment's gbExp helper (pages*4096/1e9), not binary GiB.
func humanBytes(n int64) string {
	const gb = 1_000_000_000
	if n >= gb {
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	}
	return fmt.Sprintf("%d MB", n/1_000_000)
}

// FormatStorageReport builds the per-boot `storage` sys_event detail. When
// advise is true it appends a hint to run `etape -vacuum`, estimating the
// reabsorption horizon from a nominal raw-day size (a display estimate, not a
// tunable threshold).
func FormatStorageReport(st SizeStats, chunkBytes, rawRows int64, advise bool) string {
	file := st.FileBytes()
	free := st.FreeBytes()
	pct := 0
	if file > 0 {
		pct = int(free * 100 / file)
	}
	rep := fmt.Sprintf("file %s, free %s (%d%%), journal_chunks ~%s, raw rows %d",
		humanBytes(file), humanBytes(free), pct, humanBytes(chunkBytes), rawRows)
	if advise {
		const rawDayBytesEstimate = 2 << 30 // ~one trading day of raw feed
		days := free / rawDayBytesEstimate
		if days < 1 {
			days = 1
		}
		rep += fmt.Sprintf(" — consider `etape -vacuum` to reclaim %s now (otherwise reabsorbed over ~%d days)",
			humanBytes(free), days)
	}
	return rep
}
