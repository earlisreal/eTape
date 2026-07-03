# eTape

Personal trading platform ("reading the tape"): a local app consuming broker market-data
feeds and rendering candlestick charts, a Level 2 DOM ladder, and time & sales.
Priorities: runtime speed, execution, stability. All code is AI-written, so
reviewability and compiler-enforced safety weigh heavily.

Full stack rationale: `docs/2026-07-03-stack-decision.md`.

## Stack (decided)

- **Engine:** Go — feed parsing, book building, indicators, order logic
- **UI:** TypeScript + React + Vite; TradingView Lightweight Charts for candles;
  custom canvas for the L2 ladder; virtualized list over a ring buffer for time & sales
- **Engine ↔ UI:** WebSocket + JSON over localhost; TS types generated from Go structs (`tygo`)
- **Packaging:** browser tab first; Wails v3 later (Electron fallback)
- **Rule:** high-frequency data never flows through React state — chart/ladder/tape are
  canvas surfaces painted imperatively, coalesced to one repaint per rAF tick

## Market data: moomoo OpenD (primary source)

moomoo OpenD is the primary source of quotes, ticks, and order-book data —
**market data only**. Order execution will go through **TradeZero** (separate broker,
integration planned later). TradeZero API fully researched 2026-07-03 —
`docs/2026-07-03-tradezero-api.md` (REST + WebSocket, order model, locates, rate
limits, eTape design consequences) + reconstructed OpenAPI spec in `docs/tradezero/`;
confirmed TradeZero exposes **no market data**, so the moomoo/TradeZero split stands.
TradeZero API credentials live in `~/.eJournal/credentials.json` (key `tradeZero`:
`keyId` + `secretKey`). **Verified 2026-07-03: these are LIVE-account keys**
(`accountType: "Live"`, real funds).
**Safety rule: never place, modify, or cancel real orders with these keys unless
Earl explicitly says so in the current conversation.** Read-only endpoints (accounts,
positions, orders, routes, pnl) are fine for verification.
Design consequence: the engine keeps a broker-agnostic
execution interface — fills arrive as generic events (symbol, side, qty, price,
timestamp) consumed by the chart/annotation layer, regardless of broker.
**Scope decision (2026-07-03): US stocks only.** One market simplifies everything:
ET session times (pre-market 04:00, RTH 09:30–16:00, post 16:00–20:00), single
timezone, `US.` code prefix, `extended_time` on subscriptions, LV3 entitlement.

**Bar architecture (decided):**
- TICKER subscription → time & sales, 10s/sub-minute bars, buy/sell delta
- K_1M subscription → live 1m bars; **5m/15m/30m/60m aggregated locally from 1m**
  (session-anchored buckets at 09:30 ET, TradingView-style; make anchor configurable)
- `request_history_kline` K_1M → intraday history backfill on chart open
  (live K_1M subscription only streams forward)
- `request_history_kline` K_DAY → daily history **fetched, not aggregated** (official
  auction open/close prices, split/dividend adjustment via rehab; use forward-adjusted)
- Weekly/monthly derived locally from daily (calendar aggregation, no extra calls)
- Quota: all periods of one symbol = 1 historical-quota slot, so 1m + daily backfill
  for a symbol costs a single slot; verify intraday history depth limit empirically

- OpenD is a **local gateway process** that maintains the upstream connection to
  moomoo servers; clients talk to it locally. It exposes **two client interfaces**:
  raw TCP (default `127.0.0.1:11111`, custom binary framing + protobuf bodies — used
  by the Python/Java/C#/C++ SDKs) and a **WebSocket port** (same protobuf messages,
  used by the JS SDK; optional MD5 auth key + TLS). Each request/response pair is
  keyed by a protocol ID (e.g. `Qot_Sub` = 3001). Push frequency is configurable in
  OpenD (milliseconds).
- **No official Go SDK, and none needed** — Go can speak the protocol directly.
  Verified from the installed Python SDK (2026-07-03):
  - All **167 `.proto` files** ship in the SDK at
    `$(python3 -c 'import moomoo,os;print(os.path.dirname(moomoo.__file__))')/common/pb/`
    → compile to Go with `protoc-gen-go`.
  - TCP framing is a simple **44-byte packed little-endian header**
    (struct fmt `<1s1sI2B2I20s8s`): `"FT"` magic, u32 protoID, u8 fmtType
    (0=protobuf, **1=JSON**), u8 protoVer, u32 serialNo, u32 bodyLen,
    20-byte body SHA1, 8 reserved bytes. Body encryption is optional and off by
    default on localhost.
  - Lifecycle: `InitConnect` (1001) handshake first (returns connID + keepalive
    interval) → `KeepAlive` (1004) heartbeat → request/response correlated by
    serialNo; pushes dispatched by protoID.
  - Leading option: **raw TCP + generated Go protobuf** (framing is ~200–300 lines
    of Go). OpenD's WebSocket port is the fallback if TCP framing surprises.
  - Rule: the Go client must NOT implement `unlock_trade` — live unlock stays
    manual in the OpenD GUI.
- Quota rules that shape the ingestion design: subscriptions cost 1 quota per stock per
  subtype; minimum 1 minute before unsubscribing; historical K-line has its own quota.
  Details: `.claude/skills/moomooapi/docs/API_LIMITS.md`.
- **No sub-minute K-line** (smallest is 1m) — but Earl requires **10s charts**, so the
  engine builds them by aggregating TICKER pushes (ms timestamps, price, volume,
  BUY/SELL/NEUTRAL direction). Design: ticks are the primary feed (T&S + sub-minute
  bars + buy/sell delta); K_1M subscription covers ≥1m timeframes via local
  aggregation (1 quota slot instead of one per period) + validation against
  tick-derived 1m bars. Tick backfill is limited to ~1,000 recent ticks
  (`get_rt_ticker`) — measured 2026-07-03: that is **<1s of history on AAPL**
  (closing burst) and ~19s on KO, i.e. cold symbols start with effectively no 10s
  history. Mitigation: **pre-subscribe TICKER for the day's watchlist at session
  start** and persist ticks (SQLite) so 10s history spans the session and survives
  restarts; cold symbols show 1m context + 10s bars growing from subscribe time.
  Bucket by exchange timestamp, not arrival time.

**Pre-market gap scanner (researched + verified 2026-07-03):** moomoo covers it natively,
no external API. Pre-market rank `Qot_GetUSPreMarketRank` (3410, 60 req/30s) gives the whole
US universe sorted by pre-market % with pre-market price/volume/turnover; V1 screener
`Qot_StockFilter` (3215) filters by `FLOAT_SHARE` (⚠️ unit = **thousands** of shares) for the
daily low-float universe; snapshot `outstandingShares` = **true free float** (DJT-verified).
All request APIs — zero subscription quota. Design + caveats (batch-failing OTC codes, coarse
server filters, holiday-data verification): `docs/2026-07-03-premarket-scanner-api.md`.
Verify live refresh latency Monday pre-market.

## moomoo skills (project-local, `.claude/skills/`)

Installed from moomoo's official package (v2.1.0), security-reviewed 2026-07-03:

- **`moomooapi`** — 170+ runnable Python scripts (quotes, K-line, order book, ticker,
  subscribe/push, trading) + condensed docs (`API_REFERENCE.md`, `API_LIMITS.md`,
  `FIELD_MAPPING.md`, `TROUBLESHOOTING.md`). Use these to capture ground-truth payloads
  when designing Go structs and to debug eTape output against known-good SDK output.
  Scripts read `FUTU_*` env vars and default to `FUTU_TRD_ENV=SIMULATE`; they refuse to
  unlock real trading via SDK (unlock only in the OpenD GUI).
- **`install-moomoo-opend`** — installs OpenD + Python SDK. OpenD is installed and
  verified working (2026-07-03).

## Verified environment (2026-07-03)

- OpenD running on `127.0.0.1:11111`, quote + trade logged in; SDK `moomoo-api`
  10.8.6808 installed under pyenv `python3`.
- **Quote entitlements**: US **LV3** (full depth order book + ticks — the DOM works),
  HK **LV1** (verified: quotes + TICKER + **1-level** book — charts/T&S yes, DOM no),
  crypto LV1, SG/MY/JP stocks: none. → eTape's L2 ladder is **US-market-first**.
- **10s-bar aggregation verified live** (2026-07-03, HK.00700 mid-session): 7 complete
  bars over 75s via TICKER push — OHLC continuity, volume, tick count, buy/sell delta
  all correct. Reference implementation for the Go port:
  `prototypes/tick_to_10s_bars.py` (exchange-timestamp bucketing, watermark emission
  on next-bucket tick, first-push cache seeding).
- **Quotas**: base tier — 100 subscription slots, 100 historical K-line slots.
- **Accounts**: real FUTUSG margin (auth: HK/US/SG/JP + funds), paper HK cash,
  paper US margin (`STOCK_AND_OPTION`, needs `refresh_cache=True` on queries).
- Verified pipeline: snapshot, subscribe (QUOTE/ORDER_BOOK/TICKER), 10-level book,
  tick-by-tick with ms timestamps and BUY/SELL/NEUTRAL direction.
- Search skills (news/digest/sentiment) and anomaly skills — peripheral to core eTape.

## Official API docs

https://openapi.moomoo.com/moomoo-api-doc/en/intro/intro.html — authoritative reference
(protocol IDs, protobuf definitions, error codes, permission tiers). Pages are fully
server-rendered and navigable via WebFetch; use `curl` for lossless detail (protobuf
field tables, enums). Prefer the local skill docs for quick lookup, the official docs
when implementing the wire protocol.

## Open questions (design phase)

- **Execution broker: TradeZero vs Alpaca** — Alpaca researched 2026-07-03
  (`docs/2026-07-03-alpaca-api.md`): ~70–90 ms faster warm round trip than TZ from
  Earl's machine, native order replace, server-side brackets/OCO, HTB locates API,
  $0 commission; but 200 req/min pool and no live account yet (paper keys verified,
  `~/.eJournal/credentials.json` key `alpaca`). Alpaca market data = no L2 depth, so
  the moomoo DOM stays either way. Decide after the Monday order-latency benchmark
  (run both brokers' paper APIs in one session).
- Go protocol client vs. sidecar bridge for OpenD (see above)
- Historical OHLCV storage (likely SQLite)
- Backtesting scope
- Order-management safety rules (kill switch, max position, duplicate-order guards)
- Indicator set for v1
- News aggregation source — options + recommendation in
  `docs/2026-07-03-news-aggregation-options.md` (lean: poll `Qot_GetSearchNews`
  from the Go engine for v1; news is poll-only, no push subtype exists)
