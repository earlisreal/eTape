package store

import (
	"fmt"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

const (
	bar10sUpsertSQL        = `INSERT OR REPLACE INTO bars_10s (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	bar1mUpsertSQL         = `INSERT OR REPLACE INTO bars_1m (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	dailyUpsertSQL         = `INSERT OR REPLACE INTO bars_daily (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	bars10sSelectSQL       = `SELECT ts, o, h, l, c, v FROM bars_10s WHERE symbol=? AND ts>=? AND ts<=? ORDER BY ts`
	bars1mSelectSQL        = `SELECT ts, o, h, l, c, v FROM bars_1m WHERE symbol=? AND ts>=? AND ts<=? ORDER BY ts`
	recentBars1mSelectSQL  = `SELECT ts, o, h, l, c, v FROM bars_1m WHERE symbol=? ORDER BY ts DESC LIMIT ?`
	recentBars10sSelectSQL = `SELECT ts, o, h, l, c, v FROM bars_10s WHERE symbol=? ORDER BY ts DESC LIMIT ?`
	recentDailySelectSQL   = `SELECT ts, o, h, l, c, v FROM bars_daily WHERE symbol=? ORDER BY ts DESC LIMIT ?`
	dailySelectSQL         = `SELECT ts, o, h, l, c, v FROM bars_daily WHERE symbol=? ORDER BY ts`
)

// HasArchivedSymbol is a fast positive-only existence check. Archived market
// data proves a symbol was valid; false still requires live feed validation.
func (s *Store) HasArchivedSymbol(symbol string) bool {
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM bars_1m WHERE symbol=?
		UNION ALL SELECT 1 FROM bars_10s WHERE symbol=?
		UNION ALL SELECT 1 FROM bars_daily WHERE symbol=?
	)`, symbol, symbol, symbol).Scan(&exists)
	return err == nil && exists != 0
}

type barOp struct {
	query string
	b     feed.Bar
}

type pruneBars10sOp struct {
	beforeMs int64
	done     chan pruneResult
}

func (pruneBars10sOp) render() []pendingWrite { return nil }

type pruneResult struct {
	rows int64
	err  error
}

func (o barOp) render() []pendingWrite {
	return []pendingWrite{{
		query: o.query,
		args:  []any{o.b.Symbol, o.b.BucketMs, o.b.O, o.b.H, o.b.L, o.b.C, o.b.Volume},
	}}
}

// ArchiveBar10s upserts a finalized 10s bar. Idempotent by (symbol, ts).
func (s *Store) ArchiveBar10s(b feed.Bar) { s.writes <- barOp{query: bar10sUpsertSQL, b: b} }

// PruneBars10sBefore deletes archived 10s bars older than beforeMs.
func (s *Store) PruneBars10sBefore(beforeMs int64) (int64, error) {
	done := make(chan pruneResult, 1)
	s.writes <- pruneBars10sOp{beforeMs: beforeMs, done: done}
	r := <-done
	return r.rows, r.err
}

func (s *Store) commitPruneBars10s(beforeMs int64) (int64, error) {
	result, err := s.db.Exec(`DELETE FROM bars_10s WHERE ts < ?`, beforeMs)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ArchiveBar1m upserts a finalized 1m bar. Idempotent by (symbol, ts).
func (s *Store) ArchiveBar1m(b feed.Bar) { s.writes <- barOp{query: bar1mUpsertSQL, b: b} }

// ArchiveDaily upserts a daily bar (official auction OHLCV). Idempotent.
func (s *Store) ArchiveDaily(b feed.Bar) { s.writes <- barOp{query: dailyUpsertSQL, b: b} }

// ReadBars10s returns 10s bars in [fromMs, toMs], ascending.
func (s *Store) ReadBars10s(symbol string, fromMs, toMs int64) ([]feed.Bar, error) {
	return s.readBars(bars10sSelectSQL, symbol, fromMs, toMs)
}

// ReadBars1m returns 1m bars in [fromMs, toMs], ascending.
func (s *Store) ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error) {
	return s.readBars(bars1mSelectSQL, symbol, fromMs, toMs)
}

// ReadRecentBars1m returns at most limit newest 1m bars, ascending. DESC LIMIT
// lets SQLite stop after first-paint depth instead of scanning full archive.
func (s *Store) ReadRecentBars1m(symbol string, limit int) ([]feed.Bar, error) {
	return s.readRecentBars(recentBars1mSelectSQL, symbol, limit)
}

func (s *Store) ReadRecentBars10s(symbol string, limit int) ([]feed.Bar, error) {
	return s.readRecentBars(recentBars10sSelectSQL, symbol, limit)
}

// ReadRecentDailyBars returns at most limit newest daily bars, ascending.
// SSR carryover only needs the most recent completed sessions, so this avoids
// scanning the full multi-year daily archive on every lookup.
func (s *Store) ReadRecentDailyBars(symbol string, limit int) ([]feed.Bar, error) {
	return s.readRecentBars(recentDailySelectSQL, symbol, limit)
}

func (s *Store) readRecentBars(query, symbol string, limit int) ([]feed.Bar, error) {
	bars, err := s.readBars(query, symbol, limit)
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(bars)-1; left < right; left, right = left+1, right-1 {
		bars[left], bars[right] = bars[right], bars[left]
	}
	return bars, nil
}

// ReadDailyBars returns all daily bars for a symbol, ascending.
func (s *Store) ReadDailyBars(symbol string) ([]feed.Bar, error) {
	return s.readBars(dailySelectSQL, symbol)
}

func (s *Store) readBars(query string, args ...any) ([]feed.Bar, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	symbol, _ := args[0].(string)
	var out []feed.Bar
	for rows.Next() {
		b := feed.Bar{Symbol: symbol}
		if err := rows.Scan(&b.BucketMs, &b.O, &b.H, &b.L, &b.C, &b.Volume); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ArchiveRange atomically upserts a fetched segment and records that the
// requested interval was completely explored. Empty successful segments are
// recorded too, preventing repeated pre-IPO/provider-empty requests.
func (s *Store) ArchiveRange(symbol, timeframe string, fromMs, toMs int64, bars []feed.Bar) error {
	done := make(chan error, 1)
	s.writes <- archiveRangeOp{symbol: symbol, timeframe: timeframe, fromMs: fromMs,
		toMs: toMs, bars: append([]feed.Bar(nil), bars...), done: done}
	return <-done
}

func (s *Store) commitArchiveRange(op archiveRangeOp) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	query := bar1mUpsertSQL
	if op.timeframe == "1d" {
		query = dailyUpsertSQL
	}
	for _, b := range op.bars {
		if _, err := tx.Exec(query, b.Symbol, b.BucketMs, b.O, b.H, b.L, b.C, b.Volume); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO bar_archive_ranges
		(symbol,timeframe,from_ts,to_ts,completed_at) VALUES (?,?,?,?,?)`,
		op.symbol, op.timeframe, op.fromMs, op.toMs, time.Now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return nil
}

// RangeCovered reports whether completed ranges form an unbroken union over
// [fromMs,toMs].
func (s *Store) RangeCovered(symbol, timeframe string, fromMs, toMs int64) (bool, error) {
	gaps, err := s.MissingRanges(symbol, timeframe, fromMs, toMs)
	return len(gaps) == 0, err
}

// MissingRanges returns the merged uncovered portions of [fromMs,toMs].
func (s *Store) MissingRanges(symbol, timeframe string, fromMs, toMs int64) ([]feed.TimeRange, error) {
	if toMs < fromMs {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT from_ts,to_ts FROM bar_archive_ranges
		WHERE symbol=? AND timeframe=? AND to_ts>=? AND from_ts<=? ORDER BY from_ts`,
		symbol, timeframe, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cursor := fromMs
	var gaps []feed.TimeRange
	for rows.Next() {
		var from, to int64
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		if from > cursor+1 {
			gaps = append(gaps, feed.TimeRange{FromMs: cursor, ToMs: min(from-1, toMs)})
		}
		if to >= cursor {
			cursor = to + 1
		}
		if cursor > toMs {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("range coverage: %w", err)
	}
	if cursor <= toMs {
		gaps = append(gaps, feed.TimeRange{FromMs: cursor, ToMs: toMs})
	}
	return gaps, nil
}
