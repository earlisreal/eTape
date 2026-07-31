# eTape — Agent Quick Reference

Personal trading platform: Go engine + TypeScript/React UI consuming moomoo OpenD market data. Candlestick charts (Lightweight Charts), L2 DOM ladder, time & sales tape. US stocks only.

## Stack

- **Engine:** Go — feed parsing, book building, indicators, order execution
- **UI:** TS + React + Vite; canvas painting for chart/ladder/tape (no React state for data)
- **Engine ↔ UI:** WebSocket + JSON over `localhost:8191` (default); TS types generated from Go via `tygo`
- **Packaging:** browser tab first; Wails v3 later
- **Rule:** high-frequency data never flows through React state

## Project Structure

```
engine/                    # Go engine
  cmd/etape/               # binary entry point (boot, tray, logging, scheduler)
  internal/
    atomicfile/            # atomic write helper
    backfill/              # historical data backfill from external sources
    broker/
      alpaca/              # Alpaca adapter (REST + WS)
      moomoo/              # moomoo trading adapter (Trd_*)
      netx/                # HTTP retry/backoff/ratelimit
      sim/                  # built-in sim broker
      tradezero/           # TradeZero adapter (REST + WS)
    clock/                 # wall clock + fake clock for tests
    config/                # config.toml parsing + validation
    creds/                 # ~/.eTape/credentials.json (per-venue keys)
    exec/                  # broker-agnostic execution engine (order lifecycle, gate, reconcile, roundtrip)
    feed/
      opend/               # moomoo OpenD client (TCP framing + protobuf)
        pb/                # ~100 generated .pb.go files (Qot_*, Trd_*, etc.)
    health/                # health checks
    hist/
      alpaca/              # historical bars from Alpaca
      yahoo/               # historical bars from Yahoo
    md/                    # market data core (bars, book, quote, indicators, tick aggregation)
    news/                  # Qot_GetSearchNews polling
    quota/                 # moomoo quota tracking/polling
    scan/                  # scanner (stock filter, watchlist)
    session/               # session time tracking (pre-market, RTH, post)
    singleinstance/        # macOS/Linux mutex + Windows named mutex
    stockinfo/              # company profiles, financials
    store/                 # SQLite feed journal + bar archive + execution state
    synth/                 # synthetic data generator (for demo/replay)
    uihub/                 # WebSocket server + JSON command/response layer
      wsmsg/               # WS message type definitions (generated)
    uihubtest/             # UI hub test helpers
    venueadmin/             # venue boot/discovery/verify
    venueprobe/             # probe venue connectivity
    venueseed/              # seed venue data
    watchlist/              # watchlist list + poller
    webui/                 # embed dist/ for browser serving
  go.mod / go.sum
  etape.exe                # built binary (dev)

ui/                        # React frontend
  src/
    chrome/                 # UI shell, panels, settings
      exec/                 # order ticket, hotkeys, sizing, venue selection
      panels/               # Chart, Ladder, Tape, Scanner, Watchlist, StockInfo
        tv/                 # TradingView chart integration
    data/                  # stores (non-React state: ring buffer, bar/book stores)
    fonts.css               # IBM Plex font faces
    gen/wsmsg.ts             # TS types generated from Go protobuf
    main.tsx               # entry point
    perf/                   # performance monitoring
    render/                 # canvas painters
      chart/                # chart primitives, drawings, indicators, sessions
      ladder/               # DOM ladder painter
      tape/                 # time & sales tape painter
    sound/                  # order event sounds (Web Audio API)
    wire/                   # WebSocket client + codec + contract types
  mock-engine/              # mock OpenD server for dev/testing
  e2e/                      # Playwright E2E tests
  fixtures/                 # JSON session fixtures for testing

docs/                      # API research, benchmarks, runbooks
  superpowers/
    specs/                  # approved design specs
    plans/                  # implementation plans
  2026-07-03-*.md           # API research (TradeZero, Alpaca, moomoo, premarket scanner)
  2026-07-04-*.md           # moomoo trading API, venue benchmark
  2026-07-06-*.md           # venue latency benchmark, Monday checklist
  2026-07-07-*.md           # engine pre-live checklist
  2026-07-09-*.md           # settings redesign, venues/creds ownership
  2026-07-12-*.md           # journal seal/vacuum boot

prototypes/               # reference implementations & latency benchmarks
  tick_to_10s_bars.py      # Python reference for Go tick→10s bar aggregation
  captures/                # JSON captures from live benchmark runs
  *.py                     # benchmark scripts (moomoo, TZ, Alpaca, venue order latency)

scripts/                   # build/utility scripts
.githooks/commit-msg       # blocks plain commits on main branch
.claude/skills/moomooapi   # moomoo SDK Python scripts + condensed docs
```

## Key Runtime Facts

- **OpenD:** runs on `127.0.0.1:11111` (TCP raw framing). Quote + trade logged in.
- **moomoo:** LV3 US entitlement (full depth book + ticks). HK LV1, crypto LV1.
- **Credentials:** `~/.eTape/credentials.json` (venue keys) + `~/.eTape/config.toml`
- **Venues (v1):** TradeZero (live), Alpaca (paper + live), moomoo (live-only). Sim broker for replay.
- **Latency:** Alpaca ~0.23s < TZ ~0.33-0.44s < moomoo ~0.9-1.0s
- **DayPnL gap:** moomoo `AccountSnapshot.DayPnL` always 0 — global MaxDayLoss circuit breaker doesn't see moomoo losses.
- **Protocols:** Go speaks raw TCP + protobuf to OpenD. Lifecycle: `InitConnect`(1001) → `KeepAlive`(1004) → request/response by serialNo. No `Trd_UnlockTrade` (2005) — done in OpenD GUI.
- **Bar architecture:** TICKER → T&S + 10s bars. K_1M → ≥1m bars. Daily fetched not aggregated. Weekly/monthly derived from daily.
- **Quotas:** base = 100 subscription slots + 100 historical K-line slots. All periods of one symbol = 1 quota slot.

## Safety Rules

- Never place/modify/cancel real orders with live keys unless Earl explicitly says so.
- Read-only endpoints (accounts, positions, orders, pnl) are fine for verification.
- Live-leg guardrails: 1-share marketable limits, cheap liquid symbol, long only, flatten immediately, RTH only.
- Re-confirm auth for live runs in the session.

## Dev Conventions

- **Subagent execution:** isolated git worktree, `main` branch protected by `.githooks/commit-msg`
- **Auto-commit:** approved specs → `docs(specs): <desc>`, plans → `docs(plans): add <feature>`
- **Push:** Earl does manually, agent does not
- **Tests:** Go `*_test.go` everywhere. UI: Vitest for TS, Playwright for E2E.
- **Build engine:** `cd engine && go build ./cmd/etape`
- **Run UI:** `cd ui && npm run dev`
- **Proto recompile:** `cd engine/internal/feed/opend && protoc-gen-go` against SDK `.proto` files

## Time-Saving Context

- **10s bars built from TICKER ticks** (not from K_1M). Exchange timestamp bucketing, not arrival time.
- **K_1M subscription preferred over per-period subscriptions** for ≥1m (1 quota slot).
- **Pre-market rank `Qot_GetUSPreMarketRank`(3410)** — zero subscription quota, 60 req/30s. Float filter `Qot_StockFilter`(3215) with `FLOAT_SHARE` (unit: thousands of shares).
- **`outstandingShares` = true free float** (DJT-verified).
- **moomoo symbols:** `US.AAPL` prefix format.
- **Session times:** pre-market 04:00 ET, RTH 09:30-16:00 ET, post 16:00-20:00 ET.
- **`~/.eTape/` directory** owns credentials.json + config.toml.
- **SQLite:** feed journal (always-on, records book/quote/bar1m) + bar archive + execution state.

Respond terse like smart caveman. All technical substance stay. Only fluff die.

Rules:
- Drop: articles (a/an/the), filler (just/really/basically), pleasantries, hedging
- Fragments OK. Short synonyms. Technical terms exact. Code unchanged.
- Pattern: [thing] [action] [reason]. [next step].
- Not: "Sure! I'd be happy to help you with that."
- Yes: "Bug in auth middleware. Fix:"

Switch level: /caveman lite|full|ultra|wenyan
Stop: "stop caveman" or "normal mode"

Auto-Clarity: drop caveman for security warnings, irreversible actions, user confused. Resume after.

Boundaries: code/commits/PRs written normal.
