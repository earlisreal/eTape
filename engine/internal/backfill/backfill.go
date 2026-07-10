// Package backfill wires eTape's deep-history path: at boot it warm-starts each
// fed symbol from the SQLite bar archives, then gap-fills from moomoo (daily
// full depth + intraday 1m) with an optional Alpaca 1m-depth fallback, seeding
// md.Core with each batch in one call. md.Core itself absorbs an entire
// history batch as one BarSnapshot per timeframe rather than one BarUpdate
// per bar, so the per-bar fan-out that used to require chunking (see the
// removed seedChunked) can no longer overflow its drop-on-full updates
// channel.
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
// keyed. DailyBars is price-adjusted (moomoo forward-rehab / Alpaca
// adjustment=all) for continuous official prices across splits/dividends.
// Intraday1m (1m, and everything cascaded from it: 5m/15m/30m/60m) is
// unadjusted (moomoo RehabType_None / Alpaca adjustment=raw) so it matches
// the raw scale of the live tick/quote feed — forward-adjusting intraday
// history scales pre-split bars up by the cumulative split ratio, which for a
// heavily reverse-split symbol diverges from the live price by orders of
// magnitude and corrupts anything computed over that window (e.g. an EMA
// straddling the split). A source that has no data for the range returns
// (nil, nil).
type HistFetcher interface {
	DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error)
	Intraday1m(ctx context.Context, symbol string, from, to time.Time) ([]feed.Bar, error)
}

// Seeder receives backfilled bars. Implemented by *md.Core.
type Seeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
	SeedHistory1m(symbol string, bars []feed.Bar)
	SeedSessionTicks(symbol string, ticks []feed.Tick)
}

// Archive is the local warm-start + persistence source. Implemented by
// *store.Store. ArchiveBar1m/ArchiveDaily persist freshly-fetched (non
// warm-start) history: md.Core's history seed no longer emits a per-bar
// BarUpdate for forwardMD to archive (see the BarSnapshot fan-out fix in
// package md), so a fresh fetch must be archived here at the source instead.
// Warm-started bars (read from this same archive by warmStart) are not
// re-archived -- ArchiveBar1m/ArchiveDaily is idempotent (INSERT OR REPLACE)
// regardless, but there is nothing new to persist. ReadJournalTicks
// reconstructs today's session-ticks warm-start (10s series + shadow-1m
// delta) from the tick journal.
type Archive interface {
	ReadDailyBars(symbol string) ([]feed.Bar, error)
	ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error)
	ReadJournalTicks(symbol string, tsMs int64) ([]feed.Tick, error)
	ArchiveBar1m(b feed.Bar)
	ArchiveDaily(b feed.Bar)
}

// seedUnlessCanceled calls seed(bars) unless ctx is already done or bars is
// empty -- the same shutdown guard seedChunked used to apply per chunk:
// md.Core's inbox is bounded and blocking, and nothing drains it once
// Core.Run has returned (e.g. during shutdown), so a cancelled ctx must skip
// the send rather than risk blocking on a full, undrained inbox forever.
func seedUnlessCanceled(ctx context.Context, bars []feed.Bar, seed func([]feed.Bar)) {
	if ctx.Err() != nil || len(bars) == 0 {
		return
	}
	seed(bars)
}

// Config sizes the orchestrator. Zero fields get defaults in New.
type Config struct {
	IntradayDays int
	DailyYears   int
	Concurrency  int
	// SeedChunk is vestigial: it bounded seedChunked's per-call emitted-update
	// count, a mitigation for the per-bar BarUpdate fan-out that overflowed
	// md.Core's updates channel on a deep seed. Now that a seed emits one
	// BarSnapshot per timeframe regardless of batch size (see package md),
	// there is no longer a batch to chunk. Left in place (rather than removed)
	// to avoid an unrecognized-key break for any existing config.toml that
	// still sets seed_chunk.
	SeedChunk int
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
			_ = o.Backfill(ctx, sym) // per-symbol daily-fetch outcome is logged inside fillDaily; Run has no caller to report it to
		}(s)
	}
	wg.Wait()
}

// Backfill runs warm-start → daily gap-fill → 1m gap-fill for one symbol.
// Every step is best-effort: a failure is logged and the next step still runs,
// so a single dead source never blanks the chart. The daily-fetch outcome is
// returned (nil on success) so a caller can decide whether to retry -- e.g.
// the uihub re-arms a failed daily backfill once OpenD reconnects.
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) error {
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	o.warmStart(ctx, symbol, from1m, now)
	dailyErr := o.fillDaily(ctx, symbol, o.dailyFrom(now), now)
	o.fill1m(ctx, symbol, from1m, now)
	return dailyErr
}

// dailyFrom is DailyYears ago, or the epoch (all available) when DailyYears==0.
func (o *Orchestrator) dailyFrom(now time.Time) time.Time {
	if o.cfg.DailyYears <= 0 {
		return time.Unix(0, 0)
	}
	return now.AddDate(-o.cfg.DailyYears, 0, 0)
}

// warmStart seeds from the local archive/journal only -- it does not
// re-archive/re-journal what it just read back out. Session-ticks
// reconstruction runs first: it rebuilds today's 10s series (and enriches
// today's shadow-1m delta) from the tick journal before the daily/1m
// archive seeds run, so SeedHistory1m's fillDelta sees today's shadow data
// instead of the zero-delta a bars_1m-archived bar would otherwise get.
func (o *Orchestrator) warmStart(ctx context.Context, symbol string, from1m, now time.Time) {
	if ticks, err := o.archive.ReadJournalTicks(symbol, now.UnixMilli()); err != nil {
		slog.Warn("backfill: warm-start session-ticks read failed", "symbol", symbol, "err", err)
	} else if len(ticks) > 0 && ctx.Err() == nil {
		o.seeder.SeedSessionTicks(symbol, ticks)
	}
	if daily, err := o.archive.ReadDailyBars(symbol); err != nil {
		slog.Warn("backfill: warm-start daily read failed", "symbol", symbol, "err", err)
	} else {
		seedUnlessCanceled(ctx, daily, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	}
	if m1, err := o.archive.ReadBars1m(symbol, from1m.UnixMilli(), now.UnixMilli()); err != nil {
		slog.Warn("backfill: warm-start 1m read failed", "symbol", symbol, "err", err)
	} else {
		seedUnlessCanceled(ctx, m1, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
}

// archive1m persists freshly-fetched (non warm-start) 1m bars so they survive
// a future restart's warm-start read -- see the Archive interface's doc
// comment for why this can no longer ride the per-bar BarUpdate emit path.
func (o *Orchestrator) archive1m(bars []feed.Bar) {
	for _, b := range bars {
		o.archive.ArchiveBar1m(b)
	}
}

// archiveDailyBars is archive1m's daily-bar counterpart.
func (o *Orchestrator) archiveDailyBars(bars []feed.Bar) {
	for _, b := range bars {
		o.archive.ArchiveDaily(b)
	}
}

// gapThresholdMs ignores sub-day gaps between the requested `from` and the
// primary's oldest bar — those are just weekend/holiday edges, not a real
// depth shortfall, and must not trigger a fallback fetch every boot.
const gapThresholdMs = 24 * 3600 * 1000

// fillDaily returns the daily-fetch outcome: nil once either source seeded
// bars, otherwise the last error encountered (primary, or fallback's if a
// fallback is configured and also failed).
func (o *Orchestrator) fillDaily(ctx context.Context, symbol string, from, to time.Time) error {
	bars, err := o.primary.DailyBars(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary daily failed", "symbol", symbol, "err", err)
		if o.fallback == nil {
			return err
		}
		if bars, err = o.fallback.DailyBars(ctx, symbol, from, to); err != nil {
			slog.Warn("backfill: fallback daily failed", "symbol", symbol, "err", err)
			return err
		}
	}
	o.archiveDailyBars(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	return nil
}

func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time) {
	bars, err := o.primary.Intraday1m(ctx, symbol, from, to)
	if err != nil {
		slog.Warn("backfill: primary 1m failed", "symbol", symbol, "err", err)
		bars = nil
	}
	if len(bars) > 0 {
		o.archive1m(bars)
		seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
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
		o.archive1m(gap)
		seedUnlessCanceled(ctx, gap, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
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
