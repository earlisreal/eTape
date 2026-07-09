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
