package store

import (
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// open makes a temp-file store with a fast flush for tests.
func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{
		Path:          t.TempDir() + "/test.db",
		Clock:         clock.NewFake(time.UnixMilli(0)),
		FlushInterval: time.Hour, // tests drive flushing explicitly via Flush()
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesSchemaAndWAL(t *testing.T) {
	s := open(t)
	// All five tables exist.
	for _, tbl := range []string{"journal", "bars_1m", "bars_daily", "config", "sys_events"} {
		var name string
		row := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl)
		if err := row.Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", tbl, err)
		}
	}
	// WAL is on.
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestFlushAndCloseAreSafeWhenEmpty(t *testing.T) {
	s := open(t)
	s.Flush() // no queued writes — must not hang
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
