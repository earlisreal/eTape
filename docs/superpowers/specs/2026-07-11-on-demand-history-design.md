# On-demand deeper history loading (pan-triggered)

- **Date:** 2026-07-11
- **Status:** Approved
- **Revises:** nothing — builds on `2026-07-10-history-bars-providers-design.md`
  (fixed-depth boot backfill: 20 trading days of 1m, daily since 2016-01-01)

## Problem

Boot backfill loads a fixed window: 20 trading days of 1m bars and daily bars back
to 2016-01-01. Panning a chart past the earliest loaded bar hits a hard wall — no
way to see older intraday history, and nothing before 2016 on daily/weekly/monthly.
Raising `intraday_days` globally is the wrong fix: it slows boot for every
watchlist symbol whether or not the user ever pans, and still hits a wall.

Goal: when the user pans near the earliest loaded bar, asynchronously fetch the
next chunk of older history and paint it in place — depth grows only for symbols
the user actually explores.

## Decisions (brainstorm outcomes)

| Question | Decision |
|---|---|
| Trigger scope | All intraday TFs (1m/5m/15m/30m/60m) deepen the shared 1m series; D/W/M charts additionally trigger pre-2016 daily |
| Pre-2016 daily | Yes — via Yahoo (only provider with pre-2016 data), one-shot fetch to listing date |
| 1m depth cap | None — floor at 2016-01-01 (Alpaca free-tier floor). Memory grows only with deliberate panning; cap is a one-line config later if ever needed |
| Trigger timing | Prefetch when the viewport gets within ~1.5 screens of the earliest bar (not only at the wall) |
| Chunk size | Reuse `backfill.intraday_days` (20 trading days) per intraday chunk; no new config keys |
| Persistence | All fetched bars archived to SQLite; previously-explored depth re-serves from the archive instantly, across restarts. Boot seeding stays at 20 days — boot cost unchanged |
| Transport | New WS command `LoadOlderBars` with deferred ack; bars arrive as `md.bars` pushes |
| Wire growth | New `BarPrepend` batch delta for intraday TFs (constant per-chunk cost); D/W/M keep full `BarSnapshot` re-emits |

## Architecture

Approach chosen: **engine-owned deepening through the existing seed path**. The
engine is the aggregator (5m–60m cascade from 1m, W/M derive from daily, indicators
are engine-computed), so older bars must enter through md.Core — every panel on the
symbol deepens together, timeframe switches keep the depth, and indicators stay
correct. Rejected: UI-side query + local merge (fragments the single source of
truth); raising `intraday_days` (see Problem).

### 1. Trigger (UI — `ui/src/chrome/panels/ChartPanel.tsx`)

The existing `timeScale.subscribeVisibleLogicalRangeChange` handler (today only a
right-edge scroll clamp, `ChartPanel.tsx:199-221`) gains a left-edge check:

- Condition: `range.from − LEFT_PAD_BARS < 1.5 × (range.to − range.from)` — fewer
  than ~1.5 screens of bars remain to the left of the viewport.
- Intraday TF active → `LoadOlderBars {symbol}`.
  Day/Week/Month active → `LoadOlderBars {symbol, daily: true}`.
- Guards:
  - one in-flight request per symbol per panel; re-evaluate the condition after
    the prepend lands (fast panning chains fetches naturally);
  - `exhausted` flag per (symbol, kind) — set on an `exhausted` ack, permanently
    stops asking; cleared on symbol change;
  - short cooldown (~5s) after an `error` ack before re-triggering;
  - 30s client-side timeout clears the in-flight flag (lost ack on reconnect
    must not wedge the trigger).
- `ChartApiFacade` gains `getVisibleLogicalRange()`; plus visible **time** range
  get/set for §5 (`ChartApiFacade.ts`, facade built in `ChartPanel.tsx:makeFacade`).

### 2. Protocol (WS)

One new command on the existing corrId/ack machinery (`wsmsg.CommandMsg` /
`AckMsg`, `engine/internal/uihub/wsmsg/wsmsg.go`):

- `LoadOlderBars { symbol: string, daily: bool }`
- **Deferred ack**: the handler starts the fetch in a goroutine and acks only when
  fetch+seed completes — ack value `{ added: int, exhausted: bool }`, or status
  `error`. The UI's `sendCommand` Promise (`ui/src/wire/WsClient.ts`) is the
  completion signal. Acks ride the conn's outbox, so a seconds-later ack is safe
  and nothing blocks the hub loop.
- Prepended bars are **not** in the ack — they arrive as normal `md.bars` pushes
  (§4), so every connected client updates, not just the requester.

### 3. Engine fetch (`engine/internal/backfill`)

`Orchestrator` gains `LoadOlder(ctx, symbol)` and `LoadOlderDaily(ctx, symbol)`,
wrapped in per-symbol singleflight (`golang.org/x/sync/singleflight`, same pattern
as the scanner double-quota fix). The orchestrator tracks a per-symbol
oldest-loaded-1m watermark and a daily-done flag, initialized whenever that
symbol's `Backfill` completes (boot watchlist or chart-open `EnsureDemand`).
`LoadOlderBars` for a symbol with no watermark (backfill not yet run/finished)
acks `error` — the normal chart-open seed must land first.

**Intraday chunk:**
1. Window = previous oldest 1m bucket minus 20 trading days (`intradayFrom`
   weekday-walk, `engine/internal/backfill/window.go`), clamped at the
   2016-01-01 floor.
2. **Archive-first**: `store.ReadBars1m(symbol, from, to)` — if the archive
   covers the window (earliest archived bar within ~2 trading days of the window
   start), serve from SQLite. Instant, free, and previously-explored depth
   survives restarts without provider calls.
3. Otherwise walk the existing intraday chain (Alpaca SIP → moomoo,
   `walkChain`, `backfill.go:299-313`) for the window; archive results via the
   existing `archive1m` path (idempotent upsert).
4. Zero bars from archive **and** chain → `exhausted` (2016 floor or pre-listing
   reached). Partial windows (IPO mid-window) prepend what was returned; the
   *next* request comes back empty and flips to exhausted.

**Pre-2016 daily (one-shot):**
- Window = [epoch, 2016-01-01). Walk the existing daily chain
  (Alpaca → Yahoo → moomoo): Alpaca returns empty pre-2016 and the chain
  advances; Yahoo serves back to listing (explicit `period1/period2`, never
  `range=` — see the 07-10 spec's Yahoo trap note). ≤ ~15k bars worst case, so no
  chunking. Archive via `archiveDailyBars`. `exhausted` after one success or
  one empty result — never asked again for the session.

**Replay/demo:** no fetchers are wired, but archive-first still applies — archived
depth loads; past that, ack `exhausted`. No special casing beyond a nil-chain
check.

### 4. Seed + emission (`engine/internal/md`, `engine/internal/uihub`)

- **Pre-2016 daily reuses `SeedDaily` unchanged** — upsert, D/W/M full
  `BarSnapshot` re-emits (small series), indicator reseed included.
- **Intraday gets a new `SeedOlder1m` path** in md.Core: `seeding=true`
  (suppress per-bar emits), upsert + cascade the chunk. Chunks are whole trading
  days, so 5m–60m buckets never straddle the chunk boundary (session-anchored
  intraday buckets never span days); only the earliest **week/month** bar can
  mutate.
- **Emission — the one new wire concept.** Full-series `BarSnapshot` re-emits
  grow unboundedly with depth (~240k bars × 6 TFs after a year of panning), so:
  - Intraday TFs (1m/5m/15m/30m/60m) emit `md.BarPrepend {Symbol, TF, Bars}` —
    **only the newly-added older bars** (~19k 1m bars ≈ ~2MB per 20-day chunk,
    constant cost regardless of accumulated depth).
  - Day/Week/Month keep full `BarSnapshot` (small, and boundary-safe for the
    mutated week/month bar).
- The uihub mirror (`mirror.go`) maps `BarPrepend` to a batch delta frame on
  `md.bars` (keyed `"SYMBOL:TF"`) and **prepends into its bar cache**, so
  snapshot-on-subscribe stays correct for late-joining clients.
- `ui/src/data/BarStore.ts` gets a prepend branch (batch unshift of
  strictly-older sorted bars).
- Indicator recompute: reuse `reseedSymbol` — full recompute + full indicator
  snapshot re-emits. **Accepted cost:** indicator snapshot frames grow with
  depth; optimization (emit only prepended-range points) deferred until it
  measurably hurts.
- Deep-history bars have zero buy/sell delta (no tick coverage) and
  `InProgress=false` — existing semantics, unchanged.
- New wsmsg payload for the batch delta + `tygo` regeneration of
  `ui/src/gen/wsmsg.ts`.

### 5. Viewport preservation (latent-bug fix)

`ChartController`'s front-growth rebuild (`ChartController.ts:111-123`) calls
`setData`, and Lightweight Charts preserves **logical** indices — after a 19k-bar
prepend the viewport would teleport ~20 days into the past. Fix in the rebuild
path: capture the visible **time** range before `setData`, restore it after;
skipped when parked at the right edge (current behavior is correct there). This
also fixes today's boot-seed prepends when the user happens to be scrolled back
as deep history lands.

## Error handling

- Provider/chain error → ack `error`; UI clears in-flight after the cooldown and
  re-triggers on the next pan movement.
- Lost ack (reconnect mid-fetch) → 30s UI timeout clears in-flight; engine-side
  singleflight makes the retry cheap (or archive-first serves it, since the
  first attempt may have archived before the drop).
- Unknown command name engine-side follows the existing ack-error path — never
  hangs the UI Promise.

## What does NOT change

- Boot backfill sequence, depths, and progressive seeding (tail → deep 1m →
  daily) — untouched.
- SQLite schema (`bars_1m` / `bars_daily`) — untouched; only new read/write
  call sites.
- Provider chain composition and ordering (`main.go` wiring) — reused as-is.
- `BarSnapshot`/`BarUpdate` semantics for existing paths; `md.quote`/`md.book`/
  `md.tape`/`md.indicator` topics.
- No new config keys (`intraday_days` doubles as the chunk size).

## Out of scope / deferred

- Indicator prepend-delta emission (full re-snapshot accepted for now, see §4).
- Adaptive chunk sizing per timeframe (fixed 20 trading days; a 60m chart
  re-triggers every ~1.6 screens, acceptable since chunks are fast).
- Tick-level (10s bar) deep history — impossible pre-subscription; unchanged.
- Depth cap / eviction of deep in-memory bars — revisit only if memory hurts.

## Testing

- **Engine unit:** `LoadOlder` window math, archive-first coverage heuristic,
  exhausted semantics (floor, pre-listing, replay no-chain); `SeedOlder1m`
  cascade correctness including week/month boundary mutation; mirror
  prepend-cache + snapshot-on-subscribe correctness.
- **UI unit:** `BarStore` prepend branch; `ChartController` viewport
  preservation around front-growth rebuilds (both at and away from the right
  edge); trigger threshold + guard state machine (in-flight, exhausted,
  cooldown, timeout).
- **End-to-end:** replay-mode harness — archive-backed `LoadOlderBars` works
  without OpenD (same no-OpenD verify recipe as prior chart features).
- **Live verify (outstanding, with Earl):** real Alpaca/Yahoo fetches on pan,
  pre-2016 daily on an old symbol (e.g. KO), quota behavior on the moomoo
  fallback.
