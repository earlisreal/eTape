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
	s.AppendSysEvent("retention", fmt.Sprintf("pruned %d journal rows before %s (retention %dd)", n, cutoffDay, retentionDays))
	return n, nil
}
