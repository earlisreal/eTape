# eTape — Go Engine Design (v1)

**Date:** 2026-07-03
**Status:** Approved (design); implementation plan not yet written.
**Amended 2026-07-04** by `2026-07-04-multi-broker-execution-design.md`: exec is
multi-venue (TZ + Alpaca v1, moomoo v1.x); the feed-client trading-incapability rule
is reworded there (feed connection has no `Trd_*`; unlock never implemented).
**Depends on:** `docs/2026-07-03-stack-decision.md`,
`docs/superpowers/specs/2026-07-03-portfolio-orders-design.md`,
`docs/superpowers/specs/2026-07-03-ui-design.md`,
`docs/2026-07-03-moomoo-latency-benchmark.md`,
`docs/2026-07-03-premarket-scanner-api.md`, `docs/2026-07-03-news-aggregation-options.md`

## Purpose

The Go engine: a single binary that hosts the entire market-data plane (OpenD
protocol client, books, tape, bar aggregation, indicators), the execution subsystem
(per the portfolio-orders spec, hosted unchanged), the UI hub (WS topics, commands,
static file serving), the scanner/news/health pollers, SQLite persistence, and an
always-on feed journal that enables replay — dev/test in v1, interactive practice
mode in v1.5. This spec is the chassis; the execution spec remains authoritative for
exec internals.

## Decisions made during brainstorming

| Question | Decision |
|---|---|
| Relation to exec spec | Hosted as a designed black box; this spec defines the chassis + market-data plane and only the seams where exec plugs in |
| OpenD client | **Raw TCP + generated Go protobuf** (44-byte framing verified 2026-07-03); OpenD's WebSocket port is the fallback if TCP surprises; `unlock_trade` never implemented |
| Concurrency model | **Single-writer market-data core**: one goroutine owns all MD state, events in / coalesced updates out, no locks in the domain; concurrent I/O only at the edges. Same idiom as the exec fold |
| Market-data storage | **Always-on feed journal** (ticks, book, quotes, 1m bars) + persistent 1m/daily bar archives in SQLite; 10s and higher timeframes always derived, never stored |
| Replay / backtest | Journal-backed replay feed + SimBroker: dev/test + Playwright E2E in v1; **interactive practice mode is v1.5** (Clock + SimBroker seams built now); algorithmic backtesting deferred entirely |
| Repo layout | Go module in `engine/`, sibling of `ui/` |

Rejected alternatives: per-symbol actor goroutines (parallelism the load doesn't
need; per-symbol lifecycle and awkward cross-symbol ops); shared state + mutexes
(lock discipline invisible at call sites — worst fit for AI-written reviewability);
Python sidecar bridge (second process, extra hop, the measured ~30 ms pandas
overhead); OpenD WS port as primary (less-traveled interface, still needs all the
protobuf work); ticks-only ephemeral storage (superseded by the practice-data goal:
moomoo serves only ~1,000 recent ticks, so unrecorded data is unrecoverable — the
archive builds forward from day one).

## Architecture

```
OpenD (TCP 11111) ──▶ feed/opend ──▶ FeedEvents ──┬──▶ md core ────── md.* ──────▶ ┐
                       (adapter)                  └──▶ journal      │              │
                                                       (store)     last-trade      │
TradeZero ──▶ broker/tradezero ──▶ exec ◀── marks ─────────────────┘              uihub ──WS──▶ Window A
               (per exec spec)      └────────────── exec.* ──────────────────────▶ │    (+ ui/dist)  Window B
scanner / news / health pollers ─────────────── scanner.* news.* sys.* ──────────▶ ┘

store (SQLite, one writer goroutine): journal · bar archives · exec_events/fills · config · sys_events
```

```
engine/
  cmd/etape/            main: config, wiring, startup/shutdown
  internal/
    feed/               broker-agnostic MD types + Feed interface + Clock
    feed/opend/         moomoo adapter: framing, protobuf, subscriptions, backfill
    feed/opend/pb/      generated Go protobuf (all 167 protos)
    md/                 market-data core: books, tape, bars, indicators
    session/            pure ET session calendar (pre 04:00 / RTH 09:30–16:00 / post –20:00)
    exec/  exec/state  exec/gate  broker/tradezero/     ← per execution spec
    scan/               pre-market + RTH scanner poller
    news/               news poller
    health/             latency probes, sys.health / sys.events
    uihub/              WS server: topics, commands, coalescing, static files
    uihub/wsmsg/        the WS contract structs — tygo source of truth → ui/src/gen
    store/              SQLite: exec tables + journal, bar archives, config, sys_events
    replay/             journal-backed Feed + replay Clock + SimBroker
```

**Dependency rule** (generalizes the exec spec's): domain packages (`feed`, `md`,
`exec`, `session`) never import adapters (`feed/opend`, `broker/tradezero`),
`uihub`, or `store`. Adapters translate the outside world into domain events; a new
data source or broker is a new adapter only. `replay` is just another `Feed`
implementation.

**Single-writer core, concretely:** exactly one goroutine applies changes to
market-data state, consuming one typed event at a time from its inbox channel.
Everything I/O-shaped — OpenD socket reader, keepalive ticker, REST pollers, each
browser connection's writer, the SQLite writer — is its own goroutine, but none of
them touch domain state; they only pass messages. Data races are impossible by
construction, and `go test -race` enforces it in CI. Reads (snapshots for new topic
subscribers, backfill merges) enter the same loop as request messages.

**Boot sequence:** load config → open SQLite (prune per retention) → start uihub
(UI reachable immediately; panels show connecting states) → connect OpenD
(`InitConnect` → keepalive) → pre-subscribe watchlist in one batch call → seed
caches → connect TradeZero per the exec spec's buffer→snapshot→replay sequence.
Each stage retries with backoff independently; **a dead OpenD never blocks the kill
switch** — md and exec are independent subsystems that meet only at uihub and at
the price-mark events md sends exec.

**Clock:** components that need time (session calendar checks, coalescing tickers,
delayed unsubscribe, pollers) take a `Clock` interface, not `time.Now`. Live mode
passes the real clock; replay mode drives a simulated clock from journal timestamps
× speed factor. This is the seam that makes v1.5 practice mode a feature, not a
rearchitecting.

## Components

### `feed/opend` — the only code that knows moomoo exists

**Wire protocol.** TCP to `127.0.0.1:11111` (host/port in config). Framing per the
verified layout: 44-byte packed little-endian header (`"FT"` magic, u32 protoID, u8
fmtType=0 protobuf, u8 protoVer, u32 serialNo, u32 bodyLen, 20-byte body SHA1, 8
reserved) + protobuf body. All 167 `.proto` files from the installed Python SDK
compile with `protoc-gen-go` into `pb/` (generated once, committed; regeneration
scripted). One reader goroutine: read header → read body → verify SHA1 → decode →
route. One writer goroutine serializes outbound frames. Requests carry incrementing
serialNos with a pending-response map (default 5 s timeout → typed error); pushes
route by protoID to the feed-event decoder. Lifecycle: `InitConnect` (1001) returns
connID + keepalive interval → `KeepAlive` (1004) ticker. The client is structurally
incapable of trading: no trade-unlock protocol is implemented, ever (CLAUDE.md rule).

**v1 protocol surface** (names per SDK protos; IDs from generated code):
`Qot_Sub` (3001) subscribe/unsubscribe + push registration; pushes for basic quote,
order book, ticker, 1m kline; requests: get-current-kline, get-recent-ticks
(`get_rt_ticker` equivalent), get-order-book, get-basic-quote, security snapshot,
request-history-kline, US pre-market rank (3410), stock filter (3215), news search.
Everything else stays generated-but-unused.

**Subscription manager — the quota owner.** The single component that issues
`Qot_Sub`. Consumers declare *demands* ("TICKER+K_1M on AAPL for chart panel X");
the manager refcounts them — a symbol's subscriptions are the union of live demands.
Encoded rules: 1 slot per (symbol, subtype) against the 100-slot budget; no
unsubscribe within 60 s of subscribe; pending subscribes batched into one call
(benchmark: cost is per call ~50 ms, not per symbol). Released symbols get a delayed
unsubscribe with hysteresis (default 5 min) so symbol-flipping doesn't churn quota.
Under pressure: evict least-recently-used non-focused symbols, warn on `sys.events`.
Watchlist pre-subscription at session start is just another (long-lived) demand.
Standard demand profiles budget the 100 slots explicitly: **focused symbol** =
QUOTE + ORDER_BOOK + TICKER + K_1M (4 slots); **watchlist symbol** = TICKER + K_1M
(2 slots — tape/10s/1m recording, no depth). ~20 watchlist + a handful of focused
symbols ≈ 50–60 slots, comfortable headroom.

**Backfill** follows the benchmarked cheap paths: current-kline cache for ≤1,000
recent 1m bars (~9 ms, zero history quota) → recent-ticks cache (≤1,000 ticks) for
tape/10s seeding → order-book snapshot (~2.5 ms) for the ladder.
`request_history_kline` only for deeper intraday 1m and daily (forward-adjusted),
through a history-quota tracker that knows the 100-slot budget and the 30-day
same-symbol dedup. Backfill results enter the md core as seed events.

**`Feed` interface** (in `feed/`, adapter-agnostic): ensure/release subscription
demands; `Events() <-chan FeedEvent` where FeedEvent ∈ {Tick, BookSnapshot, Quote,
Bar1m, ConnUp, ConnDown, Resynced}; history/snapshot queries (bars, recent ticks,
book, quote). Implemented by `feed/opend` (live) and `replay` (journal).

### `md` — market-data core

- **Book:** moomoo pushes the full 10-level book; store latest per symbol, mark
  dirty. No incremental book building — replace is cheaper than diff at 20 rows.
- **Tape:** fixed-size ring of ticks per symbol (default 65,536, config). Appends
  mark dirty; UI mirrors via snapshot-then-delta.
- **Quotes:** latest bid/ask/last per symbol (ticket price seeding, health).
- **Bars:**
  - **10s from ticks** — Go port of `prototypes/tick_to_10s_bars.py` (verified
    live): bucket by *exchange* timestamp floored to 10 s; a tick for a later
    bucket finalizes all earlier open bars (watermark); buy/sell volume delta per
    bar. Burst-tolerant, so cache seeding replays the same path as live pushes.
  - **1m from K_1M pushes** — authoritative for 1m+. Tick-derived 1m is computed in
    parallel and compared on finalization; mismatches log to `sys.events`
    (alarm, not blocker — K_1M wins for display).
  - **5m/15m/30m/60m aggregated from 1m**, bucketed from the session anchor
    (09:30 ET default, configurable): 1m update → containing bar updates in place;
    closing 1m finalizes → containing bar finalizes when its bucket completes.
  - **Daily fetched, never aggregated** (official auction prices, forward-adjusted);
    weekly/monthly derived from daily by calendar.
  - **In-progress semantics** (matches UI spec): last bar updates in place,
    finalizes only on next-bucket evidence; quiet symbols hold partials past
    wall-clock end.
- **Indicators:** instance = `(symbol, timeframe, type, params)`, created on first
  UI demand, refcounted, destroyed on last release. Contract: `Seed(history)` once,
  `Update(bar)` per change — O(1), with the forming bar's point recomputed from the
  last finalized state (a live EMA never compounds partials). v1 catalog (UI spec):
  VWAP (session-anchored), EMA, SMA, MACD, volume, buy/sell delta. Output streams
  like any bar series.
- **Outputs:** the core is rate-unaware — it emits typed update events to uihub
  (which owns coalescing) and last-trade price events to exec (which keeps its own
  marks map for P&L and gate valuation). Message passing only; no shared state.
- **Invariant:** `replay(feed events) == state` — seeds are events, the apply path
  does no I/O, so replaying a journal reproduces bars and indicators exactly.

### `session` — pure ET calendar

Pre 04:00–09:30, RTH 09:30–16:00, post 16:00–20:00, `America/New_York` (DST-correct
by construction). Drives bar anchoring, scanner session modes, watchlist
pre-subscription timing, and the UI's session shading. Pure functions, no I/O.

### `uihub` — the engine↔UI boundary

HTTP server on `127.0.0.1:8686` (config): serves built `ui/dist` from disk (embed
is a later packaging step — disk serving keeps the Vite dev loop, where the dev
server proxies `/ws`), plus the `/ws` endpoint. Per client: reader (commands, topic
subs) + writer goroutine with its own outbound queue. Topics per the UI spec
(`md.quote/book/tape/bars/indicator`, `scanner.*`, `news.*`, `exec.*`, `sys.health`,
`sys.events`, config): full snapshot on subscribe, then deltas. **Coalescing lives
here, per topic class:** keep-latest per key for quote/book/bars flushed at a
configurable max rate (default 30 Hz, tune after Monday), batched appends for tape,
exec rates per exec spec (~4 Hz account, ~100 ms positions). A slow client never
back-pressures the engine: coalescing absorbs lag; a pathologically full queue drops
and forces snapshot re-sync. Commands (submit/cancel/replace/kill, arm/disarm,
config CRUD, topic/indicator subscribe) carry correlation IDs; sync ack
`accepted | blocked(reason)`; outcomes arrive as topic events. App-level ping/pong
supplies UI↔engine RTT. All contract structs live in `uihub/wsmsg`; `tygo` generates
`ui/src/gen` in CI so contract drift fails the build.

### Pollers

- **`scan`:** session-aware — pre-market rank (3410) every ~2 s before 09:30 ET,
  RTH rank after; daily low-float universe via stock filter (3215; float unit =
  thousands, encoded once). Emits rank rows + new-hit events; dedup memory resets at
  ET midnight; thresholds (min %change, float cap, volume floor) from config store.
- **`news`:** polls news search for focused + watchlist symbols (news is poll-only);
  normalizes to broker-agnostic `NewsItem{symbol, headline, source, url, seen_at}`;
  dedups by story ID.
- **`health`:** moomoo probe RTT (lightweight request on a subscribed symbol), TZ WS
  ping RTT surfaced from exec, periodic `sys.health`; appends `sys.events`
  (connects, gaps, quota pressure, auto-disarms) and persists them.

### `store` — persistence (one SQLite DB, WAL; extends the exec spec's schema)

One dedicated writer goroutine fed by a channel — the md core never touches disk.
Journal writes batch in transactions (~250 ms flushes). The journal tee sits at the
`Feed` boundary: every FeedEvent is forwarded to the store writer at the same moment
it enters the md core's inbox, so `replay(journal)` reproduces exactly what the core
saw (seed events included).

```sql
-- exec_events, fills: per the execution spec, unchanged
journal(day TEXT NOT NULL, seq INTEGER NOT NULL,        -- per-day monotonic
        ts_exch TEXT NOT NULL, ts_recv TEXT NOT NULL,
        symbol TEXT NOT NULL, kind TEXT NOT NULL,       -- tick|book|quote|bar1m|gap
        payload TEXT NOT NULL,                          -- JSON
        PRIMARY KEY(day, seq))
bars_1m(symbol TEXT, ts TEXT, o REAL, h REAL, l REAL, c REAL, v REAL,
        PRIMARY KEY(symbol, ts))                        -- appended on finalization
bars_daily(symbol TEXT, ts TEXT, o REAL, h REAL, l REAL, c REAL, v REAL,
        PRIMARY KEY(symbol, ts))
config(key TEXT PRIMARY KEY, value TEXT)                -- JSON docs: workspaces,
                                                        -- templates, hotkeys, thresholds
sys_events(seq INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT, kind TEXT, detail TEXT)
```

Retention (config): journal default 30 trading days (the practice-data asset —
rough volume 100–500 MB/day depending on watchlist heat; measure Monday), bar
archives kept forever, pruning at boot. Bootstrap config is a TOML file
(`~/.eTape/config.toml`: ports, OpenD address, gate limits per exec spec, retention,
coalesce rates, watchlist); runtime-changeable documents live in the `config` table
via WS CRUD. Credentials stay in `~/.eJournal/credentials.json` (existing).

### `replay` — journal-backed feed + SimBroker

Recording *is* the journal; there is no separate record mode. `etape --replay
2026-07-06 --speed 4` boots the engine with the journal `Feed` and a replay `Clock`
(simulated time from event timestamps × speed) instead of `feed/opend`, and
`SimBroker` (implements the exec spec's `Broker` interface) instead of TradeZero.
v1 SimBroker is deliberately dumb — immediate fill at limit price (market orders at
last trade) — just enough for E2E order-lifecycle walks. v1.5 practice mode adds
realistic fill simulation (queue position, partial fills) and the replay-control UI;
the seams (Clock, Feed, Broker) are all in place.

## Error handling

The exec spec's table stands for its side. Engine-wide policy: honesty (never render
stale as live), and bad input never panics — every subsystem goroutine wraps
recover → log → restart with backoff; md crashing never takes exec down (kill switch
stays reachable); crash-looping auto-disarms + banners.

| Edge | Behavior |
|---|---|
| OpenD disconnect | Jittered backoff (1 s → 30 s) → `InitConnect` → batch re-subscribe from the manager's demand set → re-seed caches as seed events → `Resynced` → uihub re-snapshots `md.*`. Logged to `sys.events` |
| Data lost in a gap | Bars self-heal (K_1M cache re-seed); missed ticks are gone — journal writes a `gap` marker, 10s bars over the hole are flagged to the UI, never interpolated |
| Corrupt frame (SHA1/decode) | Drop + count + log; repeated corruption → reconnect |
| Keepalive/request timeout | Reconnect; in-flight requests fail with typed errors |
| Subscription quota pressure | LRU eviction of non-focused symbols + `sys.events` warning; failed subscribes surface as the panel's failed state, never silence |
| History quota exhausted | Charts show what the quota-free cache serves (≤1,000 1m bars); deeper backfill degrades with a logged warning |
| Slow UI client | Coalescing absorbs; pathological overflow → drop queue, force snapshot re-sync. Engine never blocks on a browser |
| SQLite failure / disk full | Market data keeps flowing (in-memory state intact); journal/archive degrade with a loud banner. **Exception:** exec event append failure blocks order submission (safety over availability, per exec spec) |
| Timestamps | Exchange timestamps are authoritative for all bucketing; receive time is used only for latency metrics |

## Testing

- **Pure units:** bar bucketing/watermark (table-driven, seeded with the prototype's
  live-captured fixtures), session calendar (ET boundaries incl. DST transitions),
  higher-TF anchoring, indicator math with the property *streaming Seed+Update ==
  recompute-from-scratch*, quota-manager rules (refcounts, min-60s, hysteresis,
  eviction, batching).
- **Protocol client:** golden frames — real OpenD bytes captured via the Python SDK,
  checked in; codec round-trip tests; in-process mock OpenD server exercising
  handshake, keepalive timeout, mid-stream disconnect, corrupted frames.
- **MD core:** `replay(events) == state`; determinism — same journal twice yields
  byte-identical bars and indicator series; real recorded days accumulate as
  regression fixtures.
- **uihub:** contract tests for snapshot-then-delta per topic; tygo generation in CI
  (drift fails the build); slow-client drop/re-sync behavior.
- **E2E:** replay day + SimBroker + Playwright, per the UI spec.
- **CI:** `go test -race` everywhere (turns "single writer, no locks" from a
  convention into a checked invariant) + `golangci-lint`.
- **Monday live session** feeds config defaults: push cadences, scanner refresh
  latency, real journal volume/day, quota behavior.

## Out of scope (v1)

Interactive practice mode (v1.5: realistic SimBroker, replay controls, UI);
algorithmic backtesting harness; embedding `ui/dist` in the binary (packaging step,
Wails-era); HK depth (LV1 — ladder is US-first per entitlements); multiple
simultaneous feed sources; options; multi-account. The TZ-vs-Alpaca execution
decision doesn't touch this design — either lands behind the exec spec's `Broker`
interface.

## Open items

- Monday live session: push cadences, scanner refresh latency, journal MB/day,
  intraday history depth limit (empirical), OpenD push-frequency setting choice
- Capture the golden-frame corpus from the Python SDK (needed early — it validates
  the Go codec before anything is built on it)
- `protoc-gen-go` run over all 167 protos — verify no proto2/proto3 or naming
  surprises (spike, first implementation task)
- Journal `payload` as JSON is the reviewable default — revisit encoding only if
  Monday's measured volume says so
- Paper TZ keys (carried from exec spec) — blocks E2E order paths
