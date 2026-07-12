package main

import (
	"log/slog"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/store"
)

// runVacuumMode is the `etape -vacuum` one-shot maintenance mode: no uihub, no
// browser, no feed. It runs the exact boot-maintenance sequence (prune → seal →
// vacuum), so it also opportunistically compresses anything pending, then exits.
// The caller must already hold the single-instance lock (so no live engine can
// race the VACUUM). Returns a process exit code.
func runVacuumMode(dbPath string, cfg config.Config, log *slog.Logger) int {
	st, err := store.Open(store.Options{
		Path: dbPath, Clock: clock.System{},
		FlushInterval: time.Duration(cfg.Store.FlushMs) * time.Millisecond,
	})
	if err != nil {
		log.Error("vacuum: open store", "err", err)
		return 1
	}
	defer st.Close()

	before, _ := st.SizeStats()
	log.Info("vacuum: starting", "fileMB", before.FileBytes()>>20, "freeMB", before.FreeBytes()>>20)

	if n, err := st.PruneJournal(cfg.Store.RetentionDays); err != nil {
		log.Error("vacuum: prune", "err", err)
		return 1
	} else if n > 0 {
		log.Info("vacuum: pruned", "rows", n)
	}
	st.Flush()
	if sum, err := st.SealJournalDays(); err != nil {
		log.Error("vacuum: seal", "err", err)
		return 1
	} else if sum.Days > 0 || sum.Failed > 0 {
		log.Info("vacuum: sealed", "days", sum.Days, "rows", sum.Rows, "failed", sum.Failed)
	}
	st.Flush()
	if ran, err := st.VacuumIfNeeded(); err != nil {
		log.Error("vacuum: VACUUM", "err", err)
		return 1
	} else if !ran {
		log.Info("vacuum: nothing to reclaim", "freeMB", before.FreeBytes()>>20, "thresholdMB", 64)
	}

	after, _ := st.SizeStats()
	log.Info("vacuum: done", "fileMB", after.FileBytes()>>20, "freeMB", after.FreeBytes()>>20,
		"reclaimedMB", (before.FileBytes()-after.FileBytes())>>20)
	return 0
}
