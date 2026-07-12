package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/demojournal"
	"github.com/earlisreal/eTape/engine/internal/store"
)

func TestRunVacuumModeHappyPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "etape.db")
	// demojournal.Generate uses the real wall clock to open its own Store and
	// writes raw (unsealed) rows for the given day, then closes — so today's
	// real ET date is always strictly after "2026-07-08", and runVacuumMode's
	// own real-clock SealJournalDays call below will seal it deterministically
	// regardless of when this test runs.
	if err := demojournal.Generate(dbPath, "2026-07-08"); err != nil {
		t.Fatalf("generate demo journal: %v", err)
	}

	cfg := config.Default()
	cfg.Store.RetentionDays = 30
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// SealJournalDays logs via the package-level slog default (see
	// store/seal.go), not the *slog.Logger passed into runVacuumMode -- swap
	// the default too, same convention as store's own journal_test.go/
	// store_test.go, so the seal's "sealed day" line doesn't leak to the test
	// binary's stderr.
	prevDefault := slog.Default()
	slog.SetDefault(log)
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	if code := runVacuumMode(dbPath, cfg, log); code != 0 {
		t.Fatalf("exit=%d want 0", code)
	}

	// After the run the day is sealed: no raw rows, chunks present.
	st2, err := store.Open(store.Options{Path: dbPath, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	_, rawRows, err := st2.JournalFootprint()
	if err != nil {
		t.Fatal(err)
	}
	if rawRows != 0 {
		t.Fatalf("expected sealed (0 raw rows), got %d", rawRows)
	}
}
