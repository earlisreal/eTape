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
