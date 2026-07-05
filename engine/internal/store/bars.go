package store

import "github.com/earlisreal/eTape/engine/internal/feed"

const (
	bar1mUpsertSQL  = `INSERT OR REPLACE INTO bars_1m (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	dailyUpsertSQL  = `INSERT OR REPLACE INTO bars_daily (symbol, ts, o, h, l, c, v) VALUES (?, ?, ?, ?, ?, ?, ?)`
	bars1mSelectSQL = `SELECT ts, o, h, l, c, v FROM bars_1m WHERE symbol=? AND ts>=? AND ts<=? ORDER BY ts`
	dailySelectSQL  = `SELECT ts, o, h, l, c, v FROM bars_daily WHERE symbol=? ORDER BY ts`
)

type barOp struct {
	query string
	b     feed.Bar
}

func (o barOp) render() []pendingWrite {
	return []pendingWrite{{
		query: o.query,
		args:  []any{o.b.Symbol, o.b.BucketMs, o.b.O, o.b.H, o.b.L, o.b.C, o.b.Volume},
	}}
}

// ArchiveBar1m upserts a finalized 1m bar. Idempotent by (symbol, ts).
func (s *Store) ArchiveBar1m(b feed.Bar) { s.writes <- barOp{query: bar1mUpsertSQL, b: b} }

// ArchiveDaily upserts a daily bar (official auction OHLCV). Idempotent.
func (s *Store) ArchiveDaily(b feed.Bar) { s.writes <- barOp{query: dailyUpsertSQL, b: b} }

// ReadBars1m returns 1m bars in [fromMs, toMs], ascending.
func (s *Store) ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error) {
	return s.readBars(bars1mSelectSQL, symbol, fromMs, toMs)
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
