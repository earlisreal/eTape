package store

import (
	"database/sql"
	"errors"
	"log/slog"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

const journalInsertSQL = `INSERT INTO journal
	(day, seq, ts_exch, ts_recv, symbol, kind, seed, payload)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

// RecordEvent journals one feed event. Blocking by design: the journal is the
// practice-data asset, so back-pressure surfaces upstream rather than dropping.
// recvMs is the pipeline receive time (ms) — it sets the day partition and the
// ts_exch fallback for events without an exchange timestamp.
func (s *Store) RecordEvent(ev feed.Event, recvMs int64) {
	s.writes <- recordOp{s: s, ev: ev, recvMs: recvMs}
}

// DroppedJournalRows counts rows dropped because their payload failed to encode.
func (s *Store) DroppedJournalRows() uint64 { return s.dropped.Load() }

type recordOp struct {
	s      *Store
	ev     feed.Event
	recvMs int64
}

// render runs in the writer goroutine only, so touching s.daySeq is race-free.
func (o recordOp) render() []pendingWrite {
	payload, err := encodePayload(o.ev)
	if err != nil {
		o.s.dropped.Add(1)
		slog.Error("store: journal encode failed, row dropped", "err", err, "kind", eventKind(o.ev))
		return nil
	}
	day := dayKey(o.recvMs)
	seq := o.s.nextSeq(day)
	seed := 0
	if eventSeed(o.ev) {
		seed = 1
	}
	return []pendingWrite{{
		query: journalInsertSQL,
		args: []any{day, seq, eventExchTs(o.ev, o.recvMs), o.recvMs,
			eventSymbol(o.ev), eventKind(o.ev), seed, string(payload)},
	}}
}

// nextSeq returns the next per-day seq, seeding from the DB max on first use of
// a day (so restarts mid-day continue rather than collide). Writer goroutine only.
func (s *Store) nextSeq(day string) int64 {
	seq, ok := s.daySeq[day]
	if !ok {
		seq = s.maxSeq(day)
	}
	seq++
	s.daySeq[day] = seq
	return seq
}

func (s *Store) maxSeq(day string) int64 {
	var m sql.NullInt64 // not `max`: avoid shadowing the builtin (predeclared linter)
	if err := s.db.QueryRow("SELECT MAX(seq) FROM journal WHERE day=?", day).Scan(&m); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Error("store: maxSeq query", "err", err, "day", day)
	}
	if m.Valid {
		return m.Int64
	}
	return 0
}
