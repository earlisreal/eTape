// Package backfill wires eTape's deep-history path: at boot it warm-starts each
// fed symbol from the SQLite bar archives, then gap-fills from moomoo (daily
// full depth + intraday 1m) with an optional Alpaca 1m-depth fallback, seeding
// md.Core in bounded chunks so the per-bar BarUpdate fan-out never overflows
// the core's drop-on-full updates channel.
package backfill

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
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

// Config sizes the orchestrator. Zero fields get defaults in New.
type Config struct {
	IntradayDays int
	DailyYears   int
	Concurrency  int
	SeedChunk    int
}

// Orchestrator runs the per-symbol backfill sequence. primary is moomoo;
// fallback (Alpaca) is optional and may be nil.
type Orchestrator struct {
	primary  HistFetcher
	fallback HistFetcher
	seeder   Seeder
	archive  Archive
	clk      clock.Clock
	cfg      Config
}

func New(primary, fallback HistFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator {
	if cfg.IntradayDays <= 0 {
		cfg.IntradayDays = 20
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	if cfg.SeedChunk <= 0 {
		cfg.SeedChunk = 500
	}
	return &Orchestrator{primary: primary, fallback: fallback, seeder: seeder, archive: archive, clk: clk, cfg: cfg}
}

// Run backfills every symbol through a bounded worker pool, honoring ctx.
// Per-symbol failures are isolated inside Backfill (logged, never propagated).
func (o *Orchestrator) Run(ctx context.Context, symbols []string) {
	sem := make(chan struct{}, o.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, s := range symbols {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			defer func() { <-sem }()
			o.Backfill(ctx, sym)
		}(s)
	}
	wg.Wait()
}

// Backfill runs warm-start → daily gap-fill → 1m gap-fill for one symbol.
// Every step is best-effort: a failure is logged and the next step still runs,
// so a single dead source never blanks the chart.
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) {
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	o.warmStart(symbol, from1m, now)
	o.fillDaily(ctx, symbol, o.dailyFrom(now), now)
	o.fill1m(ctx, symbol, from1m, now)
}

// dailyFrom is DailyYears ago, or the epoch (all available) when DailyYears==0.
func (o *Orchestrator) dailyFrom(now time.Time) time.Time {
	if o.cfg.DailyYears <= 0 {
		return time.Unix(0, 0)
	}
	return now.AddDate(-o.cfg.DailyYears, 0, 0)
}

func (o *Orchestrator) warmStart(symbol string, from1m, now time.Time) {
	if daily, err := o.archive.ReadDailyBars(symbol); err != nil {
		slog.Warn("backfill: warm-start daily read failed", "symbol", symbol, "err", err)
	} else if len(daily) > 0 {
		seedChunked(o.cfg.SeedChunk, daily, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	}
	if m1, err := o.archive.ReadBars1m(symbol, from1m.UnixMilli(), now.UnixMilli()); err != nil {
		slog.Warn("backfill: warm-start 1m read failed", "symbol", symbol, "err", err)
	} else if len(m1) > 0 {
		seedChunked(o.cfg.SeedChunk, m1, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
}

// gapThresholdMs ignores sub-day gaps between the requested `from` and the
// primary's oldest bar — those are just weekend/holiday edges, not a real
// depth shortfall, and must not trigger a fallback fetch every boot.
const gapThresholdMs = 24 * 3600 * 1000

func (o *Orchestrator) fillDaily(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.DailyBars(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary daily failed", "symbol", symbol, "err", err)
		if o.fallback == nil {
			return
		}
		if bars, err = o.fallback.DailyBars(ctx, symbol, from, to); err != nil {
			slog.Warn("backfill: fallback daily failed", "symbol", symbol, "err", err)
			return
		}
	}
	seedChunked(o.cfg.SeedChunk, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
}

func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.Intraday1m(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary 1m failed", "symbol", symbol, "err", err)
		bars = nil
	}
	if len(bars) > 0 {
		seedChunked(o.cfg.SeedChunk, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
	if o.fallback == nil {
		return
	}
	// Fallback fills only the older gap [from, gapTo). If the primary succeeded
	// and its oldest bar is within a day of `from`, the window is covered.
	gapTo := to
	if len(bars) > 0 {
		oldestMs := bars[0].BucketMs
		if oldestMs-from.UnixMilli() < gapThresholdMs {
			return
		}
		gapTo = time.UnixMilli(oldestMs)
	}
	gap, err := o.fallback.Intraday1m(ctx, symbol, from, gapTo)
	if err != nil {
		slog.Warn("backfill: fallback 1m failed", "symbol", symbol, "err", err)
		return
	}
	if len(gap) > 0 {
		seedChunked(o.cfg.SeedChunk, gap, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
}

// MoomooFetcher adapts a feed.Feed (the live OpenD feed) as the primary
// HistFetcher: ResDay for daily, Res1m for intraday.
func MoomooFetcher(fd feed.Feed) HistFetcher { return moomooFetcher{fd: fd} }

type moomooFetcher struct{ fd feed.Feed }

func (m moomooFetcher) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return m.fd.HistoryBars(ctx, symbol, feed.ResDay, from, to)
}
func (m moomooFetcher) Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error) {
	return m.fd.HistoryBars(ctx, symbol, feed.Res1m, from, to)
}
