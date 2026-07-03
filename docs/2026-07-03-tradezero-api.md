# eTape — TradeZero API Research

**Date:** 2026-07-03
**Status:** Research complete; integration not started (execution planned after moomoo data pipeline)
**Sources:** https://developer.tradezero.com — docs guides fetched directly; REST schemas
decoded from the site's Docusaurus JS chunks (each endpoint page embeds its OpenAPI
operation as zlib-compressed base64 in `assets/js/*.js`; no official spec download
exists). Reconstructed spec: [`tradezero/tradezero-openapi.json`](tradezero/tradezero-openapi.json).
Changelog shows monthly releases since Nov 2025; docs refreshed 2026-05-14.

## Role in eTape

TradeZero is the **execution broker**. Confirmed: the API has **no market data
endpoints on either environment** — validating the split of moomoo OpenD for data,
TradeZero for orders. The Portfolio WebSocket stream is the source for eTape's
broker-agnostic fill events (symbol, side, qty, price, timestamp).

## Auth & environments

- REST base `https://webapi.tradezero.com`, WebSocket base `wss://webapi.tradezero.com/stream`.
- Two headers on every request: `TZ-API-KEY-ID` (public) + `TZ-API-SECRET-KEY`
  (shown once at generation). Keys don't expire; rotate via portal.
- **Same base URL for paper and live** — the key pair selects the environment.
- Keys: portal → Platform Selection → enable "API Trading" add-on → sign agreement →
  generate. Paper portal has a simpler "Enable API Trading" flow.
- Paper accounts: $1M virtual, 30-day life (permanent if linked to live), everything
  easy-to-borrow, no real locate inventory, **no order history endpoints**, no
  IOC/FOK/OPG TIFs, routes auto-assigned (`PAPER`/`PAPERM`).
- Detect environment via `accountType` field (`"Paper"`/`"Live"`/`"Margin"`/`"Cash"`),
  never by account-ID pattern. Use the `2TZ`-prefixed account number in paths, not the
  login ID; get canonical IDs from `GET /v1/api/accounts`.

## REST surface (19 endpoints, all under `/v1/api`)

| Area | Method & path | Notes |
|---|---|---|
| Accounts | `GET /accounts` · `GET /account/{accountId}` | equity, buyingPower, availableCash, leverage, sodEquity, accountStatus |
| P&L | `GET /accounts/{id}/pnl` | account aggregates + per-position realized/unrealized (REST twin of the P&L stream) |
| Positions | `GET /accounts/{id}/positions` | positionId, side Long/Short, shares, priceAvg, dayOvernight |
| Routes | `GET /accounts/{id}/routes` | per-route allowed orderTypes/TIFs/securityTypes — query, don't hardcode |
| Orders | `POST /accounts/{id}/order` · `GET /accounts/{id}/orders` (today) · `GET /accounts/{id}/order/{orderId}` | |
| Cancel | `DELETE /accounts/{id}/orders/{clientOrderId}` · `DELETE /accounts/orders[?symbol=]` (cancel-all) | cancel-all body needs `account=` form field |
| History | `GET /accounts/{id}/orders/start-date/{date}` (≤1 wk) · `.../orders-with-pagination/start-date/{date}` (≤365 d) | **fill-level rows** (tradeId, qty, price, commission, fees, proceeds); live accounts only |
| Borrow | `GET /accounts/{id}/is-easy-to-borrow/symbol/{sym}` | |
| Locates | `POST /accounts/locates/quote` · `POST .../accept` · `POST .../sell` · `DELETE .../cancel/...` · `GET /accounts/{id}/locates/history` · `.../inventory` | |

Full request/response schemas in [`tradezero/tradezero-openapi.json`](tradezero/tradezero-openapi.json).

## Order model

**`POST /order` request** — required: `symbol`, `orderQuantity` (int, 1–1,000,000),
`orderType` (`Limit|Market|Stop|StopLimit`), `timeInForce`, `securityType`
(`Stock|Option|Mleg`). Plus `side` (`Buy|Sell`), `openClose` (`Open|Close`),
`limitPrice`/`stopPrice` when the type needs them, `route`, `clientOrderId`.
All enums **case-sensitive**; wrong case → 400 (or 200/Rejected for `openClose`).

**Trader actions** map to `side`+`openClose`: Buy=`Buy/Open`, Sell=`Sell/Close`,
Short=`Sell/Open`, Cover=`Buy/Close`. Never send `SellShort` — but **responses**
enrich `side` to `SellShort` for short orders (Cover must be derived from `openClose`).

**Order statuses:** `PendingNew → New → PartiallyFilled → Filled`, with
`PendingCancel → Canceled`, `Rejected`, `Expired`, `DoneForDay`. Terminal:
Filled/Canceled/Rejected/Expired.

**TIFs:** `Day`, `GoodTillCancel`, `Day_Plus` (ext-hours, Limit only), `GTC_Plus`
(ext-hours multi-session, Limit only), `AtTheOpening`, `ImmediateOrCancel`,
`FillOrKill` (direct routes only), `GoodTillCrossing` (deprecated on SMART).
Extended hours (04:00–09:30, 16:00–20:00 ET): market orders rejected (`R78`) — coerce
Market→Limit at last price and Day→`Day_Plus`.

**Routes (live):** `SMART` (stock, widest support, icebergs), `CTDL` (direct, TrailStop),
`ARCA` (ECN, adds IOC/FOK), `SMARTO` (single-leg options), `SMARTM` (multi-leg, Day only).
Live orders without an explicit `route` can fail at execution — always send one, from
`/routes`.

**`clientOrderId`:** the primary order key (server `orderId` was removed 2026-01).
≤36 chars recommended; **permanently consumed once accepted, even after cancel** —
reuse → `R114` rejection (free duplicate-order guard). Omitted → server generates
`MMDDHHmmssSSS.NNNN`. **No modify endpoint** — cancel, poll until `Canceled`, re-place
with a fresh ID.

### Error handling gotchas (all confirmed in docs)

- **`POST /order` always returns HTTP 200** — read `orderStatus`; rejection reason in
  `text` as `"R##: …"`. Key codes: `R78` market order outside RTH, `R95` simultaneous
  opening buy+sell on one symbol, `R114` duplicate clientOrderId, `R54` unreachable route.
- Schema violations → 400 with a **plain-text** bullet body (not JSON).
- Auth failures are inconsistent: most endpoints 404, cancel-by-id 401.
- Cancel immediately after place can 404 (order not yet registered) — brief delay/poll first.
- Stop/limit price coherence is not validated — an inverted StopLimit sits unfilled.
- Rejected orders may show `route: "<no value>"`.

## WebSocket streams (beta)

Two independent endpoints, same three-step handshake:
connect → server sends `{"@system":true,"status":"PENDING_AUTH"}` (re-sent every ~5s)
→ client sends `{"key":…,"secret":…}` → `CONNECTED` → send subscribe payload.
Other statuses: `FAILED_AUTH` (don't retry), `TERMINATED`/`INVALID_DATA` (connection
stays open — fix and resend subscribe). **No server ping/pong** — client must detect
dead connections and reconnect with jittered exponential backoff (docs suggest 1s–30s).

**Portfolio stream** (`/stream/portfolio`) — subscribe
`{"accountId":…,"subscriptions":["Order","Position"]}`. Pushes `action:"update"`
messages carrying a full order object (state changes, fills, cancels) or position
object. **This is eTape's fill-event source.** Recommended snapshot pattern: open WS,
buffer pushes, fetch REST `/orders` + `/positions`, apply buffer on top.

**P&L stream** (`/stream/pnl`) — subscribe `{"account":…}` (note different field);
multiple accounts per connection. Sends full `init` snapshot, then partial updates
(`aggCalcs` account-level, `position` per-position) on every price tick affecting a
held position — merge, don't replace. eTape computes its own P&L from moomoo ticks;
this stream is a cross-check, not a primary feed.

**WS↔REST field mapping quirks** (Portfolio order objects): `account` vs `accountId`;
`userOrderId` = `"{accountId}:{clientOrderId}"` (split on first `:`); `cancelledQuantity`
(British) vs `canceledQuantity`; `lastQty`/`maxDisplayQty` abbreviations; `status` vs
`orderStatus`. Normalize in the Go adapter behind the broker-agnostic interface.

## Short locates (live only)

Flow: ETB check → `POST /quote` (fresh `quoteReqID`, qty multiple of 100, min 100) →
poll `/locates/history` (~2s cadence) for `locateStatus: 65` (Offered) →
**accept within a strict 30s window** (`POST /accept`) or it expires (67) → shares
appear in `/locates/inventory` (`available`) → short against pool → optionally credit
back unused via `POST /sell`. Status codes: 48 New, 50 Filled, 52 Canceled, 54 Pending,
56 Rejected, 65 Offered, 67 Expired, 81 Quoting. Reg SHO threshold symbols return two
priced offers (PreBorrow + cheaper `.SU` Single Use); accepting one expires the other.
Field-name trap: `/quote` and `/sell` use `account`, `/accept` uses `accountId`.
One in-flight quote per symbol (`14 Locate is already pending`); ≥30s between quotes
for the same symbol. All writes async — the 200 body is only an acknowledgment.

## Rate limits

Token bucket, per API key, **per endpoint** (independent buckets). Exceeded → 429 with
empty body and **no Retry-After / X-RateLimit headers** — back off 1–2s.

| Endpoint | Sustained | Burst |
|---|---|---|
| `POST /order` | 10/s | 10 |
| `DELETE /orders/{id}` | 15/s | 15 |
| `DELETE /orders` (cancel-all) | 3/s | none |
| `GET /orders`, `/order/{id}`, ETB | 2/s | 2 |
| `GET /positions`, `/pnl` | 3/s | 3 |
| `GET /routes`, paginated history | 1/s | 1 |
| `POST /locates/quote` | 1/s (+30s per symbol) | 1 |
| `POST /locates/accept`, `/sell` | 10/s | 10 |

## Options (later phase)

OCC compact symbols (`AAPL260717C00600000`). Single-leg: `securityType:"Option"`.
Spreads: `securityType:"Mleg"`, root `symbol` = underlying, 2–4 legs each with own OCC
symbol/side/ratio/openClose; `SMARTM` route, Day TIF only.

## Design consequences for eTape

1. **Fill events**: Portfolio stream order updates → generic fill events
   (`lastQuantity`/`lastPrice` per execution, `priceAvg` VWAP, `leavesQuantity`
   remaining). Position updates reconcile the position store. WS quirks normalized in
   the TradeZero adapter; engine stays broker-agnostic.
2. **Kill switch** (open question in CLAUDE.md): `DELETE /v1/api/accounts/orders` is
   the primitive — one call cancels everything, optional `?symbol=`. 3/s, no burst;
   confirm via `GET /orders` poll afterward.
3. **Duplicate-order guard**: generate structured `clientOrderId`s (≤36 chars);
   server-side R114 dedup is a second line of defense. IDs are single-use forever —
   the engine's ID generator must never repeat, including across restarts.
4. **Order-state machine**: model the 9 statuses; treat HTTP 200 as "delivered", not
   "accepted"; surface `text` R-codes in the UI.
5. **No modify**: chart-drag order amendment = cancel → poll → replace with new ID;
   UI must show the in-between state.
6. **Reconnect logic owns correctness**: no server heartbeat on WS; staleness
   detection + backoff + REST re-snapshot on reconnect is mandatory.
7. **Dev on paper first**, but paper lacks: order history, IOC/FOK/OPG, real locates,
   explicit routes. Those paths need live-account verification (small size).
8. **Rate-limit budget**: order status polling at 2/s max — prefer the Portfolio
   stream and use REST polls only as fallback/reconcile.

## Credentials (verified 2026-07-03)

Keys in `~/.eJournal/credentials.json` under `tradeZero` (`keyId`/`secretKey`; an
`alpaca` pair sits alongside, not eTape's concern). `GET /v1/api/accounts` returns
HTTP 200: **one LIVE account** (`accountType: "Live"`, Active, 2×
leverage, real funds). There is no paper key pair yet — consider generating one via
the paper portal before any order-flow development.
**Never place/modify/cancel real orders with these keys unless Earl explicitly says
so in the conversation** (also recorded in CLAUDE.md). Read-only endpoints are fine.

**Overnight platform downtime (measured 2026-07-03 ~00:30 ET):** with the same valid
keys, `GET /v1/api/accounts` returns 200 with an **empty accounts array**, every
account-scoped endpoint returns 404, and the Portfolio WS accepts auth (`CONNECTED`)
but rejects the account subscription with `TERMINATED: "invalid account"`. During US
post-market hours the identical calls returned the account normally. Design
consequence: the engine must treat empty-accounts/404/invalid-account as a
**retryable "platform asleep" state, never a fatal auth failure** — only
`FAILED_AUTH` means bad keys. Also measured: the first `PENDING_AUTH` frame can take
~5 s to arrive after WS connect (docs imply immediate); the handshake needs a
generous timeout.

Note: the real `/accounts` payload is a **superset of the documented schema** — extra
fields observed: `accountType`, `availableCashEMS`, `isFutureAccount`,
`maintenanceDeficit`, `marginDeficit`, `marginRatio`, `marginRequirement`,
`unrealized`. Expect the same elsewhere; capture real payloads before freezing Go
structs, and keep decoders tolerant of unknown fields.

## To verify empirically (keys now available — read-only checks OK)

- WS payload shapes vs docs (beta — capture real Portfolio/P&L frames, then freeze Go structs)
- Whether Portfolio stream emits one update per partial fill (fill-event granularity)
- Actual routes available on Earl's account tier (`GET /routes`)
- Latency: order POST → Portfolio-stream ack; and WS staleness detection threshold
- Paper vs live behavior drift beyond documented differences
