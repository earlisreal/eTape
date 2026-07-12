# eTape

Personal trading platform ("reading the tape"): a local app consuming broker market-data
feeds and rendering candlestick charts, a Level 2 DOM ladder, and time & sales.
Priorities: runtime speed, execution, stability. All code is AI-written, so
reviewability and compiler-enforced safety weigh heavily.

Full stack rationale: `docs/2026-07-03-stack-decision.md`. Approved designs:
execution/portfolio (`docs/superpowers/specs/2026-07-03-portfolio-orders-design.md`),
UI (`docs/superpowers/specs/2026-07-03-ui-design.md`),
Go engine (`docs/superpowers/specs/2026-07-03-go-engine-design.md`),
multi-broker execution
(`docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md` — revises the
execution spec: venues, two-layer gate, TZ + Alpaca v1, moomoo v1.x).

## Workflow conventions

- **Auto-commit approved specs and plans.** When a design spec or an implementation
  plan is approved, commit the artifact automatically, in the same turn — don't wait
  to be asked.
  - Specs live in `docs/superpowers/specs/` → commit as `docs(specs): <description>`.
  - Plans live in `docs/superpowers/plans/` → commit as
    `docs(plans): add <feature> implementation plan`.
  - This covers committing the *artifact* once it's approved — it does **not** mean
    auto-starting the next phase (still don't jump from an approved spec into writing
    a plan unprompted), and it does **not** mean pushing — pushing stays a separate,
    explicit step.
- **Default to subagent-driven execution in a worktree.** When executing an approved
  implementation plan (or a batch of independent tasks) and Earl hasn't said how,
  default to `superpowers:subagent-driven-development` with the work running in an
  isolated git worktree (`superpowers:using-git-worktrees`) — don't edit the main
  checkout inline. Earl runs many parallel sessions against shared checkouts, so
  worktree isolation is the safe default. Trivial one-offs (a typo, a single-line
  fix) may stay inline, and Earl can always override per task.
- **At the end of worktree work, ask before merging.** At the end of such work, if
  verification passes (tests/build green — `superpowers:verification-before-completion`)
  and there are no unresolved review problems or merge conflicts, ask Earl whether he
  wants to merge the branch into local `main` and clean up the worktree
  (`superpowers:finishing-a-development-branch`) — do not merge automatically. If
  anything is wrong, stop and surface it instead of offering to merge. As with the
  auto-commit rule above, **pushing stays a separate, explicit step** — merge and
  worktree cleanup are local only.
- **Guard against subagents committing to `main`.** Subagents have repeatedly landed
  stray commits directly on shared `main` instead of their assigned worktree (a bare
  "Work from: `<dir>`" line in a dispatch prompt is not a strong enough gate). Two
  layers, both already in place:
  - **Mechanical backstop:** `.githooks/commit-msg`, wired in via
    `git config core.hooksPath /Users/earl.savadera/Projects/eTape/.githooks` (a local,
    uncommitted config — re-run this once after a fresh clone). It blocks any plain
    commit whose current branch is `main`, except: commits on any other branch
    (worktrees), an in-progress merge, a message starting with `docs(specs):` or
    `docs(plans):` (the auto-commit rule above), or an explicit
    `ETAPE_ALLOW_MAIN_COMMIT=1 git commit ...` override for a deliberate one-off main
    commit. Set the env var inline on the command — never export it into a subagent's
    shell.
  - **Dispatch discipline:** open every implementer/fix-subagent dispatch prompt in a
    worktree-based session with the verbatim checklist in
    `.claude/dispatch/implementer-first-step.md` (pwd/branch verification, STOP on
    mismatch) — paste it in rather than re-deriving a bare "Work from:" line.

## Stack (decided)

- **Engine:** Go — feed parsing, book building, indicators, order logic
- **UI:** TypeScript + React + Vite; TradingView Lightweight Charts for candles;
  custom canvas for the L2 ladder; virtualized list over a ring buffer for time & sales
- **Engine ↔ UI:** WebSocket + JSON over localhost; TS types generated from Go structs (`tygo`)
- **Packaging:** browser tab first; Wails v3 later (Electron fallback)
- **Rule:** high-frequency data never flows through React state — chart/ladder/tape are
  canvas surfaces painted imperatively, coalesced to one repaint per rAF tick

## Market data: moomoo OpenD (primary source)

moomoo OpenD is the primary source of quotes, ticks, and order-book data.
**Execution (decided 2026-07-04): multi-broker** — TradeZero + Alpaca adapters in v1,
moomoo as a third execution venue in v1.x (its paper env can't validate fills); every
order names its venue explicitly; broker-selection UX deferred to a later UI design
revision. Spec: `docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md`;
moomoo trading API research: `docs/2026-07-04-moomoo-trading-api.md`.
TradeZero API fully researched 2026-07-03 —
`docs/2026-07-03-tradezero-api.md` (REST + WebSocket, order model, locates, rate
limits, eTape design consequences) + reconstructed OpenAPI spec in `docs/tradezero/`;
confirmed TradeZero exposes **no market data**, so the moomoo/TradeZero split stands.
TradeZero API credentials live in `~/.eTape/credentials.json` (key `tradeZero`:
`keyId` + `secretKey`) — eTape owns this file (alongside `~/.eTape/config.toml`); it
is **no longer shared with eJournal** (decided 2026-07-09, reversing the
settings-redesign spec's §6 choice to share `~/.eJournal/credentials.json`). The
store started **empty** when this change landed, so Earl's existing live TradeZero
and `alpaca-live` keys are **not** automatically available — they must be re-entered
through the Venues & credentials settings UI before those venues will boot.
**Verified 2026-07-03 (of the eJournal-era keys): these are LIVE-account keys**
(`accountType: "Live"`, real funds). Alpaca live keys added 2026-07-07 (key
`alpaca-live`, limited-margin account, real funds; paper keys stay under `alpaca`).
**Safety rule: never place, modify, or cancel real orders with live keys (TradeZero
or `alpaca-live`) unless Earl explicitly says so in the current conversation.**
Read-only endpoints (accounts, positions, orders, routes, pnl) are fine for
verification.
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
  - **Decided (2026-07-03, engine design): raw TCP + generated Go protobuf**
    (framing is ~200–300 lines of Go). OpenD's WebSocket port is the fallback if
    TCP framing surprises.
  - Rule (amended 2026-07-04): eTape never implements `Trd_UnlockTrade` (2005) —
    the trade password never touches eTape. Unlock is per-OpenD-process and happens
    outside the engine (verified 2026-07-06: the OpenD GUI exposes an unlock
    control — runbook is GUI unlock once per OpenD restart). The **feed connection implements no
    `Trd_*` protocols**; order writes live only in the separate `broker/moomoo`
    adapter connection (v1.x, multi-broker spec).
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

- **Execution broker — resolved 2026-07-04: multi-broker, all three** (TZ + Alpaca +
  moomoo as configured venues; multi-broker spec). Alpaca research:
  `docs/2026-07-03-alpaca-api.md` (paper keys `alpaca`; live account funded
  2026-07-07, keys `alpaca-live`, limited margin; no L2 depth, so the moomoo DOM
  stays). Order-latency benchmarks DONE — 2026-07-06 three-venue session (TZ live +
  Alpaca paper + moomoo live) and 2026-07-07 Alpaca-live addendum:
  `docs/2026-07-06-venue-latency-benchmark.md`. Real fills: **Alpaca ~0.23 s <
  TZ 0.33–0.44 s < moomoo 0.9–1.0 s**; ack ordering the same. Live-leg
  guardrails (standing): 1-share marketable limits, cheap liquid symbol, long only,
  flatten immediately, RTH only, moomoo trade unlock done outside eTape first —
  **re-confirm authorization in the session that runs it** (safety rule above
  still applies). **Known unresolved gap (flagged 2026-07-11 final review):**
  moomoo's `AccountSnapshot.DayPnL` is always 0 (`Trd_GetFunds` has no
  day-P&L field, no ledger-derived alternative built yet) — the global
  `MaxDayLoss` circuit breaker does not see moomoo losses; do not live-arm
  moomoo as the primary/only venue until this is addressed. Details:
  `docs/2026-07-04-moomoo-trading-api.md`.

Closed by the three approved designs (2026-07-03): OpenD client = raw TCP + Go
protobuf (engine design); market-data storage = always-on SQLite feed journal +
1m/daily bar archives, everything else derived (engine design; supersedes
"persist ticks" above — the journal also records book/quote/bar1m); backtesting =
record from day one, interactive practice mode v1.5 on replay+SimBroker seams,
algorithmic backtesting deferred (engine design); order-safety rules = gate envelope
(execution design); indicator set v1 = VWAP, EMA, SMA, MACD, volume, buy/sell delta
(UI design); news = poll `Qot_GetSearchNews` from the engine (engine design, per
`docs/2026-07-03-news-aggregation-options.md`).
