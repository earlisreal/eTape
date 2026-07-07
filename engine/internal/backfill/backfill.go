// Package backfill wires eTape's deep-history path: at boot it warm-starts each
// fed symbol from the SQLite bar archives, then gap-fills from moomoo (daily
// full depth + intraday 1m) with an optional Alpaca 1m-depth fallback, seeding
// md.Core in bounded chunks so the per-bar BarUpdate fan-out never overflows
// the core's drop-on-full updates channel.
package backfill

import (
	"context"
	"time"

	"github.com/earlisreal/eTape/engine/internal/feed"
)

// HistFetcher pulls history from one source. Bars are ascending, bucket-START
// keyed, and price-adjusted (moomoo forward-rehab / Alpaca adjustment=all). A
// source that has no data for the range returns (nil, nil).
type HistFetcher interface {
	DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error)
	Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error)
}

// Seeder receives backfilled bars. Implemented by *md.Core.
type Seeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
	SeedHistory1m(symbol string, bars []feed.Bar)
}

// Archive is the quota-free local warm-start source. Implemented by *store.Store.
type Archive interface {
	ReadDailyBars(symbol string) ([]feed.Bar, error)
	ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error)
}

// seedChunked calls seed with successive ≤chunk slices of bars, preserving
// order. Chunking bounds a single md.Core apply's emitted-update count so it
// cannot overflow the 8192-deep updates channel (each 1m bar fans out to ~8
// updates: 1m + intraday cascade + daily + weekly/monthly); the concurrent
// forwardMD drains between chunks.
func seedChunked(chunk int, bars []feed.Bar, seed func([]feed.Bar)) {
	if chunk <= 0 {
		chunk = 500
	}
	for i := 0; i < len(bars); i += chunk {
		end := i + chunk
		if end > len(bars) {
			end = len(bars)
		}
		seed(bars[i:end])
	}
}
