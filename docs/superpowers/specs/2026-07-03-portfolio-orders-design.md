# eTape — Execution & Portfolio Management Design

**Date:** 2026-07-03
**Status:** Approved (design); implementation plan not yet written.
**Revised 2026-07-04** by `2026-07-04-multi-broker-execution-design.md` (multi-venue:
TZ + Alpaca + moomoo; venue-keyed fold, two-layer gate, `ReplaceOrder` in the
`Broker` interface). Unrevised sections stand.
**Depends on:** `docs/2026-07-03-stack-decision.md`, `docs/2026-07-03-tradezero-api.md`

## Purpose

Manage account/portfolio state, orders, and fill history in the eTape Go engine on
top of TradeZero (execution) and moomoo OpenD (market data / price marks). Covers
tracking **and** order actions (place, cancel, replace, kill switch) behind a safety
gate. Excludes trading UX (ladder click-trading, hotkeys, order tickets) — later
design. Development runs against a TradeZero **paper** account (keys to be
generated); the live account is read-only until Earl explicitly says
otherwise.

## Decisions made during brainstorming

| Question | Decision |
|---|---|
| Scope | Tracking + order actions; no trading UX |
| Fill history ownership | eTape owns its own SQLite store; eJournal untouched (its unique `externalId` enables a trivial future export) |
| Safety | Full envelope + armed/disarmed switch, built from day one |
| v1 UI surfaces | Account bar, positions panel, open orders panel, fills-on-chart |
| Architecture | Hybrid event log: order lifecycle + fills as append-only events; account + positions as broker-reconciled snapshots |
| P&L | Unrealized P&L marked locally from moomoo ticks; TZ P&L stream unused in v1 |

Rejected alternatives: mutable stores + reconciler (races become implicit, audit
manual); full event sourcing (re-derives numbers TZ owns authoritatively).

## Architecture

```
  TradeZero REST ──┐                ┌─────────────────────────────┐
  TradeZero WS  ──┤ broker/        │           engine            │
                   │ tradezero   ──▶│  exec/      domain + iface  │
                   │ (adapter)      │  exec/state event fold      │
                   └────────────────│  exec/gate  safety envelope │
  moomoo ticks ────▶ (P&L marks)    │  store/     SQLite          │
                                    └──────┬──────────────────────┘
                                           │ uihub: WS topics (JSON, tygo types)
                                           ▼
                        account bar · positions · open orders · chart fills
```

Dependencies are one-way: `exec` (domain) knows nothing about TradeZero; the adapter
imports the domain, never the reverse. A future broker = a new adapter only.

**Startup / reconnect sequence** (TZ-documented pattern): open Portfolio WS → buffer
pushes → REST snapshot (account, positions, open orders) → seed state → replay
buffered pushes → live. Re-runs in full on every WS reconnect.

## Components

### `exec` — broker-agnostic domain

Types: `Order` (ID, symbol, side, qty, orderType, TIF, limitPrice, stopPrice, status,
executedQty, leavesQty, avgFillPrice, rejectReason, timestamps), `Fill` (orderID,
symbol, side, qty, price, ts), `Position` (symbol, long/short, qty, avgPrice,
dayOvernight), `AccountSnapshot` (equity, buyingPower, availableCash, sodEquity,
realized, leverage).

`Side ∈ {Buy, Sell, Short, Cover}` — trader actions, not wire format. The adapter
maps to TZ `side`+`openClose` outbound and un-enriches `SellShort` inbound.

**Order IDs:** `clientOrderId` = `"ET"` + ULID (28 chars, under TZ's 36-char limit).
Time-ordered, collision-free across restarts with no coordination — satisfies TZ's
IDs-consumed-forever rule by construction.

**Broker interface:**

```go
type Broker interface {
    SubmitOrder(ctx context.Context, req OrderRequest) (OrderAck, error)
    CancelOrder(ctx context.Context, orderID string) error
    CancelAll(ctx context.Context, symbol string) error // symbol "" = everything
    Snapshot(ctx context.Context) (AccountSnapshot, []Position, []Order, error)
    Events() <-chan BrokerEvent // domain events + ConnUp/ConnDown
}
```

### Events (append-only log; source of truth for what eTape owns)

`OrderSubmitted`, `OrderAccepted`, `OrderRejected` (broker refused), `OrderBlocked`
(gate refused; never left the machine), `OrderFilled` (one per execution, partial or
full), `OrderCanceled`, `OrderExpired`, `StreamGap` (reconnect occurred; reconcile
synthesized transitions).

Envelope: monotonic `seq`, timestamp, `source ∈ {local, ws, rest, reconcile}`, JSON
payload.

### `exec/state` — pure fold

`apply(state, event) → state`. Holds open orders, today's fills, positions, account.
No I/O; single writer goroutine; invariant `replay(log) == state`.

Account and positions are **snapshot-reconciled** (not event-sourced): REST snapshot
seeds them, WS position updates and account refreshes overwrite them. TZ is the
authority; eTape mirrors.

### `exec/gate` — safety envelope

Every `SubmitOrder` passes, in order (first failure → `OrderBlocked` with reason):

1. **Armed** — engine boots disarmed, always; arming is an explicit UI command;
   state shown in account bar
2. **Duplicate ID** — order ID absent from `exec_events`
3. **Max order value** — qty × limitPrice (market orders: qty × last moomoo trade)
4. **Max resulting position** — |position + working same-direction orders + this
   order| ≤ configured $ and share caps
5. **Max open orders** — working-order count cap
6. **Max day loss** — uses TZ-reported `dayPnl` (from the account refresh poll,
   authoritative), not local marks; breach auto-disarms; re-arming requires
   explicit human action

`KillSwitch` = TZ cancel-all + immediate disarm + confirmation poll of `GET /orders`.
All limits in a `[gate]` config section — never code.

### `broker/tradezero` — adapter

- **Normalization** (quirks per `docs/2026-07-03-tradezero-api.md`): split
  `userOrderId` (`accountId:clientOrderId`) on first `:`; `cancelledQuantity` →
  `canceledQuantity`; `lastQty`/`maxDisplayQty` → full names; `status` →
  `orderStatus`; enriched `SellShort` → domain `Short`/`Cover` via `openClose`.
- **Fill derivation:** Portfolio order update with `lastQuantity > 0` and increased
  cumulative `executed` → one `OrderFilled`; dedup key `(orderID, executed)`.
- **Reconnect:** jittered exponential backoff (1 s → 30 s cap) → re-handshake →
  re-subscribe → REST re-snapshot → reconcile (diff order states, synthesize missed
  transitions with `source=reconcile`). Never reconnect after `FAILED_AUTH`.
- **Staleness:** TZ sends no app-level heartbeat. WS protocol-level ping every 30 s
  (pong timeout → reconnect) + slow REST reconcile poll of `GET /orders` **only
  while working orders exist** (well under the 2/s limit).
- **Client-side rate limiting:** token buckets mirroring TZ's documented
  per-endpoint limits; eTape throttles itself rather than discovering 429s.
- **Routes:** fetched from `GET /routes` at startup, validated against config
  defaults per securityType — handles paper (auto-assign) vs live (explicit
  required) without code branches.
- **Account refresh (steady state):** the Portfolio WS carries orders/positions
  only, and v1 skips the TZ P&L stream — so account values come from slow REST
  polling: `GET /account/{id}` (buying power, equity) and `GET /pnl` (day P&L,
  cash) every ~5 s during the session, plus an immediate refresh after any fill or
  cancel event. Both endpoints allow 3/s; this uses a fraction of that.

### `store` — persistence (SQLite, WAL mode; same DB planned for ticks/OHLCV)

```sql
exec_events(seq INTEGER PRIMARY KEY AUTOINCREMENT,
            ts TEXT NOT NULL, source TEXT NOT NULL, type TEXT NOT NULL,
            order_id TEXT, payload TEXT NOT NULL)          -- JSON
fills(fill_id INTEGER PRIMARY KEY AUTOINCREMENT,
      order_id TEXT NOT NULL, symbol TEXT NOT NULL, side TEXT NOT NULL,
      qty REAL NOT NULL, price REAL NOT NULL, ts TEXT NOT NULL,
      seq INTEGER NOT NULL REFERENCES exec_events(seq))
-- index: fills(symbol, ts)  → chart-annotation range queries
```

`exec_events` is the source of truth; `fills` is a projection written in the same
transaction. No orders projection table: boot replays today's events, then
REST-reconciles (broker is the authority on open orders). Account/position history
is not persisted in v1; an equity-curve table is a cheap later addition.

## Commands (UI → engine)

`SubmitOrder`, `CancelOrder`, `ReplaceOrder`, `KillSwitch`, `Arm`, `Disarm`.

`ReplaceOrder` (TZ has no modify): cancel → await `OrderCanceled` (timeout ~3 s →
abort replace, report) → submit with fresh ID. Order shows `Replacing` state
in between.

Commands carry correlation IDs; the synchronous ack is `accepted | blocked(reason)`;
outcomes arrive asynchronously as events on `exec.orders`.

## Engine → UI contracts (WS + JSON; TS types via tygo)

Each topic: full snapshot on subscribe, then deltas.

| Topic | Content | Rate |
|---|---|---|
| `exec.account` | account bar fields + armed state | throttled ~4 Hz |
| `exec.positions` | position rows, unrealized P&L marked from moomoo last trade | coalesced ~100 ms batches |
| `exec.orders` | order upserts | event-driven |
| `exec.fills` | live fill events; plus request/response query `(symbol, range)` against `fills` for chart-open backfill | event-driven |
| `exec.status` | broker conn state, last reconcile, gate config in effect | on change |

Per stack rule, tick-rate data never reaches React state; P&L marking and coalescing
happen engine-side.

## Error handling

| Edge | Behavior |
|---|---|
| HTTP 200 with `orderStatus: Rejected` | `OrderRejected` event carrying R-code text |
| HTTP 400 (schema, plain-text body) | `OrderRejected` with body text; logged loudly — indicates an adapter bug |
| Transport failure on submit (no HTTP response) | Retry **once with the same ID**; R114 response = original landed (probe), clean acceptance = it didn't. No double-order ambiguity |
| Crash mid-submit | `OrderSubmitted` appended **before** POST; boot recovery resolves dangling `Submitted` events against REST |
| Cancel returns 404 (too fresh / already terminal) | Resolve truth via `GET /orders` poll; never assume |
| WS gap | Reconcile synthesizes transitions (`source=reconcile`); fill dedup key prevents double-count; `StreamGap` logged + surfaced on `exec.status` |
| 429 (should not occur given client buckets) | Backoff + retry idempotent GETs only; order POSTs never blind-retried |

## Testing

- **Fold + gate:** table-driven pure unit tests; property `replay(log) == state`;
  one test per gate rule.
- **Adapter:** golden-file tests from captured paper-account payloads (REST JSON +
  WS frames); corpus grows with every newly observed quirk. Mock WS server exercises
  handshake, reconnect, staleness, `TERMINATED`/`INVALID_DATA` recovery.
- **Integration (paper keys):** scripted far-from-market limit order → cancel →
  replace → kill switch; frames captured into the golden corpus.
- **Live account:** read-only verification only (per CLAUDE.md safety rule).

## Out of scope (v1)

Trading UX (ladder/hotkeys/tickets), options and multi-leg orders, short locates
workflow, TZ P&L stream cross-check, equity-curve persistence, eJournal export,
multi-account support (single active account assumed; config holds credentials path
and account ID).

## Open items

- Generate paper API keys (Earl, via TZ paper portal) — blocks integration tests
- Verify on paper: WS frame shapes vs docs; whether Portfolio stream emits one
  update per partial fill (fill dedup key protects either way); actual routes;
  POST→WS-ack latency
- Confirm real payload supersets before freezing Go structs (decoders stay tolerant
  of unknown fields regardless)
