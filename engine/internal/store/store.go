// Package store is eTape's SQLite persistence: the always-on feed journal,
// 1m/daily bar archives, config docs, and sys_events. Exactly one goroutine
// executes writes (batched transactions); reads use the shared *sql.DB under
// WAL. It imports domain types (feed, session, clock) for serialization but
// never md/opend/uihub.
package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // driver name "sqlite"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// Store owns the SQLite handle and the single writer goroutine.
type Store struct {
	db     *sql.DB
	clk    clock.Clock
	writes chan writeOp
	batch  int

	wg        sync.WaitGroup
	closeOnce sync.Once // Close is idempotent (tests Close explicitly AND via t.Cleanup)
	dropped   atomic.Uint64

	daySeq map[string]int64 // per-day next-seq cache; writer goroutine ONLY (Task 3)
}

type pendingWrite struct {
	query string
	args  []any
}

// writeOp is one queued mutation; render (called in the writer goroutine only)
// turns it into SQL statements.
type writeOp interface{ render() []pendingWrite }

// flushReq is a synchronous barrier; it renders nothing and signals done after
// the buffer is committed.
type flushReq struct{ done chan struct{} }

func (flushReq) render() []pendingWrite { return nil }

// Options configures Open.
type Options struct {
	Path          string
	Clock         clock.Clock
	FlushInterval time.Duration
	BatchMax      int
}

// Open opens (creating if absent) the SQLite DB, applies WAL pragmas, migrates
// the schema, and starts the writer goroutine.
func Open(opt Options) (*Store, error) {
	if opt.Clock == nil {
		opt.Clock = clock.System{}
	}
	if opt.FlushInterval <= 0 {
		opt.FlushInterval = 250 * time.Millisecond
	}
	if opt.BatchMax <= 0 {
		opt.BatchMax = 512
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", opt.Path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", opt.Path, err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	s := &Store{
		db:     db,
		clk:    opt.Clock,
		writes: make(chan writeOp, 4096),
		batch:  opt.BatchMax,
		daySeq: make(map[string]int64),
	}
	s.wg.Add(1)
	go s.writer(opt.FlushInterval)
	return s, nil
}

// Flush blocks until every write queued before the call is committed.
func (s *Store) Flush() {
	done := make(chan struct{})
	s.writes <- flushReq{done: done}
	<-done
}

// Close stops the writer (final-flushing buffered writes) and closes the DB.
// Idempotent: safe to call more than once (e.g. an explicit Close plus a
// t.Cleanup). Callers must ensure no producer still calls RecordEvent/Archive*/
// SetConfig/AppendSysEvent after Close begins — a send on the closed channel
// panics (cmd/etape joins its feed pipe before Close; see Task 10).
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.writes)
		s.wg.Wait()
	})
	return s.db.Close()
}

// writer is the single write goroutine: batch until the flush ticker fires,
// the batch cap is hit, or a barrier arrives; commit in one transaction.
func (s *Store) writer(flush time.Duration) {
	defer s.wg.Done()
	ticker := s.clk.NewTicker(flush)
	defer ticker.Stop()
	var buf []pendingWrite
	commit := func() {
		if len(buf) == 0 {
			return
		}
		s.commit(buf)
		buf = buf[:0]
	}
	for {
		select {
		case op, ok := <-s.writes:
			if !ok { // channel closed by Close: final flush, then exit
				commit()
				return
			}
			switch v := op.(type) {
			case flushReq:
				commit()
				close(v.done)
				continue
			case execAppendOp:
				commit() // flush any pending batch first, then the sync exec tx
				v.done <- s.commitExecAppend(v)
				continue
			}
			buf = append(buf, op.render()...)
			if len(buf) >= s.batch {
				commit()
			}
		case <-ticker.C():
			commit()
		}
	}
}

// commit applies a batch in one transaction; on failure it logs loudly and
// drops the batch (honesty policy: journal degrades, market data never blocks).
func (s *Store) commit(buf []pendingWrite) {
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: begin tx", "err", err, "batch", len(buf))
		return
	}
	for _, pw := range buf {
		if _, err := tx.Exec(pw.query, pw.args...); err != nil {
			slog.Error("store: exec", "err", err, "query", pw.query)
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("store: commit", "err", err, "batch", len(buf))
		_ = tx.Rollback()
	}
}
