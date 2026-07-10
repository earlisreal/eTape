package store

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// countHandler is a slog.Handler that only counts records, for asserting a
// burst of row errors was coalesced rather than logged one-per-row.
type countHandler struct {
	mu    sync.Mutex
	count int
}

func (h *countHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countHandler) Handle(context.Context, slog.Record) error {
	h.mu.Lock()
	h.count++
	h.mu.Unlock()
	return nil
}
func (h *countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countHandler) WithGroup(string) slog.Handler      { return h }
func (h *countHandler) n() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

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

// TestCommitCoalescesExecErrors is the regression for the field incident: a
// burst of identical row-exec failures in one batch must not produce one log
// record per row. Unbounded per-row synchronous logging is what stalled the
// writer goroutine long enough to back up the (blocking) RecordEvent channel
// and lag the live feed — see commit()/logRowErr in store.go. This drives
// non-journal (journalDay=="") rows so it exercises logRowErr's aggregation
// path directly, independent of the journal-specific collision/reseed path.
func TestCommitCoalescesExecErrors(t *testing.T) {
	s := open(t)
	h := &countHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const n = 50
	buf := make([]pendingWrite, n)
	for i := range buf {
		buf[i] = pendingWrite{query: "INSERT INTO no_such_table (x) VALUES (?)", args: []any{i}}
	}
	s.commit(buf)
	s.flushErrAgg()

	if got := h.n(); got == 0 {
		t.Fatal("expected at least one error log for a batch of failing rows")
	} else if got >= n {
		t.Fatalf("logged %d records for %d identical row errors, want far fewer (coalesced)", got, n)
	}
}
