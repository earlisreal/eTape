# Deep-history backfill — design

**Date:** 2026-07-08 · **Status:** approved · **Scope:** new `engine/internal/backfill`
+ new `engine/internal/hist/alpaca` + `cmd/etape` wiring + one config block

Wires the deep-history machinery that was built but never called, so charts show full
daily history and ~20 trading days of intraday 1m instead of the ~2 days derived from
OpenD's quota-free cache.

## Problem

Charts only ever show what OpenD's quota-free caches serve. On subscribe,
`OpenDFeed.seed` emits `Bars1mEvent{Seed:true}` from `CachedBars1m` (≤1,000 recent 1m
bars via `Qot_GetKL`); the daily series is then *derived* from those 1m bars
(`barEngine.deriveDaily`), so it spans only the ~2 trading days the 1m cache covers.

The deep-history path is fully implemented but has **zero production callers**:

- `feed/opend/backfill.go` `historyBars` — `Qot_RequestHistoryKL` for K_DAY (full depth,
  forward-adjusted) and K_1M (intraday), paginated to 40k bars.
- `feed/opend/opendfeed.go` `OpenDFeed.HistoryBars` — the quota guard + 30-day fetch dedup
  wrapping the above (in the `feed.Feed` interface).
- `md/core.go` `Core.SeedDaily` / `Core.SeedHistory1m` — inbox messages that upsert bars and
  emit `BarUpdate`s the same way live bars do (verified: `barEngine.seedDaily`/`seedHistory1m`
  both call `c.barOut`, so seeded bars reach the uihub mirror and, being finalized, are
  archived by `forwardMD`).
- `store/bars.go` `ReadDailyBars` / `ReadBars1m` — read the SQLite archives that
  `forwardMD` already writes every session. Never read back, so recorded history is lost on
  restart.

Quota is **not** the reason (the original question): the account tier has 100 historical-KL
slots, all periods of one symbol cost **1 slot**, and re-fetches within 30 days are free
(`API_LIMITS.md`). A ~20–30 symbol watchlist doing daily + 1m backfill spends ~20–30 slots.
The path simply was never wired into a plan task.

## Non-goals

- **On-demand / chart-open backfill and dynamic UI→engine subscription** — owned by a separate
  session. This spec exposes a reusable per-symbol `Backfill(ctx, symbol)` entry point that the
  on-demand path will call; it does not add WS commands, a subscription controller, connection
  lifecycle changes, or UI work. Backfill here is triggered only at boot, for the symbols the
  engine already feeds (`cfg.Feed.Watchlist` + `--watch`/`--focus`).
- Non-US markets, tick-level backfill beyond the existing cache, algorithmic-backtest history,
  replacing moomoo as the primary source.

## Architecture — `internal/backfill` orchestrator

A new package owning the per-symbol backfill sequence. It depends only on three narrow
interfaces (each already satisfied by an existing concrete type), so it is unit-testable with
fakes and imports no adapter internals:

```go
package backfill

// HistFetcher pulls history from one source. Bars are ascending, bucket-START
// keyed, forward/split-adjusted. A source that lacks a period returns (nil, nil).
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
```

`Orchestrator` holds a `primary HistFetcher` (moomoo), an optional `fallback HistFetcher`
(Alpaca, nil when disabled), a `Seeder`, an `Archive`, a `clock.Clock`, and config. Its one
public method:

```go
func (o *Orchestrator) Backfill(ctx context.Context, symbol string) error
```

Steps, in order:

1. **Warm-start (instant, zero quota, offline-safe).** `Archive.ReadDailyBars` + `ReadBars1m`
   for the last `IntradayDays` trading days → seed via the chunked seeder (below). The chart
   paints immediately from local data before any network call.
2. **Daily gap-fill.** `primary.DailyBars(from, to=now)` where `from` = start of `DailyYears`
   ago, or the epoch when `DailyYears == 0` (all available). → `SeedDaily`. moomoo only —
   official auction OHLC, forward-adjusted; Alpaca daily is not substituted except on total
   primary failure (see fallback).
3. **Intraday 1m gap-fill.** `primary.Intraday1m(from = N trading days ago, to=now)` →
   `SeedHistory1m`.
4. **Fallback (Alpaca).** If `fallback != nil` **and** either the primary 1m call errored
   (incl. `ErrHistoryQuotaExhausted`) **or** its oldest returned bar is newer than the requested
   `from` (moomoo depth shallower than asked), fetch **only the missing older gap**
   `[from, primaryOldest)` from `fallback.Intraday1m` → `SeedHistory1m`. Fetching only the gap
   keeps moomoo authoritative across the overlap; `series.upsert` dedups by bucket regardless.
   If the primary *daily* call fails entirely and `fallback != nil`, use `fallback.DailyBars`
   (accepting the adjustment-seam caveat below) so a chart still renders.

`SeedDaily`/`SeedHistory1m` bars flow out as `BarUpdate`s → `forwardMD` → uihub mirror (UI) and,
being finalized, → `ArchiveDaily`/`ArchiveBar1m` (SQLite). So archiving is automatic; the
orchestrator never writes the store directly.

### Seeding discipline — the drop-on-full constraint

`md.Core.emit` is **non-blocking, drop-on-full** against an 8192-deep `updates` channel
(increments `DroppedUpdates()` on overflow). Each seeded 1m bar fans out to ~8 updates: the 1m
bar + up to 5 intraday cascade timeframes (`cascadeTFs`) + the derived daily + weekly/monthly.
20 trading days ≈ 7,800 1m bars ≈ ~62k updates — an ~8× overflow that would silently drop bars
from the UI stream (the core's *internal* series stays complete, but the emitted deltas the
mirror consumes would be lossy). The existing ≤1,000-bar cache seed already runs right at the
edge (~8k emits).

Mitigation: the orchestrator seeds in **bounded chunks** — `SeedHistory1m`/`SeedDaily` are
called with ≤ `SeedChunk` bars (default 500) per call, yielding between chunks so the concurrent
`forwardMD` goroutine drains the updates channel. 500 × ~8 = ~4k emits per chunk stays safely
under 8192 even if the drainer is momentarily behind. Daily (~1,260 bars × ~3 emits) fits in one
call but is chunked uniformly.

Chunking lives in the orchestrator (a `seedChunked` helper), not in `md.Core` — the core's
seed methods stay simple and the batching policy stays with the batch producer. Rejected
alternatives: enlarging the core's buffer (moves the cliff, doesn't remove it) and adding a bulk
"bars snapshot" emit path (more invasive to the mirror/hub contract than chunking).

## Boot-time backfill (`cmd/etape`)

After the existing `fd.Ensure(...)` loop in `main.go` (live branch only — replay's
`HistoryBars` returns nil and we issue no network history in replay), dispatch `Backfill` for
each `cfg.Feed.Watchlist` + `--watch`/`--focus` symbol through a bounded worker pool
(`Backfill.Concurrency`, default 3) so boot isn't serialized. Failures are isolated per symbol
(one bad symbol logs and does not fail the batch). The workers are `ctx`-bound so a shutdown
mid-backfill stops promptly. The orchestrator touches the store only indirectly — `SeedDaily`
sends to `md.Core`'s inbox, and store writes happen downstream in `forwardMD`
(`ArchiveDaily`/`ArchiveBar1m`), already joined by `forwardWG` before `st.Close()`. A seed that
lands in the core's buffered inbox after `md.Core.Run` has stopped is simply never applied
(harmless), so no new shutdown ordering is required beyond ctx-binding the workers.

Quota: `request_history_kline` has no per-30s rate cap in `API_LIMITS.md`, only the 100-slot
historical-KL quota (1 slot/symbol/30 days, all periods = 1 slot). At ~20–30 symbols this is
comfortable; the existing per-symbol guard in `OpenDFeed.HistoryBars` returns
`ErrHistoryQuotaExhausted` rather than erroring hard, and the orchestrator treats that as
"warm-start + fallback only", logged.

## moomoo source wrapper

`primary` is a thin adapter over the existing `feed.Feed` (concretely `*opend.OpenDFeed`):

```go
type moomooFetcher struct{ fd feed.Feed }
func (m moomooFetcher) DailyBars(ctx, sym, from, to)  { return m.fd.HistoryBars(ctx, sym, feed.ResDay, from, to) }
func (m moomooFetcher) Intraday1m(ctx, sym, from, to) { return m.fd.HistoryBars(ctx, sym, feed.Res1m, from, to) }
```

No changes to `feed/opend` — it already normalizes moomoo's end-labeled intraday K-lines to
bucket-start and applies forward rehab.

## Alpaca historical client — `internal/hist/alpaca`

A standalone REST client (not the execution broker adapter — different base URL and concern):

- Endpoint: `GET https://data.alpaca.markets/v2/stocks/{symbol}/bars` with
  `timeframe=1Min|1Day`, `start`/`end` (RFC3339), `adjustment=all` (split+dividend, closest to
  moomoo forward-adjust), `feed=iex` (free tier), `limit=10000`, paginated via `page_token`.
- **Credentials: the paper `alpaca` key** (`creds_key`, default `"alpaca"`). Free market data
  works with any key; using the paper key keeps the live `alpaca-live` keys untouched, honoring
  the CLAUDE.md safety rule. Read-only data endpoint, no order surface.
- Symbology: strip the `US.` prefix → plain ticker.
- Timestamps: Alpaca bars are UTC start-of-bar; map directly to epoch-ms bucket-start (no
  end-label shift, unlike moomoo intraday).
- Implements `backfill.HistFetcher`.

Documented caveats (surfaced in code comments + here, not silently absorbed):
- IEX-only volume is a fraction of SIP, so Alpaca-sourced bar volumes will differ from moomoo's.
- Alpaca's `adjustment=all` and moomoo's forward-rehab differ methodologically; a cosmetic price
  seam is possible at an old split/dividend boundary. Low risk inside the 20-day 1m window
  Alpaca fills, which is why Alpaca is the *gap* source (older tail) and not authoritative.

## Config (`[backfill]`)

```toml
[backfill]
enabled       = true   # master switch; false => no boot backfill at all
intraday_days = 20      # trading days of 1m history to backfill/warm-start
daily_years   = 0       # 0 = all available daily history
concurrency   = 3       # bounded boot worker pool
seed_chunk    = 500     # max bars per Seed* call (drop-on-full guard)

[backfill.alpaca]
enabled   = true        # fallback source on/off
creds_key = "alpaca"    # paper keys — free data, live keys untouched
feed      = "iex"       # free tier
```

`config.Load` gets a `Backfill` struct with these defaults applied when zero/absent, mirroring
existing config blocks. `daily_years`/`intraday_days` are resolved to `time.Time` bounds using
the `session` package's ET calendar.

## Error handling

- moomoo quota exhausted or history call fails → warm-start (SQLite) + Alpaca fallback still
  serve; logged at WARN, never silent.
- Alpaca disabled / unreachable / no creds → moomoo-only; logged, boot continues.
- Per-symbol failure isolated; the boot batch always completes.
- Replay mode → orchestrator not constructed (no network history in replay).

## Testing

- **Orchestrator** (fakes for all three interfaces): warm-start-before-network ordering; daily
  and 1m gap-fill happy path; Alpaca-fallback trigger on shallow-depth and on primary error;
  gap-only fetch (fallback range is `[from, primaryOldest)`); chunk boundary (a >`seed_chunk`
  seed splits into N calls); disabled-fallback path.
- **Alpaca client** against a mock HTTP server (mirrors the existing
  `broker/alpaca` rest_test pattern): URL/param assembly, pagination via `page_token`, `US.`
  strip, UTC→bucket-ms mapping, empty-range and error responses.
- **Drop-guard integration**: seed 20 days through a real `md.Core` + a draining `forwardMD`
  stand-in and assert `core.DroppedUpdates() == 0` and the mirror's bar count matches the seed.
- **Boot wiring**: a bounded-pool test (concurrency respected, per-symbol failure isolation,
  ctx cancellation stops dispatch).

## Forward compatibility (on-demand session)

The on-demand/dynamic-subscription work (separate session) triggers backfill per symbol when a
UI chart opens. It reuses `Orchestrator.Backfill(ctx, symbol)` unchanged — the same entry point
boot uses — after calling `feed.Ensure` for the new symbol. This spec deliberately keeps
`Backfill` free of any boot-specific assumptions so that path is a straight call.

## Implementation phases (for the plan)

1. `internal/backfill` orchestrator + `moomooFetcher` + SQLite warm-start + chunked seeding +
   `[backfill]` config + boot worker pool + shutdown join. **Delivers the reported daily/1m
   chart-depth fix.**
2. `internal/hist/alpaca` client + `[backfill.alpaca]` config + fallback arbitration in the
   orchestrator.
