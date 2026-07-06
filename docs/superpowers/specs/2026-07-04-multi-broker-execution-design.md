# eTape — Multi-Broker Execution Design

**Date:** 2026-07-04
**Status:** Approved (design); implementation plan not yet written
**Revises:** `docs/superpowers/specs/2026-07-03-portfolio-orders-design.md` (its
single-broker assumptions); amends two lines of
`docs/superpowers/specs/2026-07-03-go-engine-design.md` (§ Ripple effects). Everything
in those specs not touched here stands as approved.
**Depends on:** `docs/2026-07-03-tradezero-api.md`, `docs/2026-07-03-alpaca-api.md`,
`docs/2026-07-04-moomoo-trading-api.md`

## Purpose

Generalize the execution subsystem from one broker to **concurrent multi-venue**
execution across TradeZero, Alpaca, and moomoo — Earl holds funded accounts at all
three. Covers the venue model, domain/interface changes, the two-layer safety gate,
per-broker adapter designs, persistence, and UI contracts. Excludes trading UX and
the broker-*selection* UX (deferred to a later UI design revision — the engine
contract makes selection a pure presentation concern).

## Decisions made during brainstorming

| Question | Decision |
|---|---|
| Multi-broker? | Yes — all three funded brokers as concurrently configured venues |
| Venue unit | `(broker, account, environment)` tuple from config; the unit of adapter instance, gate scope, arming, and event tagging |
| Core architecture | One exec fold, venue-keyed state + fold-derived cross-venue aggregates; one event log, one writer (approach A) |
| Gate | Two layers: per-venue caps + global aggregates; global day-loss breach = master disarm |
| v1 adapters | TradeZero + Alpaca; moomoo designed now, built v1.x (its paper env can't validate fills) |
| Routing | None engine-side; every `OrderRequest` names its venue explicitly |
| Kill switch | Cancel-all on **all** venues + master disarm; venue-scoped kill secondary; flatten never in the kill path |
| Replace | `ReplaceOrder` joins the `Broker` interface + `Capabilities`; native on Alpaca/moomoo, TZ emulates internally |

Rejected alternatives: **global-only gate** (one broker's runaway trips everything;
per-venue exposure uncapped) and **per-venue-only gate** (total risk never checked
anywhere); **N exec instances + risk aggregator** (the safety check either reads
cross-goroutine state — racy — or serializes through the aggregator, which is
approach A with extra steps; three logs to order; `replay==state` fragmented);
**one process per broker** (global gate and single UI impossible); **all three
adapters in v1** (an adapter that can only be validated with real money would be
built blind and rot; prove the multi-venue chassis on the two high-fidelity paper
environments first).

## Venue model

```toml
[[venue]]
id = "alpaca-paper"        # slug used everywhere: events, topics, commands, gate config
broker = "alpaca"          # tradezero | alpaca | moomoo
env = "paper"              # paper | live
credentials = "alpaca"     # key into ~/.eJournal/credentials.json
# broker-specific extras: TZ accountId, moomoo accID, …
```

A venue is one configured `(broker, account, environment)` tuple. Paper and live are
distinct venues (they differ in keys, endpoints, and risk posture). Each venue gets
one adapter instance; venue IDs tag every event, topic row, command, and gate
section. v1 ships `broker/tradezero` + `broker/alpaca`; `broker/moomoo` is designed
here and built in v1.x.

Monday's order-latency benchmark is reframed: a **routing input** (which venue to
prefer for what), no longer a broker decision. Scope (amended 2026-07-04): **three
venues in one session** — **TZ live** (paper keygen failed 2026-07-04; Earl
authorized live instead), Alpaca paper, and the **moomoo live account** (its paper
env can't validate fills). Live-order guardrails on both live legs: minimal size,
cheap liquid symbol, long side only, flatten immediately; re-confirm authorization
in the session that runs it.

## Domain changes (`exec`)

- `VenueID` (string). `Order`, `Fill`, `Position`, `AccountSnapshot` gain
  `Venue VenueID`. `OrderRequest` **requires** one — the engine performs zero
  routing; a request without a valid venue is malformed.
- `Side {Buy, Sell, Short, Cover}` unchanged. Adapter mappings: TZ
  `side`+`openClose` (as speced); Alpaca plain buy/sell with position context;
  moomoo sends Buy/Sell, un-enriches inbound `SellShort`/`BuyBack`.
- Order IDs stay `"ET"`+ULID — globally unique across venues and restarts by
  construction; fits TZ (36-char cap), Alpaca (128), moomoo (`remark`, 64 bytes).

```go
type Broker interface {
    Capabilities() Capabilities
    SubmitOrder(ctx context.Context, req OrderRequest) (OrderAck, error)
    ReplaceOrder(ctx context.Context, orderID string, req ReplaceRequest) error
    CancelOrder(ctx context.Context, orderID string) error
    CancelAll(ctx context.Context, symbol string) error // "" = everything on this venue
    Snapshot(ctx context.Context) (AccountSnapshot, []Position, []Order, error)
    Events() <-chan BrokerEvent // domain events + ConnUp/ConnDown
}

type Capabilities struct {
    NativeReplace    bool // Alpaca PATCH, moomoo ModifyOrder-Normal; TZ false
    FlattenAll       bool // Alpaca DELETE /v2/positions only
    OvernightSession bool // Alpaca (Blue Ocean ATS), moomoo (Session OVERNIGHT); TZ false
}
```

**Events:** the envelope gains `venue`. New event `OrderReplaced` (native replaces).
The TZ adapter implements `ReplaceOrder` internally as cancel → await → resubmit and
emits its natural `OrderCanceled` + `OrderSubmitted` chain with a `replacesOrderID`
link — the exec spec's `Replacing` interim state becomes a TZ-adapter artifact,
invisible to the domain.

## Exec core — one fold, venue-keyed (approach A)

State becomes `map[VenueID]VenueState` (open orders, today's fills, positions,
account snapshot, armed flag) plus fold-derived cross-venue aggregates (per-symbol
net position and working same-direction exposure across venues; summed day P&L).
Commands enter the fold's inbox; the gate evaluates in-loop against the one
consistent state — cross-venue races are impossible by construction, the same
argument the engine spec makes for the md core. `OrderSubmitted` is still appended
**before** the adapter POST (crash-recovery rule unchanged). `replay(log) == state`
holds over the single interleaved log. Event volume is human-scale; the shared inbox
is a non-issue.

## Gate — two layers

Check order; first failure → `OrderBlocked(reason)`:

1. **Master armed** — one engine-wide switch; boot: off, always. Master arm and
   disarm are their own explicit commands; trading on a venue requires master AND
   that venue armed
2. **Venue armed** — per-venue explicit UI action; boot: all off
3. **Duplicate ID** — global (one event log)
4. **Per-venue:** max order value → max resulting venue position ($ and shares) →
   max open orders
5. **Global:** max resulting **per-symbol position across all venues** (positions +
   working same-direction orders everywhere + this order) → max **total day loss**
   (venue day P&Ls summed; breach = master disarm; re-arming is explicit human
   action)

Day-P&L source per venue: TZ `GET /pnl` poll (authoritative, as speced); Alpaca
`equity − last_equity` from `GET /account`; moomoo from `Trd_GetFunds` (exact field —
open item, v1.x). Config, never code:

```toml
[gate.global]
max_day_loss = …
max_symbol_position_value = …
max_symbol_position_shares = …

[gate.venue.alpaca-paper]
max_order_value = …
max_position_value = …
max_position_shares = …
max_open_orders = …
```

**KillSwitch** = parallel `CancelAll` on every connected venue + master disarm +
per-venue confirmation polls; a dead venue never blocks killing the others.
Venue-scoped kill (`KillSwitch{venue}`) is a secondary control. **Flatten is not in
the kill path** (kill never places orders): a separate explicit `Flatten{venue}`
command uses the native primitive where `Capabilities.FlattenAll` (Alpaca) and is
rejected as `blocked(unsupported)` elsewhere in v1.

Auto-disarm triggers: global day-loss breach → master disarm; a crash-looping or
`FAILED_AUTH` adapter → that venue disarmed + banner (per engine-spec error policy).

## Adapters

### `broker/tradezero` — deltas only

Per the exec spec, plus: implements `ReplaceOrder` via internal cancel → await
`OrderCanceled` (timeout ~3 s → abort, report) → resubmit with fresh ID;
`Capabilities{NativeReplace: false, FlattenAll: false, OvernightSession: false}`.

### `broker/alpaca` — new, v1

- **Transport:** REST (`paper-api`/`api` per venue env) + `trade_updates` WS
  (`wss://…/stream`). Handles **paper=binary / live=text frames** and
  JSON/MessagePack. Warm keep-alive connection pool mandatory (cold TLS ~430 ms).
- **Rate limiting:** one pooled token bucket, 200 req/min; WS-first state, REST only
  for reconnect re-snapshot — no polling loops.
- **Normalization:** `trade_updates` events map near-1:1 onto domain events; fill
  dedup key = `execution_id`; `position_qty` cross-checks position reconciliation.
  Structured JSON errors (no 200-but-rejected trap); async exchange rejections still
  arrive as `rejected` events.
- **Replace:** `PATCH /v2/orders/{id}` → `OrderReplaced`. Cancel rejected while
  `pending_replace` — surfaced, not retried.
- **CancelAll:** `""` → `DELETE /v2/orders`; symbol-scoped → list + cancel each.
- **Startup/reconnect:** buffer → REST snapshot → replay, same sequence as TZ.
- **Order IDs:** `client_order_id` = `"ET"`+ULID; resolve transport-failure
  ambiguity via `GET /v2/orders:by_client_order_id` (reuse semantics — verify list).

### `broker/moomoo` — designed now, built v1.x

Full protocol detail in `docs/2026-07-04-moomoo-trading-api.md`.

- **Transport:** its **own TCP connection** to OpenD reusing the feed client's
  framing/protobuf code. The feed connection remains structurally trade-incapable —
  `Trd_*` encoding/decoding exists only here. Trade writes carry
  `PacketID{connID, serialNo}` anti-replay.
- **Unlock:** never implements `Trd_UnlockTrade` (2005) — the trade password never
  touches eTape. Unlock is per-OpenD-process and happens outside the engine
  (verified 2026-07-06: the OpenD GUI exposes an unlock control — runbook is GUI
  unlock once per OpenD restart). An
  OpenD "unlock needed" error parks the venue blocked with a banner requiring human
  action; never auto-unlock. Paper needs no unlock.
- **Startup:** `GetAccList` (validate accID/securityFirm) → `SubAccPush` (full accID
  list) → snapshot via `GetOrderList`/`GetPositionList`/`GetFunds` (USD) with
  `refreshCache=true` → live pushes 2208 (order) + 2218 (fill).
- **Reconnect:** no push replay exists → re-`InitConnect` → re-`SubAccPush` →
  re-snapshot with `refreshCache=true`; synthesize missed transitions
  (`source=reconcile`), same pattern as TZ.
- **Replace:** `ModifyOrder` op Normal (price/qty native; qty = new total) →
  `OrderReplaced`. **CancelAll:** `ModifyOrder` `forAll=true` (live; paper iterates).
- **Fills:** dedup key = `fillID`; correlate to orders via `orderID` (fill push has
  no `remark`); `remark` echoes the client order ID on order pushes.
- **Rate limiting:** buckets 15 places/30 s (min 20 ms gap) + 20 modifies+cancels/
  30 s (min 40 ms gap); query bucket only spent when `refreshCache=true`.
- **Paper fallback:** the US paper account may miss pushes and has no fill data —
  adapter supports poll-derived fill synthesis (diff `fillQty` on order polls) for
  SIMULATE venues only.

## Persistence (`store`)

`exec_events` and `fills` each gain `venue TEXT NOT NULL`. The single AUTOINCREMENT
`seq` already gives the interleaved total order the fold replays. Existing indexes
stand — chart annotations query `fills(symbol, ts)` across venues by design. No
other schema changes; no migration needed (pre-implementation).

## Commands and UI contracts (WS + JSON; tygo)

Commands: `SubmitOrder{venue, …}`, `CancelOrder{venue, orderID}`,
`ReplaceOrder{venue, orderID, …}`, `Flatten{venue}`, `KillSwitch{venue?}` (absent =
all venues), `Arm{venue?}` / `Disarm{venue?}` (absent = the master switch).
Correlation IDs and sync `accepted | blocked(reason)` ack unchanged.

| Topic | Multi-venue content (rates unchanged from exec spec) |
|---|---|
| `exec.account` | one row per venue (snapshot + armed flag) + master-arm state |
| `exec.positions` | per-venue rows **plus engine-computed per-symbol net rows**; presentation deferred to UI design |
| `exec.orders` | order upserts, venue-tagged |
| `exec.fills` | venue-tagged live fills; chart backfill query returns all venues for a symbol |
| `exec.status` | per-venue conn/reconcile state + gate config in effect |

How the UI *selects* a venue (ticket dropdown, per-panel default, hotkey profiles…)
is explicitly deferred; the engine contract above is sufficient for any of them.

## Error handling

The exec spec's TZ table stands. Engine policy (honesty, recover-and-restart, md
never takes exec down) stands. Additions:

| Edge | Behavior |
|---|---|
| One venue's adapter down/crash-looping | Other venues' commands and kill switch unaffected; venue disarmed + banner |
| Alpaca 429 (shouldn't occur given bucket) | Backoff + retry idempotent GETs only; order POSTs never blind-retried |
| Alpaca paper binary frames / msgpack | Decoded transparently; golden corpus covers both encodings |
| moomoo `retType != 0` | Judge by `retType`+`retMsg`; `errCode` logged only |
| moomoo `OrderStatus.TimeOut` (result unknown) | Reconcile via order-list poll before treating as terminal — mirror of TZ transport-failure probe |
| moomoo fill corrections (`OrderFillStatus` Cancelled/Changed, `FillCancelled`) | Rare: log loudly, fix state via reconcile, surface on `sys.events` |
| moomoo "unlock needed" | Venue parked blocked + banner; human unlocks outside eTape; never auto-unlock |
| Cross-venue day-loss breach | Master disarm + banner; re-arm human-only |

## Testing

- **Fold + gate:** table-driven multi-venue scenarios — cross-venue aggregate math,
  master-vs-venue arming, one-venue-down, duplicate IDs across venues;
  `replay(log) == state` unchanged.
- **Adapters:** golden corpora per broker — TZ as speced; Alpaca paper captures in
  **both frame encodings**; moomoo order-push frames captured now via the Python SDK
  on paper (fill-push corpus is live-only → v1.x validation). Mock servers exercise
  handshake/reconnect/staleness per adapter.
- **Integration (v1):** scripted lifecycle (far-from-market limit → replace → cancel
  → kill) on TZ paper + Alpaca paper in one session.
- **moomoo validation (v1.x):** tiny live orders (e.g. 1 share, far-from-market
  limit, then cancel) only in sessions Earl explicitly authorizes, per the standing
  safety rule.
- **E2E:** two SimBroker venues under replay exercise the aggregate gate, master/
  venue arming, and venue-tagged UI flows with no real broker. SimBroker implements
  `Capabilities` like any adapter.

## Ripple effects on approved specs

- **Engine spec:** `internal/broker/` gains `alpaca/` (v1) and `moomoo/` (v1.x)
  siblings of `tradezero/`. The line "the client is structurally incapable of
  trading" is amended to the sharper true claim: *the feed connection implements no
  `Trd_*` protocols; order writes live only in `broker/moomoo`, which never
  implements unlock — the trade password never touches eTape.* Its out-of-scope
  line "TZ-vs-Alpaca … either lands behind the Broker interface" becomes "all three
  land behind it, concurrently."
- **Exec spec:** venue model, gate layering, `Broker` interface, and schema columns
  per this document; its TZ adapter internals, event-log semantics, fold rules, and
  error table otherwise stand.

## Out of scope (v1)

Trading UX and broker-selection UX (later UI design revision); engine-side routing,
auto-failover, or smart order splitting across venues; `broker/moomoo`
implementation (v1.x — designed above); flatten on non-Alpaca venues; options and
multi-leg; TZ locates workflow; per-venue day-loss sub-limits (global only in v1);
eJournal export (unchanged).

## Open items

Most closed in the 2026-07-06 Monday live session — details in
`docs/2026-07-06-monday-live-session-checklist.md` and the per-broker research docs:

- ✅ OpenD GUI unlock control **exists** → unlock runbook = GUI once per OpenD
  restart (2026-07-06)
- ✅ Three-venue benchmark **run 2026-07-06 pre-market** (Earl amended RTH-only
  in-session; live legs re-confirmed before ordering). Results:
  `docs/2026-07-06-venue-latency-benchmark.md`. Headline: ack — Alpaca ~210 ms
  < moomoo ~285 ms ≲ TZ ~345 ms (OpenD hop adds ~nothing: the ack push lands
  before the RPC returns); real fills — TZ 0.34–0.43 s vs moomoo 0.87–1.04 s;
  fill prices identical at this scale; moomoo's US$0.99/order platform fee is
  the standing non-latency routing consideration. Optional RTH re-run pending.
- ✅ moomoo `Trd_GetFunds` has **no day-P&L field** on the universal account →
  gate rule 5 computes day-loss from eTape's own fill/position ledger
  (2026-07-06; per-currency cash blocks exist, `currency=USD` converts)
- ✅ moomoo US paper account **does deliver order pushes** (0.3–0.9 s) → polling
  is fallback, not primary; `fill_outside_rth=true` accepted on paper; paper
  cancel-all unsupported (per-order cancels only) (2026-07-06)
- ✅ Alpaca: `client_order_id` permanently consumed after terminal states (same
  as TZ R114 — always mint fresh ids); paper stream = plain JSON in binary
  frames; FOK/OPG/CLS accepted on standard account, IOC time-gated not
  entitlement-gated (2026-07-06)
- TZ paper keys (carried) — keygen failed 2026-07-04; still blocks v1
  integration/E2E tests (the Monday benchmark runs on TZ live instead)
