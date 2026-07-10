// Package store is eTape's SQLite persistence: the always-on feed journal,
// 1m/daily bar archives, config docs, and sys_events. Exactly one goroutine
// executes writes (batched transactions); reads use the shared *sql.DB under
// WAL. It imports domain types (feed, session, clock) for serialization but
// never md/opend/uihub.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite" // driver name "sqlite"; also gives us *sqlite.Error for collision detection
	sqlite3 "modernc.org/sqlite/lib"

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

	daySeq         map[string]int64 // per-day next-seq cache; writer goroutine ONLY
	dayCollisioned map[string]bool  // per-day: has commit() already logged this day's first (day,seq) collision? writer goroutine ONLY
	errAgg         rowErrAgg        // coalesces repeated non-collision exec-error logging; writer goroutine ONLY
}

// rowErrAgg coalesces repeated row-exec-error logging within a rolling
// window so a burst of failures can't flood the log — and, by extension,
// can't stall the writer goroutine long enough to back up the (blocking)
// RecordEvent channel. That stall is what turned a handful of duplicate-PK
// rows into a feed-wide lag in the field; see commit()/logRowErr.
type rowErrAgg struct {
	suppressed int
	lastLog    time.Time
}

const errAggWindow = time.Second

type pendingWrite struct {
	query string
	args  []any
	// journalDay is non-empty for a journal insert whose args[1] (seq) is a
	// placeholder — commit() assigns/retries it, not render(), so a
	// (day,seq) collision can be corrected in place instead of failing.
	journalDay string
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
		db:             db,
		clk:            opt.Clock,
		writes:         make(chan writeOp, 4096),
		batch:          opt.BatchMax,
		daySeq:         make(map[string]int64),
		dayCollisioned: make(map[string]bool),
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
				s.flushErrAgg()
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

// commit applies a batch in one transaction; on a begin/commit failure it
// logs loudly and drops the whole batch (honesty policy: journal degrades,
// market data never blocks). Per-row exec failures are handled by
// execJournalRow (journal inserts, which can self-heal a (day,seq) collision)
// or logRowErr (everything else) — deliberately never a per-row synchronous
// log, which is what let a burst of collisions stall this goroutine long
// enough to back up the blocking RecordEvent channel in the field.
func (s *Store) commit(buf []pendingWrite) {
	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("store: begin tx", "err", err, "batch", len(buf))
		return
	}
	for i := range buf {
		pw := &buf[i]
		if pw.journalDay != "" {
			s.execJournalRow(tx, pw)
			continue
		}
		if _, err := tx.Exec(pw.query, pw.args...); err != nil {
			s.logRowErr(err, pw.query)
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("store: commit", "err", err, "batch", len(buf))
		_ = tx.Rollback()
	}
}

// execJournalRow assigns pw's seq (from the per-day cache, seeding from the
// DB max on first use of the day) and executes the insert. On a (day,seq)
// primary-key collision — a poisoned/stale counter, or a second process
// sharing this DB — it reseeds the day from a fresh DB max and retries once:
// bounded, so a persistent collision drops the row instead of spinning or
// flooding the log. A dropped row never advances daySeq, so it burns no seq.
func (s *Store) execJournalRow(tx *sql.Tx, pw *pendingWrite) {
	day := pw.journalDay
	seq, err := s.assignSeq(day)
	if err != nil {
		s.dropped.Add(1)
		s.logRowErr(err, "store: assignSeq "+day)
		return
	}
	pw.args[1] = seq
	_, err = tx.Exec(pw.query, pw.args...)
	if err == nil {
		return
	}
	if !isPKCollision(err) {
		s.logRowErr(err, pw.query)
		return
	}
	newSeq, err := s.reseedDay(day)
	if err != nil {
		s.dropped.Add(1)
		s.logRowErr(err, "store: reseedDay "+day)
		return
	}
	s.logCollisionOnce(day, seq, newSeq-1)
	pw.args[1] = newSeq
	if _, err := tx.Exec(pw.query, pw.args...); err != nil {
		s.dropped.Add(1) // still colliding (e.g. a live second writer) -- give up on this row
	}
}

// isPKCollision reports whether err is SQLite's extended result code for a
// PRIMARY KEY constraint violation (1555) -- the (day,seq) collision case
// execJournalRow recovers from via reseedDay, rather than logging and dropping.
func isPKCollision(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

// logCollisionOnce logs the first (day,seq) collision seen for day in this
// process -- a diagnostic, not a per-row log, so a flood of collisions costs
// one log line total. attemptedSeq is what assignSeq had minted; dbMax is the
// fresh MAX(seq) the reseed found. attemptedSeq far below dbMax means the
// counter was poisoned back toward zero; attemptedSeq close to dbMax means a
// second writer is interleaving inserts against this DB.
func (s *Store) logCollisionOnce(day string, attemptedSeq, dbMax int64) {
	if s.dayCollisioned[day] {
		return
	}
	s.dayCollisioned[day] = true
	slog.Warn("store: journal seq collision (self-healed)",
		"day", day, "attemptedSeq", attemptedSeq, "dbMax", dbMax, "reseededTo", dbMax+1)
}

// logRowErr logs a non-collision row exec error, coalescing repeats within
// errAggWindow into one aggregate line instead of one log record per row.
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

// flushErrAgg emits the suppressed-row-error count accumulated since the
// last logRowErr flush, if any. Called before starting a new aggregation
// window and on writer shutdown, so a trailing count is never lost.
func (s *Store) flushErrAgg() {
	if s.errAgg.suppressed > 0 {
		slog.Error("store: exec (aggregated)", "suppressed", s.errAgg.suppressed)
		s.errAgg.suppressed = 0
	}
}
