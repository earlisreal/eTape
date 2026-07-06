# eTape — Alpaca API Research

**Date:** 2026-07-03
**Status:** Research complete; candidate **execution broker** (vs TradeZero) and optional
secondary market-data source. Decision pending order-latency benchmarks.
**Sources:** https://docs.alpaca.markets — fully navigable; every page serves raw
markdown by appending `.md`, and https://docs.alpaca.markets/llms.txt is a complete
page index (use `/us/` paths; other regions like `in-giftcity` are irrelevant).
API reference pages embed full JSON schemas in the `.md` output.

## Role in eTape

Alpaca is a US retail broker with a first-class Trading API (REST + WebSocket), a
separate Market Data API, and optional low-latency tiers (FIX 4.2, DMA). Earl is
leaning toward Alpaca for **order execution** if it beats TradeZero on speed; its
market data is a possible supplement, **not** a moomoo replacement (no stock depth-of-
book — see Market Data below, DOM ladder stays on moomoo LV3).

## Auth & environments

- Live trading `https://api.alpaca.markets`, paper trading
  `https://paper-api.alpaca.markets` — **separate base URLs and separate key pairs**
  (unlike TradeZero where the key selects the environment). Market data for both:
  `https://data.alpaca.markets`.
- Auth: `APCA-API-KEY-ID` + `APCA-API-SECRET-KEY` headers (or HTTP Basic). The newer
  OAuth2 client-credentials flow (`authx.alpaca.markets/v1/oauth2/token`) is
  **Broker-API-only for now** — "not yet available for Trading API". eTape uses headers.
- Every response carries `X-Request-ID` — persist recent ones for support tickets.
- All accounts open as margin accounts; ≥$2,000 equity unlocks margin + shorting.
  `multiplier` field: 1/2/4 (4 = 4x intraday, 2x overnight Reg T).
- **PDT is dead**: FINRA's revised Rule 4210 (intraday margin regime) replaced
  pattern-day-trader rules; Alpaca deprecated PDT fields June 4 2026 and sunsets PDT
  endpoints July 6 2026. No $25k day-trading floor.

### Credentials (verified 2026-07-03)

`~/.eJournal/credentials.json` key `alpaca` (`keyId`/`secretKey`) is a **paper**
account: `PA3IC96WKTXD`, ACTIVE, $100k equity, multiplier 4, shorting enabled,
crypto active. The live endpoint rejects these keys (40110000) — **no live Alpaca
keys/account exist yet**; live onboarding (individuals worldwide) would be needed
before real orders. Safe default: all development against paper.

## Measured latency (2026-07-03, market closed, from Earl's machine)

Warm keep-alive HTTPS request→response (`/v2/clock`, 5-request series):

| Endpoint | Warm round trip | Jitter |
|---|---|---|
| `api.alpaca.markets` / `paper-api` | **~210–214 ms** | ±2 ms, very consistent |
| `webapi.tradezero.com` | ~272–301 ms | outliers to 755 ms |

- Alpaca is **~70–90 ms faster per request and far more consistent**. TradeZero
  terminates TLS at a nearby edge (TCP connect ~34 ms) but proxies to a distant
  origin; Alpaca connects straight to US-East (~105 ms one-way).
- Authenticated paper `/v2/clock` ≈ unauthenticated 401 time → server processing is
  negligible; the wire dominates. Cold TLS setup to Alpaca is ~430 ms → the Go client
  **must keep a warm connection pool** (and consider periodic keepalive requests).
- Still unmeasured: order `POST → trade_updates ack/fill` on a live session. Extend
  Monday's TradeZero benchmark script to hit Alpaca paper in the same run.

## REST surface (Trading API, `/v2` unless noted)

| Area | Endpoints | Notes |
|---|---|---|
| Account | `GET /account` | equity, buying_power, multiplier, shorting_enabled, daytrade_count |
| Orders | `POST /orders` · `GET /orders` · `GET /orders/{id}` · `GET /orders:by_client_order_id` · **`PATCH /orders/{id}`** · `DELETE /orders/{id}` · `DELETE /orders` (cancel-all) | native replace — TradeZero has none |
| Positions | `GET /positions` · `GET /positions/{symbol}` · `DELETE /positions/{symbol}` · `DELETE /positions` (close-all) | close-all = liquidate everything, optional cancel_orders flag |
| Assets | `GET /assets` · `GET /assets/{symbol}` | `tradable`, `shortable`, `easy_to_borrow`, `borrow_status`, `ipo`, `overnight_tradable` |
| Locates | `GET /v1/locates/quotes?symbols=` · `POST /v1/locates` · `GET /v1/locates` · `GET /v1/locates/{id}` | HTB shorting (new; historically Alpaca was ETB-only) |
| Clock/Calendar | `GET /clock` · `GET /calendar` | session awareness |
| Activities | `GET /account/activities[/{type}]` | fills, fees, corporate actions |

## Order model

- **Types:** `market`, `limit`, `stop`, `stop_limit`, `trailing_stop`
  (trail_price/trail_percent vs high-water mark; PATCH `trail` to adjust).
- **Order classes:** `simple`, **`bracket`** (entry + take-profit + stop-loss,
  server-side OTOCO), **`oco`**, **`oto`**, `mleg` (options). Bracket/OCO legs are
  adjusted on partial fills and support PATCH of limit/stop prices. Server-side
  exits are a major safety win vs TradeZero (where eTape would babysit exits
  client-side).
- **TIF:** `day`, `gtc`, `opg` (MOO/LOO), `cls` (MOC/LOC), `ioc`, `fok`. ⚠️ The TIF
  table footnote marks IOC/FOK/OPG/CLS "contact sales" — these are Elite premium
  order types now; assume day+gtc only on a standard account until verified.
- **Extended hours:** `extended_hours: true`, **limit orders with day/gtc only**
  (market orders rejected — same coercion rule as TradeZero). Sessions: pre 04:00–09:30,
  post 16:00–20:00, plus **overnight 20:00–04:00 ET Sun–Fri via Blue Ocean ATS**
  (24/5; limit-only; enabled by default; TradeZero has no overnight session).
- **Replace:** `PATCH /v2/orders/{id}` updates qty/limit/stop/trail/TIF —
  **chart-drag amendment is one call** (vs TradeZero cancel→poll→re-place).
  Not replaceable: notional orders, DMA orders; cancel is rejected while
  `pending_replace`.
- **`client_order_id`:** ≤128 chars, unique, auto-generated if omitted; queryable via
  dedicated endpoint. (Whether IDs are permanently consumed after cancel like
  TradeZero's R114 — verify empirically.)
- **Statuses:** `new → partially_filled → filled`, `done_for_day`, `canceled`,
  `expired`, `replaced`, `pending_cancel`, `pending_replace`; rare: `accepted`,
  `pending_new`, `accepted_for_bidding`, `stopped`, `rejected`, `suspended`,
  `calculated`. Cancelable until filled/canceled/expired.
- **Validation gotchas:** sub-penny rejection (≥$1 → 2 dp, <$1 → 4 dp, code
  42210000); short-sell buying-power check uses MAX(limit, 1.03×ask); stop-loss legs
  must be ≥$0.01 through the base price; GTC orders auto-canceled after 90 days
  (`expires_at`); fractional = market/limit day-only, never replaceable.
- Errors are structured JSON (`{"code": 42210000, "message": ...}`) with proper HTTP
  statuses — no TradeZero-style "HTTP 200 but rejected" trap on validation, though
  exchange rejections still arrive async as `rejected` events.

## WebSocket: `trade_updates` (fill-event source)

`wss://{paper-}api.alpaca.markets/stream` — same key/secret auth
(`{"action":"auth","key":…,"secret":…}`), then
`{"action":"listen","data":{"streams":["trade_updates"]}}`. JSON or MessagePack;
⚠️ **paper endpoint sends binary frames** (live sends text) — the Go client must
handle both.

Events carry `event`, the **full order object**, and per-execution fields — this maps
directly onto eTape's broker-agnostic fill event:

- `fill` / `partial_fill`: `price`, `qty` (this execution), `timestamp`,
  **`position_qty`** (position after the event — free position reconciliation),
  `execution_id`; order object carries `filled_avg_price`, `filled_qty`.
- `new`, `canceled`, `expired`, `replaced`, `rejected`, `done_for_day`, plus rare
  `pending_*`/`stopped`/`suspended`/`calculated`, `order_replace_rejected`,
  `order_cancel_rejected`.

Cleaner than TradeZero's Portfolio stream: explicit event types, no WS↔REST field-name
drift, position delta included. In-stream errors send `{"action":"error"}` then close —
client owns reconnect + REST re-snapshot, same as the TZ adapter design.

## Short selling & locates

- **ETB:** 5,000+ symbols, **$0 locate and borrow fees** for Trading API users — just
  short, no locate step. Check `GET /v2/assets/{symbol}` → `borrow_status`.
- **HTB:** eligible margin accounts only; locate required first:
  `GET /v1/locates/quotes` (advisory, mixed ETB/HTB lists return per-symbol errors) →
  `POST /v1/locates` (`qty` in round lots of 100, optional `limit_price` fee cap,
  `all_or_none`) → response is immediately `active` with `located_qty`, `total_fee`,
  `expires_at` (~01:00 ET next day). **Single-use** (covering doesn't replenish),
  non-refundable, fees separate from daily borrow rate.
- Simpler than TradeZero's flow (no 30s accept window, no ~2s status polling, no
  offered/expired state machine) — synchronous request/response.
- Margin: 2x overnight / 4x intraday (≥$2k equity); overnight maintenance table by
  price band; concentration rule (≥70% single symbol + ≥$100k debit → 50% maint);
  margin interest 6.25% (4.75% Elite T2) on overnight debit only — intraday leverage
  is free, same as TZ economics.

## Rate limits

- **Trading API: 200 requests/min per account** (pooled across all endpoints;
  429 on excess). Elite: 1,000/min. Compare TradeZero: per-endpoint buckets with
  10/s order POST + 15/s cancel — TZ allows much higher order bursts, but 200/min
  (~3.3/s sustained) is ample for discretionary trading; rely on `trade_updates`
  instead of polling and the budget is a non-issue. Kill switch = one
  `DELETE /v2/orders` + optionally `DELETE /v2/positions`.
- Market data: Basic 200/min, Algo Trader Plus 10,000/min (historical REST).

## Market Data API (secondary option — not a moomoo replacement)

- **No depth-of-book for stocks.** Stream channels: `trades`, `quotes` (**NBBO
  top-of-book only**), `bars`/`updatedBars`/`dailyBars`, `statuses` (halts), `lulds`,
  `corrections`, `cancelErrors`. **The L2 DOM ladder must stay on moomoo LV3.**
- Plans (Trading API): **Basic (free)** — real-time from **IEX only** (~2–3% of
  volume; too sparse for real T&S), 30 WS symbols, SIP data delayed ≥15 min,
  200 req/min. **Algo Trader Plus $99/mo** — full consolidated **SIP** real-time
  (CTA+UTP, 100% of volume), unlimited WS symbols, 10k req/min, history since 2016
  with no recency restriction. Verified on Earl's paper keys: IEX works
  (`"x":"V"`), `feed=sip` → "subscription does not permit".
- Stream: `wss://stream.data.alpaca.markets/v2/{iex|sip|test}`; JSON or MessagePack;
  **most users get 1 concurrent data connection** (error 406 "connection limit
  exceeded"). Overnight-session data via `feed=overnight` (BOATS).
- SIP trades stream = every consolidated trade with conditions → could build T&S and
  10s bars, **but no BUY/SELL/NEUTRAL direction field** (moomoo provides it); aggressor
  side would have to be inferred from prevailing NBBO (quote rule). Bars follow SIP
  condition rules (odd-lot `I` trades update volume only, etc.).
- Realistic uses if Alpaca becomes the broker: cross-validating moomoo bars,
  unlimited-depth 1m/daily history backfill (no moomoo 100-slot quota), halt/LULD
  status feed. Not needed for v1 if moomoo LV3 works.

## Elite tier (optional latency/feature unlock)

- **Tier 1 — $30k deposit:** Elite Smart Router, **1,000 req/min**, premium order
  types (IOC/FOK/MOO/MOC/LOO/LOC, VWAP, TWAP, midpoint peg), **DMA Gateway** (direct
  routing via DASH to NYSE/NASDAQ/ARCA — `advanced_instructions: {algorithm:"DMA",
  destination:"NYSE", display_qty:…}`; market/limit day-only; no replace; ext-hours on
  NASDAQ/ARCA), **FIX 4.2 order entry** (credentials via support; connectivity
  ~03:30–20:15 ET Mon–Fri).
- **Tier 2 — $100k:** margin 4.75%, free Algo Trader Plus, white-glove support.
- Elite Smart Router execution is **not free**: $0.0040/share all-in, or cost-plus
  $0.0025–$0.0005/share + exchange fees/rebates. Standard retail mode stays
  $0-commission (wholesale/market-maker routing, i.e. PFOF-style) + regulatory
  pass-throughs (SEC + TAF on sells, CAT both sides).
- eTape stance: start standard retail; Elite T1 is the upgrade path if order-ack
  latency or routing control becomes the bottleneck (analogous to TZ direct routes).

## Paper trading caveats

- Fills simulated against NBBO when marketable; random 10% partial fills; **no**
  queue-position, slippage, market-impact, or borrow-fee simulation (borrow fees
  "coming soon"). Good for protocol/latency testing, not execution-quality testing.
- Paper accounts are created/deleted from the dashboard (no more reset); new account
  = new keys. Order-ack latency on paper measures the API path only — live fills go
  to real venues and will differ.

## Design consequences for eTape

1. **Broker-agnostic fill events**: `trade_updates` maps more cleanly than TradeZero's
   Portfolio stream (explicit event types, per-execution price/qty, `position_qty`
   delta included, no field-name quirks). Adapter must handle paper=binary/live=text
   frames and msgpack.
2. **Chart-drag order amendment**: native PATCH replace removes the TZ
   cancel→poll→re-place dance and its in-between UI state — one call, one
   `replaced` event.
3. **Server-side brackets/OCO/OTO**: stop-losses survive an eTape crash or network
   drop. With TZ this safety lives in eTape. Strong argument for Alpaca on the
   stability priority.
4. **Kill switch**: `DELETE /v2/orders` + `DELETE /v2/positions` (cancel-all +
   flatten-all) — richer primitive than TZ's cancel-all-only.
5. **Latency**: Alpaca already wins the network floor by ~70–90 ms with far less
   jitter. Keep-alive pool mandatory (cold TLS ~430 ms). Order-ack benchmark Monday
   decides.
6. **Locates**: synchronous and simpler than TZ; ETB shorts are zero-fee zero-step.
7. **Rate limits**: single 200/min pool — no REST polling loops; WS-first state,
   REST only for reconnect re-snapshot (same architecture as planned for TZ).
8. **No live account yet**: paper-first development is free; live onboarding is the
   long pole if Alpaca wins the benchmark.

## To verify empirically

Verified 2026-07-06 pre-market on paper (`prototypes/alpaca_side_checks.py`,
raw: `prototypes/captures/alpaca_side_checks_*.json`):

- ✅ **TIFs on a standard account**: `fok`/`opg`/`cls` all **accepted**
  (`pending_new`); `ioc` rejected with 42210000 *"ioc orders are only accepted
  during market hours"* — a time gate, **not** an Elite entitlement gate.
  (IOC re-verified during the RTH benchmark session.)
- ✅ **`client_order_id` reuse: permanently consumed** — re-place after `canceled`
  → 422 `40010001 "client_order_id must be unique"`. Same semantics as TZ R114:
  eTape must always mint fresh ids.
- ✅ **Paper stream encoding: plain JSON inside binary frames** (14/14 frames;
  no msgpack). Go client: JSON-decode both text and binary frames.
- ✅ Cold TLS reconfirmed: first `GET /account` 583 ms (2026-07-06); keep-alive
  pool stands. `DELETE /v2/orders` (cancel-all) returns **HTTP 207** multi-status.
- Order lifecycle even pre-market: `pending_new` → `new` events arrive on
  `trade_updates` within the first seconds; `canceled` confirmed per order.

Still open:

- Order POST→`new` ack and →`fill` latency vs TZ/moomoo (Monday RTH benchmark —
  script ready: `prototypes/venue_order_latency_bench.py`)
- `trade_updates` granularity: one `partial_fill` event per execution (docs imply yes)
- Real fill quality of $0-commission wholesale routing vs TZ SMART/direct (live only)
- Data stream: whether Algo Trader Plus lifts the 1-connection limit
