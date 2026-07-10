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

// DroppedJournalRows counts rows dropped because their payload failed to
// encode, or because seq assignment/commit could not place them (see commit()
// in store.go: refused-to-seed and persistent-collision drops).
func (s *Store) DroppedJournalRows() uint64 { return s.dropped.Load() }

type recordOp struct {
	s      *Store
	ev     feed.Event
	recvMs int64
}

// render runs in the writer goroutine only. It does NOT assign seq: seq is
// baked into args[1] at commit time (see commit() in store.go) so a
// (day,seq) collision can be corrected in place — by the time render() ran,
// the seq it could compute here might already be stale by the time the batch
// actually commits. journalDay tags this pendingWrite so commit() knows which
// rows need a seq assigned/retried.
func (o recordOp) render() []pendingWrite {
	payload, err := encodePayload(o.ev)
	if err != nil {
		o.s.dropped.Add(1)
		slog.Error("store: journal encode failed, row dropped", "err", err, "kind", eventKind(o.ev))
		return nil
	}
	day := dayKey(o.recvMs)
	seed := 0
	if eventSeed(o.ev) {
		seed = 1
	}
	return []pendingWrite{{
		query: journalInsertSQL,
		// args[1] (seq) is a placeholder; commit() overwrites it before Exec.
		args: []any{day, int64(0), eventExchTs(o.ev, o.recvMs), o.recvMs,
			eventSymbol(o.ev), eventKind(o.ev), seed, string(payload)},
		journalDay: day,
	}}
}

// assignSeq returns the next per-day seq, seeding from the DB max on first use
// of a day (so restarts mid-day continue rather than collide). On a maxSeq
// query error it refuses to seed (returns the error) rather than caching a
// wrong value — a transient failure must not poison the day's counter at 0,
// which would collide with every already-journaled row. Writer goroutine
// only (called from commit()).
func (s *Store) assignSeq(day string) (seq int64, err error) {
	if cached, have := s.daySeq[day]; have {
		seq = cached + 1
		s.daySeq[day] = seq
		return seq, nil
	}
	m, err := s.maxSeq(day)
	if err != nil {
		return 0, err
	}
	seq = m + 1
	s.daySeq[day] = seq
	return seq, nil
}

// reseedDay re-reads the DB max for day and resets the cached counter to it,
// returning max+1. Used by commit() to recover from a (day,seq) collision —
// whatever poisoned or raced the cached counter, a fresh MAX(seq) is always a
// safe floor to mint above. Returns the query error rather than guessing.
func (s *Store) reseedDay(day string) (seq int64, err error) {
	m, err := s.maxSeq(day)
	if err != nil {
		return 0, err
	}
	seq = m + 1
	s.daySeq[day] = seq
	return seq, nil
}

// maxSeq returns the highest committed seq for day, or an error if the query
// itself failed (busy/IO/corruption) — callers must not treat that as "0",
// which would look identical to a genuinely empty day and re-mint colliding
// low seqs on top of already-journaled rows.
func (s *Store) maxSeq(day string) (int64, error) {
	var m sql.NullInt64 // not `max`: avoid shadowing the builtin (predeclared linter)
	if err := s.db.QueryRow("SELECT MAX(seq) FROM journal WHERE day=?", day).Scan(&m); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if m.Valid {
		return m.Int64, nil
	}
	return 0, nil
}

// JournalRow is one decoded journal entry.
type JournalRow struct {
	Seq    int64
	TsExch int64
	TsRecv int64
	Day    string
	Symbol string
	Kind   string
	Seed   bool
	Event  feed.Event
}

// ReadJournalDay returns a day's events in seq order, decoded to feed.Events.
func (s *Store) ReadJournalDay(day string) ([]JournalRow, error) {
	rows, err := s.db.Query(
		`SELECT seq, ts_exch, ts_recv, symbol, kind, seed, payload
		 FROM journal WHERE day=? ORDER BY seq`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JournalRow
	for rows.Next() {
		var r JournalRow
		var seed int
		var payload string
		if err := rows.Scan(&r.Seq, &r.TsExch, &r.TsRecv, &r.Symbol, &r.Kind, &seed, &payload); err != nil {
			return nil, err
		}
		r.Day = day
		r.Seed = seed != 0
		ev, err := decodePayload(r.Kind, []byte(payload))
		if err != nil {
			return nil, err
		}
		r.Event = ev
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReadJournalTicks returns one symbol's tick prints for the ET day containing
// tsMs, flattened and in journal (arrival) order — the same order the live
// pipe applied them, which the 10s watermark depends on. Seq overlaps
// (seed vs push) are preserved; de-dup is the caller's job (md.Core), exactly
// as the live and replay apply paths do it.
func (s *Store) ReadJournalTicks(symbol string, tsMs int64) ([]feed.Tick, error) {
	day := dayKey(tsMs)
	rows, err := s.db.Query(
		`SELECT payload FROM journal WHERE day=? AND symbol=? AND kind=? ORDER BY seq`,
		day, symbol, kindTicks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []feed.Tick
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		ev, err := decodePayload(kindTicks, []byte(payload))
		if err != nil {
			return nil, err
		}
		out = append(out, ev.(feed.TicksEvent).Ticks...)
	}
	return out, rows.Err()
}

// JournalDays returns the distinct recorded days, ascending.
func (s *Store) JournalDays() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT day FROM journal ORDER BY day")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
