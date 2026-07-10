package store

import (
	"context"

	"github.com/earlisreal/eTape/engine/internal/exec"
)

const (
	execEventInsertSQL = `INSERT INTO exec_events (ts, source, venue, type, order_id, payload)
        VALUES (?, ?, ?, ?, ?, ?)`
	fillInsertSQL = `INSERT INTO fills (seq, order_id, symbol, side, qty, price, ts, venue)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
)

// execAppendOp is a synchronous write: it commits its own transaction in the
// writer goroutine and reports the assigned seq (or an error) back on done.
type execAppendOp struct {
	env  exec.EventEnvelope
	fill *exec.FillRow
	done chan execAppendResult
}

type execAppendResult struct {
	seq int64
	err error
}

func (execAppendOp) render() []pendingWrite { return nil } // handled specially by the writer

// AppendExecEvent persists one exec event (and, for OrderFilled, its fills-
// projection row) in a single transaction, synchronously. It returns the
// AUTOINCREMENT seq. On error the caller (Core) blocks the order — the event log
// is the source of truth and must be durable before the broker POST.
func (s *Store) AppendExecEvent(env exec.EventEnvelope, fill *exec.FillRow) (int64, error) {
	op := execAppendOp{env: env, fill: fill, done: make(chan execAppendResult, 1)}
	s.writes <- op
	r := <-op.done
	return r.seq, r.err
}

// commitExecAppend runs in the writer goroutine only (invoked from the writer
// select). It owns the sole DB write handle, so this cannot race the batched
// journal/archive commits.
func (s *Store) commitExecAppend(op execAppendOp) execAppendResult {
	tx, err := s.db.Begin()
	if err != nil {
		return execAppendResult{err: err}
	}
	res, err := tx.Exec(execEventInsertSQL, op.env.TsMs, op.env.Source, op.env.Venue,
		op.env.Kind, op.env.OrderID, string(op.env.Payload))
	if err != nil {
		_ = tx.Rollback()
		return execAppendResult{err: err}
	}
	seq, err := res.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return execAppendResult{err: err}
	}
	if op.fill != nil {
		if _, err := tx.Exec(fillInsertSQL, seq, op.fill.OrderID, op.fill.Symbol, op.fill.Side,
			op.fill.Qty, op.fill.Price, op.fill.TsMs, op.fill.Venue); err != nil {
			_ = tx.Rollback()
			return execAppendResult{err: err}
		}
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return execAppendResult{err: err}
	}
	return execAppendResult{seq: seq}
}

// ReadExecEventsSince returns events with ts >= fromMs, ordered by seq (the boot-
// replay order). Payload bytes are copied out of the row scan.
func (s *Store) ReadExecEventsSince(fromMs int64) ([]exec.EventEnvelope, error) {
	rows, err := s.db.Query(
		`SELECT seq, ts, source, venue, type, order_id, payload
         FROM exec_events WHERE ts >= ? ORDER BY seq`, fromMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exec.EventEnvelope
	for rows.Next() {
		var e exec.EventEnvelope
		var payload string
		if err := rows.Scan(&e.Seq, &e.TsMs, &e.Source, &e.Venue, &e.Kind, &e.OrderID, &payload); err != nil {
			return nil, err
		}
		e.Payload = []byte(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryFills returns fills for a symbol in [fromMs, toMs), ascending — the chart-
// annotation backfill query (Plan 6 exposes it on exec.fills).
func (s *Store) QueryFills(symbol string, fromMs, toMs int64) ([]exec.FillRow, error) {
	rows, err := s.db.Query(
		`SELECT order_id, symbol, side, qty, price, ts, venue
         FROM fills WHERE symbol = ? AND ts >= ? AND ts < ? ORDER BY ts`, symbol, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exec.FillRow
	for rows.Next() {
		var f exec.FillRow
		if err := rows.Scan(&f.OrderID, &f.Symbol, &f.Side, &f.Qty, &f.Price, &f.TsMs, &f.Venue); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// QueryFillsSince returns fills across ALL venues/symbols with ts >= fromMs,
// ordered by (ts, seq) — the Trade History boot-seed input
// (Core.seedTrades), which needs every symbol at once rather than QueryFills'
// single-symbol scope. It is a "since" query with no upper bound (unlike
// QueryFills' closed [fromMs, toMs) range), and is ctx-aware via
// QueryContext so a canceled boot doesn't hang on slow I/O.
func (s *Store) QueryFillsSince(ctx context.Context, fromMs int64) ([]exec.FillRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT order_id, symbol, side, qty, price, ts, venue
         FROM fills WHERE ts >= ? ORDER BY ts, seq`, fromMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exec.FillRow
	for rows.Next() {
		var f exec.FillRow
		if err := rows.Scan(&f.OrderID, &f.Symbol, &f.Side, &f.Qty, &f.Price, &f.TsMs, &f.Venue); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ExportFills returns fills for one venue in [fromMs, toMs), ascending by
// (ts, fill_id) — the trade-export input. Unlike QueryFills (single-symbol)
// and QueryFillsSince (all-venues, no upper bound, no fill_id), it is
// venue-scoped, range-bounded, and carries fill_id so the exporter can mint
// stable externalIds. ctx-aware so a slow/canceled export doesn't hang.
func (s *Store) ExportFills(ctx context.Context, venue string, fromMs, toMs int64) ([]exec.ExportFillRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fill_id, symbol, side, qty, price, ts, venue
         FROM fills WHERE venue = ? AND ts >= ? AND ts < ? ORDER BY ts, fill_id`, venue, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exec.ExportFillRow
	for rows.Next() {
		var f exec.ExportFillRow
		if err := rows.Scan(&f.FillID, &f.Symbol, &f.Side, &f.Qty, &f.Price, &f.TsMs, &f.Venue); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
