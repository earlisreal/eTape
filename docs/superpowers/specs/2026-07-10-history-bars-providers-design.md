# History bars: multi-provider redesign (Alpaca 1m + Alpaca/Yahoo daily + quota-free moomoo tail)

**Date:** 2026-07-10
**Status:** Approved
**Revises:** the history-fetch portions of `2026-07-08-history-backfill-design.md`. Storage,
seeding, derivation, and WS delivery are unchanged.

## Problem

All historical bars (1m intraday + daily) come from moomoo `request_history_kline` (3103),
which spends the account's historical K-line quota: **1 slot per unique stock per rolling
30 days, 100 slots total** on the base tier, shared with every other OpenD client. Scanner
churn plus on-demand chart loads exhaust it, degrading deep backfill to cache depth. The
quota-contention-awareness feature (spec 2026-07-10) warns about this but changes no
behavior.

## Decision

Route history by resolution, keeping moomoo only for what it is uniquely good at — the
quota-free recent window and live streaming:

| Data | Primary | Fallback | Last resort |
|---|---|---|---|
| Daily history | Alpaca `1Day` (when configured) | Yahoo v8 chart (zero-config default) | moomoo 3103, quota-guarded |
| 1m deep history (20 trading days) | Alpaca `1Min` SIP | — | moomoo 3103, quota-guarded |
| 1m recent tail (≤1,000 bars) | moomoo `Qot_GetKL` (3006) — **quota-free** | — | — |
| Live bars/ticks/book | moomoo subscriptions | unchanged | |

In normal operation `request_history_kline` is never called; historical quota spend is ~0.
The quota poller stays as a pure safety net.

## Verified facts (2026-07-10, live benchmarks from Earl's machine, paper key)

- **Alpaca free tier serves SIP historical bars.** The 15-minute recency rule is enforced by
  **silent server-side clamping, not errors**: a 1m request with `end=now` returned HTTP 200
  with the last bar exactly at now−16 min (measured during pre-market; extended-hours bars
  included). A defensive retry with `end = now−16m` is still specified in case Alpaca ever
  returns 403 "recent SIP" again.
- **Alpaca free daily history starts hard at 2016-01-04** — `start=1980` still returns
  2016+, no further pages.
- **Latency, daily since 2016 (~2,643 bars):** Alpaca 1.26–1.49 s; Yahoo same window
  2.42–2.90 s; Yahoo full depth (KO→1970, ~14k bars) 3.2–3.7 s. Yahoo's cost is mostly fixed
  per-request overhead (~2.3 s floor), so Alpaca is ~4× faster on small incremental
  refreshes (~0.6 s).
- **Latency, 1m SIP (paginated, `limit=10000`):** ~5 trading days = 3.5k bars, 1 page,
  2.1 s; 20 trading days = 17k bars, 2 pages, 4.1 s; ~22 trading days = 24k bars, 3 pages,
  5.9–6.4 s. ~1.8–2.0 s per page; bar count, not symbol volume, drives it.
- **Yahoo `range=max` is a trap:** it silently coarsens granularity (returned *weekly* bars)
  and truncates the range. Explicit `period1`/`period2` epochs + `interval=1d` returned
  exactly the same 2,643 bars as Alpaca. The client must always use explicit windows.
- **Yahoo needs a browser-ish User-Agent**; no API key. Minor data variance vs Alpaca is
  acceptable for a fallback (SOFI since-2016: Yahoo 1,384 vs Alpaca 1,407 bars).
- **`Qot_GetKL` (3006) is in the real-time API family** (`.claude/skills/moomooapi/docs/API_LIMITS.md`):
  requires an active K_1M subscription, returns up to 1,000 recent 1m bars, spends **no
  historical quota**. Chart-open already subscribes K_1M on demand, and 1,000 extended-hours
  1m bars ≈ a full trading day — dwarfing the 15-minute Alpaca clamp.

## Indicator-depth rationale (why 2016+ daily is enough)

EMA truncation error decays as (1−2/(N+1))^bars. With ~2,643 daily bars: daily 200 EMA
error ≈ 4×10⁻¹² (exact); weekly 200 EMA residual ≈ 0.4% seed weight (invisible); weekly
200 SMA needs only ~3.8 years (exact). The only casualty is 200-period indicators on the
**monthly** timeframe (needs ~16.7 years; 2016+ has ~126 monthly bars). Decision: accepted —
no pre-2016 deep top-up. Depth is consistent regardless of which provider served.

## Architecture

### Per-symbol backfill sequence (revised)

```
warmStart (SQLite archive + journal)                     unchanged
→ tail:   Qot_GetKL ≤1,000 bars → archive + seed         chart interactive <1 s
→ fill1m: deep chain fetch (20 trading days)
          trim to ts < oldest tail ts                     tail wins overlaps
          archive + seed (series prepends)                background, ~4 s
→ fillDaily: chain fetch → archive + seed                 off the intraday critical path
```

Two deliberate ordering changes vs today:

1. **Progressive seeding.** The tail seeds first, so a cold symbol's chart is interactive in
   under a second; the Alpaca deep window arrives as a later, larger `BarSnapshot`. The UI
   already handles deep-history prepends (`ChartController.ts:107-166` rebuilds when a
   snapshot grows at the front), and `emitSeedSnapshots` always emits the engine's full
   current series, so successive seeds compose (archive ∪ tail ∪ deep).
2. **Daily moves after 1m.** Daily-provider latency (up to ~3 s on Yahoo) no longer delays
   the intraday chart; it only affects when daily/weekly/monthly timeframes deepen.

### Overlap rule: moomoo wins

Live 1m bars stream from moomoo (K_1M push). Keeping the newest history bars on the same
source makes the history→live transition seam-free (same tape, same volume basis); any
moomoo↔Alpaca discrepancy is pushed ~1,000 bars back from the live edge. Implementation is
by construction, not comparison: after a successful tail fetch, the deep set is **trimmed to
bars strictly older than the oldest tail bar** before archiving/seeding, so nothing
overwrites a moomoo bar within a run. (A later run's deep fetch may replace previously
archived moomoo tail bars with Alpaca history — acceptable: by then they are far from the
live edge, and it converges the deep archive onto one source.) If the tail fetch failed, the deep set is used untrimmed (front of
chart is ≤15 min stale until live pushes cover it).

### Provider chains (built in wiring, not config-exposed)

- daily: `[alpaca (if configured), yahoo, moomoo-last-resort]`
- 1m deep: `[alpaca (if configured), moomoo-last-resort]`

Chain-walk: try in order; on error or empty result, advance; log which provider served. The
moomoo last resort keeps the existing quota guard (`ErrHistoryQuotaExhausted`) + singleflight
(`opendfeed.go:279`) and logs loudly when it fires.

## Components

### New: `engine/internal/hist/yahoo`

Daily-only client for `https://query1.finance.yahoo.com/v8/finance/chart/{sym}`, modeled on
`hist/alpaca`:

- Always explicit `period1`/`period2` epoch seconds + `interval=1d`; never `range=`.
- **Adjusted OHLC** computed by scaling each bar's O/H/L/C by `adjclose/close` (matches
  Alpaca `adjustment=all` convention: current price real, past scaled). Volume as returned.
- Symbol mapping: strip `US.` prefix; dots→dashes for share classes (`US.BRK.B` → `BRK-B`);
  re-add `US.` on returned bars (same convention as `alpaca.go:93`).
- Browser User-Agent header (verified required).
- Own conservative `netx.TokenBucket` (~30 req/60 s, burst 5). On 429: one backoff retry,
  then error (chain advances).
- Implements `backfill.HistFetcher.DailyBars` only; `Intraday1m` returns a
  not-supported error (never routed to it anyway).
- Skips null-row bars (Yahoo emits occasional all-null entries in the quote arrays).

### Changed: `engine/internal/hist/alpaca`

- Default feed flips `iex` → `sip` (`config.Backfill.Alpaca.Feed` still overrides).
- `Intraday1m` requests up to the given `to` (typically now) and accepts the server clamp;
  if a 403 mentioning recent SIP data ever appears, retry once with `end = now−16m`.
- `DailyBars` window start defaults to 2016-01-01 (see Config).

### Changed: `engine/internal/feed/opend`

- Promote the private `cachedBars1m` (`backfill.go:55`) to a public
  `Tail1m(ctx, symbol) ([]feed.Bar, error)` on `OpenDFeed`: `Qot_GetKL` (3006),
  `KLType_1Min`, `RehabType_None`, `num=1000`. Errors when the symbol has no active K_1M
  subscription — callers treat that as "skip tail".
- `HistoryBars` (3103 path) is unchanged and remains the last-resort fetcher via
  `backfill.MoomooFetcher`.

### Changed: `engine/internal/backfill`

- Orchestrator replaces the `primary`/`fallback HistFetcher` pair with two ordered chains
  (`daily []HistFetcher`, `intraday []HistFetcher`) plus a
  `TailFetcher interface { Tail1m(ctx, symbol string) ([]feed.Bar, error) }`.
- `Backfill(symbol)` implements the revised sequence above (warmStart → tail seed → trimmed
  deep 1m → daily). The old `gapThresholdMs` older-gap fallback logic is deleted — chains
  subsume it.
- Freshly fetched bars are archived at the source, as today (`archive1m`/`archiveDailyBars`).

### Wiring: `engine/cmd/etape/main.go` + `boot.go`

- Build the chains: Alpaca first in both chains iff `cfg.Backfill.Alpaca.Enabled` and creds
  resolve (existing `resolveBackfillAlpacaCreds`, which still refuses `alpaca-live`);
  Yahoo in the daily chain unless `backfill.yahoo.enabled = false`; `MoomooFetcher`
  appended to both chains as last resort unless the feed is absent.
- `Tail1m` wired from the OpenD feed; nil when running without OpenD (replay/demo) — the
  orchestrator skips the tail step when nil.

## Config

```toml
[backfill]
  intraday_days = 20   # existing default, kept (≈4 s background fill per cold symbol)
  daily_years = 0      # CHANGED semantics: 0 → since 2016-01-01 (was: all available);
                       # >0 → max(now − daily_years, 2016-01-01)
  [backfill.alpaca]
    enabled = true     # existing
    feed = "sip"       # default CHANGED from "iex"
    creds_key = ""     # existing; alpaca-live refused (unchanged)
  [backfill.yahoo]
    enabled = true     # new kill switch; no credentials
```

No config for chain order or the moomoo last resort — wiring owns that (YAGNI).

## Error handling & degraded modes

- **Tail fails** (OpenD hiccup, not subscribed, nil in replay): skip; deep set untrimmed;
  front of chart ≤15 min stale until live pushes arrive. Logged, not fatal.
- **No Alpaca configured:** daily served by Yahoo; deep 1m falls to the quota-guarded moomoo
  last resort (old behavior, preserved); tail unaffected.
- **Yahoo 429/breakage:** one backoff retry, then chain advances (to moomoo last resort).
- **Everything down:** warm-start archive depth, exactly as today.
- **Quota:** last-resort fires are loud log events; the quota poller (`internal/quota`)
  continues to surface FOREIGN/LOW/EXHAUSTED states.

## What does NOT change

Both backfill triggers are untouched: the scanner pool still deep-backfills each mover on
first pool-day admission (`scan.go` `updatePool` → `backfillOne`), and the hub still
triggers on chart-open `WantsHistory` demands — both call the same orchestrator, so movers
keep pre-fetching daily + 1m in advance (now quota-free; the tail works for pool symbols
because `WatchDemand` already includes K_1M).

Storage schema (`bars_1m`, `bars_daily`, journal), `Seeder`/`Archive` seams, BarSnapshot
batching and mirror replace, 5m/15m/30m/60m cascade and weekly/monthly derivation from
daily, WS topics/commands, UI data path, live subscriptions, the ban on `Trd_*` in the feed
connection, and the quota-contention poller.

## Testing

Unit (httptest fixtures per provider):

- Yahoo: response decode, adjclose/close scaling, null-row skipping, symbol mapping both
  directions, explicit-window URL construction, 429 retry-then-error.
- Alpaca: sip default, 403-recent retry-with-clamp, pagination (existing tests adjusted).
- Orchestrator: chain-walk order and advancement on error/empty; tail-wins trim; tail-fail
  untrimmed path; seed ordering (tail snapshot before deep snapshot); daily after 1m;
  moomoo last resort quota-guard passthrough.

Live-verify checklist (for the implementation plan):

1. Cold symbol with Alpaca configured: chart interactive <1 s (tail), deepens ~4 s; logs
   name the serving provider per fill; `get_history_kl_quota` unchanged before/after.
2. Same with Alpaca disabled: Yahoo daily + moomoo last-resort deep 1m (one quota slot,
   loud log) + tail.
3. **`Qot_GetKL` on a freshly subscribed cold symbol returns pre-subscribe bars
   immediately.** The existing degrade path implies it but it has never been measured. If a
   cache warm-up delay exists, add one tail retry after ~2 s.
4. Symbol-mapping spot checks (`BRK.B`, a preferred, an OTC name) on both Alpaca and Yahoo.
5. A session's steady-state historical quota usage reads ~0 used.
