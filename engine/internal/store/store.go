// Package store is eTape's SQLite persistence for archived bars, config
// docs, sys_events, and execution history. Exactly one goroutine executes
// writes (batched transactions); reads use the shared *sql.DB under WAL.
package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/earlisreal/eTape/engine/internal/clock"
)

// Store owns SQLite handle and single writer goroutine.
type Store struct {
	db     *sql.DB
	clk    clock.Clock
	writes chan writeOp
	batch  int

	wg        sync.WaitGroup
	closeOnce sync.Once
	errAgg    rowErrAgg
}

type rowErrAgg struct {
	suppressed int
	lastLog    time.Time
}

const errAggWindow = time.Second

type pendingWrite struct {
	query string
	args  []any
}

type writeOp interface{ render() []pendingWrite }

type flushReq struct{ done chan struct{} }

func (flushReq) render() []pendingWrite { return nil }

// Options configures Open.
type Options struct {
	Path          string
	Clock         clock.Clock
	FlushInterval time.Duration
	BatchMax      int
}

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
	}
	s.wg.Add(1)
	go s.writer(opt.FlushInterval)
	return s, nil
}

func (s *Store) Flush() {
	done := make(chan struct{})
	s.writes <- flushReq{done: done}
	<-done
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.writes)
		s.wg.Wait()
	})
	return s.db.Close()
}

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
			if !ok {
				commit()
				s.flushErrAgg()
				return
			}
			switch v := op.(type) {
			case flushReq:
				commit()
				close(v.done)
				continue
			case execAppendOp:
				commit()
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

func (s *Store) commit(buf []pendingWrite) {
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: begin tx", "err", err, "batch", len(buf))
		return
	}
	for i := range buf {
		pw := &buf[i]
		if _, err := tx.Exec(pw.query, pw.args...); err != nil {
			s.logRowErr(err, pw.query)
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("store: commit", "err", err, "batch", len(buf))
		_ = tx.Rollback()
	}
}

func (s *Store) logRowErr(err error, query string) {
	now := s.clk.Now()
	if s.errAgg.lastLog.IsZero() || now.Sub(s.errAgg.lastLog) >= errAggWindow {
		s.flushErrAgg()
		slog.Error("store: exec", "err", err, "query", query)
		s.errAgg.lastLog = now
		return
	}
	s.errAgg.suppressed++
}

func (s *Store) flushErrAgg() {
	if s.errAgg.suppressed > 0 {
		slog.Error("store: exec (aggregated)", "suppressed", s.errAgg.suppressed)
		s.errAgg.suppressed = 0
	}
}
