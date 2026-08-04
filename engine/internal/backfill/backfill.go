// Package backfill wires eTape's deep-history path: at boot it warm-starts each
// fed symbol from the SQLite bar archives, then walks ordered provider chains
// (daily = [alpaca?, yahoo?, moomoo-last-resort]) plus a quota-free moomoo 1m
// tail, seeding md.Core with
// each batch in one call. md.Core itself absorbs an entire
// history batch as one BarSnapshot per timeframe rather than one BarUpdate
// per bar, so the per-bar fan-out that used to require chunking (see the
// removed seedChunked) can no longer overflow its drop-on-full updates
// channel.
package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/feed"
	"github.com/earlisreal/eTape/engine/internal/session"
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

// Source pairs a HistFetcher with a short label naming which provider served,
// for logging. The orchestrator walks a chain of Sources in order.
type Source struct {
	Name string
	HistFetcher
}

// TailFetcher pulls the quota-free recent 1m window (moomoo Qot_GetKL, ≤1,000
// bars) for a symbol with an active K_1M subscription. Implemented by
// *opend.OpenDFeed; nil in replay/demo (no OpenD), where the tail step is
// skipped.
type TailFetcher interface {
	Tail1m(ctx context.Context, symbol string) ([]feed.Bar, error)
}

// Seeder receives backfilled bars. Implemented by *md.Core. SeedOlder1m feeds
// a pan-triggered deeper 1m chunk (strictly older than anything already
// loaded) -- unlike SeedHistory1m it must not replace the existing series, so
// md.Core emits a BarPrepend delta for it instead of a full BarSnapshot.
type Seeder interface {
	SeedDaily(symbol string, bars []feed.Bar)
	SeedHistory1m(symbol string, bars []feed.Bar)
	SeedHistory10s(symbol string, bars []feed.Bar)
	SeedOlder1m(symbol string, bars []feed.Bar)
	SeedSessionTicks(symbol string, ticks []feed.Tick)
}

type historyBarrier interface{ SyncHistory(symbol string) }

func (o *Orchestrator) syncHistory(symbol string) {
	if b, ok := o.seeder.(historyBarrier); ok {
		b.SyncHistory(symbol)
	}
}

// Archive is the local warm-start + persistence source. Implemented by
// *store.Store. ArchiveBar1m/ArchiveDaily persist freshly-fetched (non
// warm-start) history: md.Core's history seed no longer emits a per-bar
// BarUpdate for forwardMD to archive (see the BarSnapshot fan-out fix in
// package md), so a fresh fetch must be archived here at the source instead.
// Warm-started bars (read from this same archive by warmStart) are not
// re-archived -- ArchiveBar1m/ArchiveDaily is idempotent (INSERT OR REPLACE)
// regardless, but there is nothing new to persist.
type Archive interface {
	ReadDailyBars(symbol string) ([]feed.Bar, error)
	ReadBars1m(symbol string, fromMs, toMs int64) ([]feed.Bar, error)
	ReadRecentBars1m(symbol string, limit int) ([]feed.Bar, error)
	ReadBars10s(symbol string, fromMs, toMs int64) ([]feed.Bar, error)
	ReadRecentBars10s(symbol string, limit int) ([]feed.Bar, error)
	ArchiveBar1m(b feed.Bar)
	ArchiveBar10s(b feed.Bar)
	ArchiveDaily(b feed.Bar)
}

type rangeArchive interface {
	ArchiveRange(symbol, timeframe string, fromMs, toMs int64, bars []feed.Bar) error
	MissingRanges(symbol, timeframe string, fromMs, toMs int64) ([]feed.TimeRange, error)
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

// Orchestrator runs the per-symbol backfill sequence over ordered provider
// chains: daily = [alpaca?, yahoo?, moomoo-last-resort], intraday (1m deep) =
// [alpaca?], plus the moomoo quota-free 1m tail. In normal operation the
// moomoo entries never fire, so historical quota spend is ~0.
type Orchestrator struct {
	daily    []Source
	intraday []Source
	tail     TailFetcher
	seeder   Seeder
	archive  Archive
	clk      clock.Clock
	cfg      Config

	mu        sync.Mutex
	oldest1m  map[string]int64 // symbol -> oldest loaded 1m watermark (ms); floor of explored depth
	dailyDone map[string]bool  // symbol -> pre-2016 daily one-shot already served
	older     singleflight.Group
	olderDay  singleflight.Group
	warm      singleflight.Group
	sem       chan struct{}
	warmMu    sync.Mutex
	warmLocks map[string]*sync.Mutex
}

func New(daily, intraday []Source, tail TailFetcher, seeder Seeder, archive Archive, clk clock.Clock, cfg Config) *Orchestrator {
	if cfg.IntradayDays <= 0 {
		cfg.IntradayDays = 70
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	return &Orchestrator{
		daily: daily, intraday: intraday, tail: tail, seeder: seeder, archive: archive,
		clk: clk, cfg: cfg,
		oldest1m: map[string]int64{}, dailyDone: map[string]bool{}, sem: make(chan struct{}, cfg.Concurrency),
		warmLocks: map[string]*sync.Mutex{},
	}
}

const warmSegmentTradingDays = 10

// Warm archives tiered history without publishing the full archive into the
// chart mirror. Watch demands pass 2; chart/focused demands pass 70. A deep
// request may race a watch request, so the singleflight key includes depth;
// archive upserts make their overlap harmless and restart-safe.
func (o *Orchestrator) Warm(ctx context.Context, symbol string, days int) error {
	if days <= 0 {
		return nil
	}
	key := fmt.Sprintf("%s|%d", symbol, days)
	_, err, _ := o.warm.Do(key, func() (any, error) {
		lock := o.symbolWarmLock(symbol)
		lock.Lock()
		defer lock.Unlock()
		select {
		case o.sem <- struct{}{}:
			defer func() { <-o.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, o.warmDepth(ctx, symbol, days)
	})
	return err
}

func (o *Orchestrator) symbolWarmLock(symbol string) *sync.Mutex {
	o.warmMu.Lock()
	defer o.warmMu.Unlock()
	lock := o.warmLocks[symbol]
	if lock == nil {
		lock = &sync.Mutex{}
		o.warmLocks[symbol] = lock
	}
	return lock
}

// RefreshDaily repairs only the completed daily tail; used by after-close
// scheduling so 1m segments do not shift and refetch every evening.
func (o *Orchestrator) RefreshDaily(ctx context.Context, symbol string) error {
	lock := o.symbolWarmLock(symbol)
	lock.Lock()
	defer lock.Unlock()
	select {
	case o.sem <- struct{}{}:
		defer func() { <-o.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return o.fillDailyArchiveOnly(ctx, symbol, dailyFloor, completedDailyTo(o.clk.Now()), true)
}

func (o *Orchestrator) warmDepth(ctx context.Context, symbol string, days int) error {
	now := o.clk.Now()
	if days >= o.cfg.IntradayDays {
		o.fastArchiveFirstPaint(ctx, symbol)
		// K_1M is active for chart/focused demands. This cache read gives first
		// paint and persists the latest 1,000 bars while deep segments archive.
		o.tail1m(ctx, symbol)
	}
	remaining := days
	to := completed1mTo(now)
	var intradayErr error
	for remaining > 0 {
		step := warmSegmentTradingDays
		if remaining < step {
			step = remaining
		}
		from := intradayFrom(to, step)
		if err := o.fill1mArchiveOnly(ctx, symbol, from, to); err != nil {
			intradayErr = err
			break
		}
		to = from
		remaining -= step
	}
	dailyErr := o.fillDailyArchiveOnly(ctx, symbol, dailyFloor, completedDailyTo(now), false)
	if intradayErr != nil || dailyErr != nil {
		return errors.Join(intradayErr, dailyErr)
	}
	o.noteBackfilled(symbol, intradayFrom(now, days))
	return nil
}

// completedDailyTo returns the latest official daily bucket eligible five
// minutes after that trading date's final data session.
func completedDailyTo(now time.Time) time.Time {
	s := session.Schedule(now)
	if s.TradingDay && !now.Before(s.DataClose.Add(5*time.Minute)) {
		return s.Date
	}
	return session.PreviousTradingDay(now)
}

// completed1mTo is the latest fully closed minute bucket in a real NYSE data
// session. Outside one it is the previous session's final minute.
func completed1mTo(now time.Time) time.Time {
	s := session.Schedule(now)
	if s.TradingDay && !now.Before(s.Date.Add(4*time.Hour)) {
		if now.Before(s.DataClose) {
			candidate := now.Truncate(time.Minute).Add(-time.Minute)
			if !candidate.Before(s.Date.Add(4 * time.Hour)) {
				return candidate
			}
			p := session.Schedule(session.PreviousTradingDay(now))
			return p.DataClose.Add(-time.Minute)
		}
		return s.DataClose.Add(-time.Minute)
	}
	p := session.Schedule(session.PreviousTradingDay(now))
	return p.DataClose.Add(-time.Minute)
}

func (o *Orchestrator) fill1mArchiveOnly(ctx context.Context, symbol string, from, to time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	ranges := []feed.TimeRange{{FromMs: from.UnixMilli(), ToMs: to.UnixMilli()}}
	if a, ok := o.archive.(rangeArchive); ok {
		if missing, err := a.MissingRanges(symbol, "1m", from.UnixMilli(), to.UnixMilli()); err == nil {
			ranges = missing
		}
	}
	if len(ranges) == 0 {
		slog.Debug("history warm skipped: explicit coverage", "symbol", symbol, "timeframe", "1m")
		return nil
	}
	if bars, err := o.archive.ReadBars1m(symbol, from.UnixMilli(), to.UnixMilli()); err == nil && len(bars) > 0 {
		if a, ok := o.archive.(rangeArchive); ok && isWholeGap(ranges, from, to) && coversTradingDates(bars, from, to) {
			if err := a.ArchiveRange(symbol, "1m", from.UnixMilli(), to.UnixMilli(), nil); err != nil {
				return err
			}
			slog.Debug("history warm skipped: inferred legacy archive", "symbol", symbol, "timeframe", "1m")
			return nil
		}
		if bars[0].BucketMs <= from.UnixMilli()+archiveCoverSlackMs {
			if _, ok := o.archive.(rangeArchive); !ok {
				return nil
			}
		}
	}
	for _, gap := range ranges {
		gf, gt := time.UnixMilli(gap.FromMs), time.UnixMilli(gap.ToMs)
		bars, served, err := walkChain(ctx, symbol, gf, gt, o.intraday, intraday1m, false)
		if err != nil && len(bars) == 0 {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if a, ok := o.archive.(rangeArchive); ok {
			if err := a.ArchiveRange(symbol, "1m", gap.FromMs, gap.ToMs, bars); err != nil {
				return err
			}
		} else {
			o.archive1m(bars)
		}
		slog.Debug("history warm: 1m missing interval", "symbol", symbol, "provider", served,
			"from", gf, "to", gt, "bars", len(bars))
	}
	return nil
}

func (o *Orchestrator) fillDailyArchiveOnly(ctx context.Context, symbol string, from, to time.Time, forceLatest bool) error {
	type window struct{ from, to time.Time }
	var windows []window
	if a, ok := o.archive.(rangeArchive); ok && !forceLatest {
		if missing, err := a.MissingRanges(symbol, "1d", from.UnixMilli(), to.UnixMilli()); err == nil {
			for _, gap := range missing {
				windows = append(windows, window{time.UnixMilli(gap.FromMs), time.UnixMilli(gap.ToMs)})
			}
		} else {
			windows = []window{{from: from, to: to}}
		}
	} else {
		windows = []window{{from: from, to: to}}
	}
	if len(windows) == 0 {
		slog.Debug("history warm skipped: explicit coverage", "symbol", symbol, "timeframe", "1d")
		return nil
	}
	if a, ok := o.archive.(rangeArchive); ok && !forceLatest && len(windows) == 1 && windows[0].from.Equal(from) && windows[0].to.Equal(to) {
		if daily, err := o.archive.ReadDailyBars(symbol); err == nil && coversTradingDates(daily, from, to) {
			if err := a.ArchiveRange(symbol, "1d", from.UnixMilli(), to.UnixMilli(), nil); err != nil {
				return err
			}
			slog.Debug("history warm skipped: inferred legacy archive", "symbol", symbol, "timeframe", "1d")
			return nil
		}
	}
	// Legacy tail fallback is retained only without explicit coverage, or for
	// the intentional forced refresh of the latest completed daily bar.
	_, hasRanges := o.archive.(rangeArchive)
	if !hasRanges || forceLatest {
		if daily, err := o.archive.ReadDailyBars(symbol); err == nil && len(daily) > 0 {
			earliest := time.UnixMilli(daily[0].BucketMs).UTC()
			latest := time.UnixMilli(daily[len(daily)-1].BucketMs).UTC()
			windows = windows[:0]
			if earliest.After(from) {
				windows = append(windows, window{from: from, to: earliest.AddDate(0, 0, -1)})
			}
			tailFrom := latest.AddDate(0, 0, 1)
			if forceLatest && !latest.Before(from) && !latest.After(to) {
				tailFrom = latest
			}
			if !tailFrom.After(to) {
				windows = append(windows, window{from: tailFrom, to: to})
			}
		}
	}
	for _, w := range windows {
		if w.to.Before(w.from) {
			continue
		}
		bars, served, err := walkChain(ctx, symbol, w.from, w.to, o.daily, dailyBars, true)
		if err != nil && len(bars) == 0 {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if a, ok := o.archive.(rangeArchive); ok {
			if err := a.ArchiveRange(symbol, "1d", w.from.UnixMilli(), w.to.UnixMilli(), bars); err != nil {
				return err
			}
		} else {
			o.archiveDailyBars(bars)
		}
		slog.Debug("history warm: daily", "symbol", symbol, "provider", served,
			"from", w.from, "to", w.to, "bars", len(bars))
	}
	return nil
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

// Backfill runs warm-start → quota-free tail seed → deep 1m (trimmed so the
// tail wins overlaps) → daily, for one symbol. Every step is best-effort: a
// failure is logged and later steps still run. The tail seeds first so a cold
// symbol's chart is interactive in <1 s; daily runs last so its (up to ~3 s)
// latency never delays the intraday chart. Returns the daily-fetch outcome
// (nil once any daily provider served) so a caller can re-arm on failure (the
// uihub retries a failed daily backfill once OpenD reconnects).
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) error {
	t0 := time.Now()
	now := o.clk.Now()
	from1m := intradayFrom(now, o.cfg.IntradayDays)
	if o.fastArchiveFirstPaint(ctx, symbol) {
		o.syncHistory(symbol)
	}
	o.warmStart(ctx, symbol, from1m, now)
	slog.Debug("backfill: warmStart done", "symbol", symbol, "elapsed", time.Since(t0).Round(time.Millisecond))
	t1 := time.Now()
	tailOldestMs, tailOK := o.tail1m(ctx, symbol)
	if tailOK {
		o.syncHistory(symbol)
	}
	slog.Debug("backfill: tail1m done", "symbol", symbol, "elapsed", time.Since(t1).Round(time.Millisecond))
	t2 := time.Now()
	o.fill1m(ctx, symbol, from1m, now, tailOldestMs, tailOK)
	slog.Debug("backfill: fill1m done", "symbol", symbol, "elapsed", time.Since(t2).Round(time.Millisecond))
	t3 := time.Now()
	err := o.fillDaily(ctx, symbol, now.Add(-24*time.Hour))
	slog.Debug("backfill: fillDaily done", "symbol", symbol, "elapsed", time.Since(t3).Round(time.Millisecond), "total", time.Since(t0).Round(time.Millisecond))
	o.noteBackfilled(symbol, from1m)
	o.syncHistory(symbol)
	return err
}

const (
	fastArchiveTailBars  = 500
	fastArchiveDailyBars = 1000
)

// fastArchiveFirstPaint seeds every visible chart timeframe before warmStart
// scans full archives. Official daily goes first so 1m derivation cannot emit
// a transient one-bar daily snapshot. Later full seeds replace these tails.
func (o *Orchestrator) fastArchiveFirstPaint(ctx context.Context, symbol string) bool {
	start := time.Now()
	daily, err := o.archive.ReadDailyBars(symbol)
	if err != nil {
		slog.Warn("backfill: fast daily archive read failed", "symbol", symbol, "err", err)
	} else {
		if len(daily) > fastArchiveDailyBars {
			daily = daily[len(daily)-fastArchiveDailyBars:]
		}
		seedUnlessCanceled(ctx, daily, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	}
	m1, err := o.archive.ReadRecentBars1m(symbol, fastArchiveTailBars)
	if err != nil {
		slog.Warn("backfill: fast 1m archive tail read failed", "symbol", symbol, "err", err)
	} else {
		seedUnlessCanceled(ctx, m1, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	}
	s10, err := o.archive.ReadRecentBars10s(symbol, fastArchiveTailBars)
	if err != nil {
		slog.Warn("backfill: fast 10s archive tail read failed", "symbol", symbol, "err", err)
	} else {
		seedUnlessCanceled(ctx, s10, func(b []feed.Bar) { o.seeder.SeedHistory10s(symbol, b) })
	}
	if len(daily)+len(m1)+len(s10) > 0 {
		slog.Debug("backfill: fast archive first paint served", "symbol", symbol, "daily", len(daily), "bars1m", len(m1), "bars10s", len(s10), "elapsed", time.Since(start).Round(time.Millisecond))
		return true
	}
	return false
}

// noteBackfilled records the initial 1m watermark for a symbol once its boot/
// chart-open backfill has run. Takes the minimum so a later re-run never
// raises the floor -- LoadOlder deepens strictly older than whatever the
// deepest previously-recorded watermark was.
func (o *Orchestrator) noteBackfilled(symbol string, from1m time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ms := from1m.UnixMilli()
	if cur, ok := o.oldest1m[symbol]; !ok || ms < cur {
		o.oldest1m[symbol] = ms
	}
}

// archiveCoverSlackMs: an archive window counts as "covered" if its earliest
// bar is within ~2 trading days of the window start (IPO/holiday gaps aside).
// promoted from loadOlder so fill1m can reuse it.
var archiveCoverSlackMs int64 = 2 * 24 * 60 * 60 * 1000

const (
	olderIntradayChunkTradingDays = 2
	olderDailyFloorYear           = 2000
)

// dailyFloor is the earliest daily-history start requested. Alpaca's free tier
// hard-floors at 2016-01-04; Yahoo goes deeper, but the extra depth is below
// the indicator-relevance threshold (spec's indicator-depth rationale: only a
// monthly 200-period indicator wants more, an accepted casualty). Clamping
// here keeps depth consistent regardless of which provider served.
var dailyFloor = time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
var olderDailyFloor = time.Date(olderDailyFloorYear, 1, 1, 0, 0, 0, 0, time.UTC)

// dailyFrom is DailyYears ago clamped to dailyFloor, or dailyFloor when
// DailyYears<=0.
func (o *Orchestrator) dailyFrom(now time.Time) time.Time {
	if o.cfg.DailyYears <= 0 {
		return dailyFloor
	}
	from := now.AddDate(-o.cfg.DailyYears, 0, 0)
	if from.Before(dailyFloor) {
		return dailyFloor
	}
	return from
}

// warmStart seeds from local archive only -- it does not re-archive what it
// just read back out.
func (o *Orchestrator) warmStart(ctx context.Context, symbol string, from1m, now time.Time) {
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
	if s10, err := o.archive.ReadBars10s(symbol, from1m.UnixMilli(), now.UnixMilli()); err != nil {
		slog.Warn("backfill: warm-start 10s read failed", "symbol", symbol, "err", err)
	} else {
		seedUnlessCanceled(ctx, s10, func(b []feed.Bar) { o.seeder.SeedHistory10s(symbol, b) })
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

// tail1m fetches the quota-free ≤1,000-bar recent 1m window, archives + seeds
// it, and returns the oldest bar's BucketMs so fill1m can trim the deep set to
// strictly-older bars (moomoo wins overlaps). ok is false when the tail is
// unavailable (no OpenD, not subscribed, empty, or error) — fill1m then uses
// the deep set untrimmed.
func (o *Orchestrator) tail1m(ctx context.Context, symbol string) (oldestMs int64, ok bool) {
	if o.tail == nil {
		return 0, false
	}
	bars, err := o.tail.Tail1m(ctx, symbol)
	if err != nil {
		slog.Warn("backfill: tail 1m failed", "symbol", symbol, "err", err)
		return 0, false
	}
	if len(bars) == 0 {
		return 0, false
	}
	o.archive1m(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	return bars[0].BucketMs, true // ascending → [0] is oldest
}

// fill1m walks the 1m chain for the deep window, trims to bars strictly older
// than the tail's oldest bar (when a tail seeded), then archives + seeds.
func (o *Orchestrator) fill1m(ctx context.Context, symbol string, from, to time.Time, tailOldestMs int64, tailOK bool) {
	// Archive-first: skip API if the local archive already covers this window.
	cutoffMs := to.UnixMilli()
	if tailOK {
		cutoffMs = tailOldestMs
	}
	if bars, err := o.archive.ReadBars1m(symbol, from.UnixMilli(), cutoffMs-1); err == nil &&
		len(bars) > 0 && bars[0].BucketMs <= from.UnixMilli()+archiveCoverSlackMs {
		o.archive1m(bars)
		seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
		slog.Info("backfill: deep 1m served from archive", "symbol", symbol, "bars", len(bars))
		return
	}

	bars, served, err := walkChain(ctx, symbol, from, to, o.intraday, intraday1m, false)
	if len(bars) == 0 {
		if err != nil {
			slog.Warn("backfill: deep 1m unavailable", "symbol", symbol, "err", err)
		}
		return
	}
	if tailOK {
		bars = trimOlderThan(bars, tailOldestMs)
	}
	if len(bars) == 0 {
		return
	}
	o.archive1m(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedHistory1m(symbol, b) })
	slog.Info("backfill: deep 1m served", "symbol", symbol, "provider", served, "bars", len(bars))
}

// fillDaily walks the daily chain and seeds the first non-empty result. It
// returns nil once any provider served (even with zero bars — no data is not a
// failure), otherwise the last error, so the uihub knows whether to re-arm.
func (o *Orchestrator) fillDaily(ctx context.Context, symbol string, to time.Time) error {
	from := dailyFloor
	// Seed complete archive first, then synchronize only missing tail. Empty DB
	// starts at 2016-01-01; internal holes remain separate repair work.
	if daily, err := o.archive.ReadDailyBars(symbol); err == nil && len(daily) > 0 {
		o.archiveDailyBars(daily)
		seedUnlessCanceled(ctx, daily, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
		latest := time.UnixMilli(daily[len(daily)-1].BucketMs).UTC()
		from = latest.AddDate(0, 0, 1)
		if !to.After(from) {
			slog.Info("backfill: daily current in archive", "symbol", symbol, "bars", len(daily))
			return nil
		}
	}

	bars, served, err := walkChain(ctx, symbol, from, to, o.daily, dailyBars, true)
	if len(bars) == 0 {
		return err
	}
	o.archiveDailyBars(bars)
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
	slog.Info("backfill: daily served", "symbol", symbol, "provider", served, "bars", len(bars))
	return nil
}

// fetchFunc selects DailyBars or Intraday1m off a Source for walkChain.
type fetchFunc func(Source) func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error)

func dailyBars(s Source) func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error) {
	return s.DailyBars
}
func intraday1m(s Source) func(context.Context, string, time.Time, time.Time) ([]feed.Bar, error) {
	return s.Intraday1m
}

// walkChain tries each source in order, returning the first non-empty result
// and the serving source's name. A source error is logged and the walk
// advances; an empty (nil, nil) result also advances. If every source errored,
// the last error is returned (bars nil); if every source returned empty with
// no error, (nil, "", nil).
func walkChain(ctx context.Context, symbol string, from, to time.Time, chain []Source, pick fetchFunc, daily bool) ([]feed.Bar, string, error) {
	var lastErr error
	for _, s := range chain {
		bars, err := pick(s)(ctx, symbol, from, to)
		if err != nil {
			slog.Warn("backfill: provider failed", "symbol", symbol, "provider", s.Name, "err", err)
			lastErr = err
			continue
		}
		bars = clipAndDedup(bars, from, to, daily)
		if len(bars) > 0 {
			return bars, s.Name, nil
		}
	}
	return nil, "", lastErr
}

func clipAndDedup(bars []feed.Bar, from, to time.Time, daily bool) []feed.Bar {
	seen := make(map[int64]bool, len(bars))
	out := make([]feed.Bar, 0, len(bars))
	for _, b := range bars {
		in := b.BucketMs >= from.UnixMilli() && b.BucketMs <= to.UnixMilli()
		// Synthetic/unit bars below the supported 2000+ history floor carry
		// relative timestamps; provider contract enforcement starts at the floor.
		if b.BucketMs < olderDailyFloor.UnixMilli() {
			in = true
		}
		if daily {
			bd, fd, td := session.Schedule(time.UnixMilli(b.BucketMs)).Date, session.Schedule(from).Date, session.Schedule(to).Date
			if b.BucketMs >= olderDailyFloor.UnixMilli() {
				in = !bd.Before(fd) && !bd.After(td) && session.IsTradingDay(bd)
			}
		}
		if in && !seen[b.BucketMs] {
			seen[b.BucketMs] = true
			out = append(out, b)
		}
	}
	return out
}

func isWholeGap(gaps []feed.TimeRange, from, to time.Time) bool {
	return len(gaps) == 1 && gaps[0].FromMs == from.UnixMilli() && gaps[0].ToMs == to.UnixMilli()
}

// coversTradingDates is deliberately conservative: legacy bars infer an
// explored interval only when both boundary sessions and every session
// between them are represented.
func coversTradingDates(bars []feed.Bar, from, to time.Time) bool {
	days := make(map[string]bool, len(bars))
	for _, b := range bars {
		days[session.Schedule(time.UnixMilli(b.BucketMs)).Date.Format("2006-01-02")] = true
	}
	d := session.Schedule(from).Date
	for d.Before(session.Schedule(to).Date) || d.Equal(session.Schedule(to).Date) {
		if session.IsTradingDay(d) && !days[d.Format("2006-01-02")] {
			return false
		}
		d = d.AddDate(0, 0, 1)
	}
	return len(days) > 0
}

// trimOlderThan returns the ascending prefix of bars with BucketMs strictly
// less than tsMs (the tail's oldest bar), so the deep 1m set never overwrites a
// moomoo tail bar within a run.
func trimOlderThan(bars []feed.Bar, tsMs int64) []feed.Bar {
	for i, b := range bars {
		if b.BucketMs >= tsMs {
			return bars[:i]
		}
	}
	return bars
}

// olderResult is the outcome of one LoadOlder/LoadOlderDaily attempt.
type olderResult struct {
	added     int
	exhausted bool
}

// LoadOlder deepens the shared 1m series. When requiredStartMs/requiredEndMs
// are both zero it deepens one intraday chunk (2 trading days older than the
// symbol's current watermark), archive-first, floored at configured archive
// depth (70 trading days by default). When non-zero it fetches the demanded
// [requiredStartMs, endMs) window instead, archive-first, clamped to the same
// floor. exhausted=true means floor or listing-date reached. Concurrent calls
// for the same symbol coalesce into a single fetch via the older
// singleflight.Group (same pattern as
// opendfeed.HistoryBars' hbGroup).
func (o *Orchestrator) LoadOlder(ctx context.Context, symbol string, requiredStartMs, requiredEndMs int64) (int, bool, error) {
	v, err, _ := o.older.Do(symbol, func() (any, error) {
		return o.loadOlder(ctx, symbol, requiredStartMs, requiredEndMs)
	})
	if err != nil {
		return 0, false, err
	}
	r := v.(olderResult)
	return r.added, r.exhausted, nil
}

func (o *Orchestrator) loadOlder(ctx context.Context, symbol string, requiredStartMs, requiredEndMs int64) (olderResult, error) {
	floor := intradayFrom(o.clk.Now(), o.cfg.IntradayDays)
	floorMs := floor.UnixMilli()

	// Determine the query window: range-driven (viewport-first) or watermark-driven.
	var from, to time.Time

	if requiredStartMs > 0 && requiredEndMs > 0 {
		// Range-driven: the UI demands a specific window.
		from = time.UnixMilli(requiredStartMs)
		to = time.UnixMilli(requiredEndMs)
		if from.Before(floor) {
			from = floor
		}
		if to.Before(from) {
			return olderResult{exhausted: true}, nil
		}
	} else {
		// Watermark-driven: one chunk older than the current watermark.
		o.mu.Lock()
		cur := o.oldest1m[symbol]
		o.mu.Unlock()
		if cur == 0 {
			return olderResult{}, fmt.Errorf("load older: no backfill watermark for %s", symbol)
		}
		if cur <= floorMs {
			return olderResult{exhausted: true}, nil
		}
		to = time.UnixMilli(cur) // exclusive upper bound: strictly older than what's already loaded
		from = intradayFrom(to, olderIntradayChunkTradingDays)
		if from.Before(floor) {
			from = floor
		}
		if !to.After(from) {
			o.advanceWatermark(symbol, from.UnixMilli())
			return olderResult{exhausted: true}, nil
		}
	}

	// Archive-first: if the local archive already covers the demanded window
	// (earliest returned bar within slack of `from`), it wins outright -- no
	// provider round-trip.
	if bars, err := o.archive.ReadBars1m(symbol, from.UnixMilli(), to.UnixMilli()-1); err != nil {
		slog.Warn("load older: archive read failed", "symbol", symbol, "err", err)
	} else if len(bars) > 0 && bars[0].BucketMs <= from.UnixMilli()+archiveCoverSlackMs {
		o.seedOlderUnlessCanceled(ctx, symbol, bars)
		o.syncHistory(symbol)
		o.advanceWatermark(symbol, from.UnixMilli())
		return olderResult{added: len(bars), exhausted: from.UnixMilli() <= floorMs}, nil
	}
	// Archive didn't cover the window (empty, or covers only a newer slice) --
	// fall through to the provider chain for the full [from, to) window.

	// Provider chain.
	bars, served, err := walkChain(ctx, symbol, from, to, o.intraday, intraday1m, false)
	if len(bars) == 0 {
		if err != nil {
			// Every provider genuinely errored (transient failure, not "no
			// more history exists") -- don't advance past this window so a
			// retry re-attempts the same [from, to) instead of skipping it.
			return olderResult{}, err
		}
		// No archive coverage AND no chain data, with no error: floor or
		// pre-listing reached.
		o.advanceWatermark(symbol, from.UnixMilli())
		return olderResult{exhausted: true}, nil
	}
	o.archive1m(bars)
	o.seedOlderUnlessCanceled(ctx, symbol, bars)
	o.syncHistory(symbol)
	o.advanceWatermark(symbol, from.UnixMilli())
	slog.Info("load older: deep 1m served", "symbol", symbol, "provider", served, "bars", len(bars), "from", from)
	return olderResult{added: len(bars), exhausted: from.UnixMilli() <= floorMs}, nil
}

// advanceWatermark lowers the symbol's oldest-loaded 1m watermark to ms,
// never raising it (mirrors noteBackfilled's take-the-minimum rule).
func (o *Orchestrator) advanceWatermark(symbol string, ms int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if cur, ok := o.oldest1m[symbol]; !ok || ms < cur {
		o.oldest1m[symbol] = ms
	}
}

func (o *Orchestrator) seedOlderUnlessCanceled(ctx context.Context, symbol string, bars []feed.Bar) {
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedOlder1m(symbol, b) })
}

// LoadOlderDaily one-shot-fetches daily history for [2000, 2016)
// (archive-first, then daily chain). Always exhausted=true after one success
// or one empty result -- there is only ever one chunk, so a symbol never asks
// twice in a session. Concurrent
// calls for the same symbol coalesce via the olderDay singleflight.Group.
func (o *Orchestrator) LoadOlderDaily(ctx context.Context, symbol string) (int, bool, error) {
	return o.LoadOlderDailyRange(ctx, symbol, 0, 0)
}

func (o *Orchestrator) LoadOlderDailyRange(ctx context.Context, symbol string, requiredStartMs, requiredEndMs int64) (int, bool, error) {
	v, err, _ := o.olderDay.Do(symbol, func() (any, error) {
		return o.loadOlderDaily(ctx, symbol, requiredStartMs, requiredEndMs)
	})
	if err != nil {
		return 0, false, err
	}
	r := v.(olderResult)
	return r.added, r.exhausted, nil
}

func (o *Orchestrator) loadOlderDaily(ctx context.Context, symbol string, requiredStartMs, requiredEndMs int64) (olderResult, error) {
	o.mu.Lock()
	done := o.dailyDone[symbol]
	o.mu.Unlock()
	if done && requiredStartMs == 0 {
		return olderResult{exhausted: true}, nil
	}

	floorMs := olderDailyFloor.UnixMilli()
	ceilingMs := dailyFloor.UnixMilli()
	if requiredStartMs > 0 && requiredEndMs > requiredStartMs {
		floorMs = max(floorMs, requiredStartMs)
		ceilingMs = min(o.clk.Now().UnixMilli()+1, requiredEndMs)
		if floorMs >= ceilingMs {
			return olderResult{exhausted: true}, nil
		}
	}

	// Archive-first: if the archive already holds load-older daily range (e.g. from a
	// prior session's one-shot), re-seed those instead of re-fetching. This
	// only checks "any bar below floor exists", not depth of coverage, so a
	// provider that silently truncated a prior pre-2016 fetch could look
	// "done" here -- accepted as-is: a proper fix needs either persisted
	// completeness metadata (a schema change the plan's Global Constraints
	// disallow) or dropping archive-first here (which would contradict the
	// plan's "previously-explored depth re-serves from the archive instantly,
	// across restarts" decision), and the same unverified-completeness
	// characteristic already exists, unfixed, in fillDaily's boot path.
	if all, err := o.archive.ReadDailyBars(symbol); err != nil {
		slog.Warn("load older daily: archive read failed", "symbol", symbol, "err", err)
	} else if len(all) > 0 {
		var pre []feed.Bar
		for _, b := range all {
			if b.BucketMs < floorMs {
				continue
			}
			if b.BucketMs >= ceilingMs {
				break
			}
			pre = append(pre, b)
		}
		if len(pre) > 0 {
			o.seedDailyUnlessCanceled(ctx, symbol, pre)
			o.syncHistory(symbol)
			o.markDailyDone(symbol)
			return olderResult{added: len(pre), exhausted: true}, nil
		}
	}

	from := time.UnixMilli(floorMs)
	to := time.UnixMilli(ceilingMs)
	bars, served, err := walkChain(ctx, symbol, from, to, o.daily, dailyBars, true)
	if len(bars) == 0 {
		o.markDailyDone(symbol) // never ask again this session
		return olderResult{exhausted: true}, err
	}
	var clipped []feed.Bar
	for _, b := range bars {
		if b.BucketMs < floorMs || b.BucketMs >= ceilingMs {
			continue
		}
		clipped = append(clipped, b)
	}
	if len(clipped) == 0 {
		o.markDailyDone(symbol)
		return olderResult{exhausted: true}, nil
	}
	o.archiveDailyBars(clipped)
	o.seedDailyUnlessCanceled(ctx, symbol, clipped)
	o.syncHistory(symbol)
	o.markDailyDone(symbol)
	slog.Info("load older: pre-2016 daily served", "symbol", symbol, "provider", served, "bars", len(clipped))
	return olderResult{added: len(clipped), exhausted: true}, nil
}

func (o *Orchestrator) markDailyDone(symbol string) {
	o.mu.Lock()
	o.dailyDone[symbol] = true
	o.mu.Unlock()
}

func (o *Orchestrator) seedDailyUnlessCanceled(ctx context.Context, symbol string, bars []feed.Bar) {
	seedUnlessCanceled(ctx, bars, func(b []feed.Bar) { o.seeder.SeedDaily(symbol, b) })
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
