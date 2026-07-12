# eTape — moomoo Trading API Research

**Date:** 2026-07-04
**Status:** Research complete. moomoo approved as third execution venue per
`docs/superpowers/specs/2026-07-04-multi-broker-execution-design.md`. **Update
2026-07-11: the `broker/moomoo` adapter is now implemented and wired in**
(opend trade-push routing, wire↔domain mapping, `trdClient`, push decoding,
the `exec.Broker` adapter, and boot/config/`venueprobe` wiring — moomoo-broker-exec
plan Tasks 1–6). A real paper (SIMULATE) `Trd_UpdateOrder` (2208) golden-frame
corpus has since been captured from a live OpenD paper order and superseded
the hand-crafted Task 4 fixture (Task 7) — see
`engine/scripts/capture_golden_frames.py`'s `capture_trd_paper`/`--trd-paper`
and `engine/internal/broker/moomoo/testdata/golden/`. Authorized live
validation (real fills, since paper still can't produce them) is Task 8,
gated on Earl's go-ahead and not yet run. **Open gap, flagged in final review
(2026-07-11), unresolved:** `trdClient.snapshot` (`engine/internal/broker/moomoo/trd.go`)
still leaves `AccountSnapshot.DayPnL` at 0 — see the "Day-P&L in `Trd_GetFunds`"
bullet below — so `exec.Core`'s global `MaxDayLoss` circuit breaker never sees
moomoo's contribution to the day's aggregate loss. Do not live-arm moomoo as
your primary or only venue until this is either built or explicitly accepted.
**Sources:** official docs `https://openapi.moomoo.com/moomoo-api-doc/en/trade/…` +
installed Python SDK 10.8.6808 (`moomoo/common/pb/Trd_*.proto`, `moomoo/trade/*.py`,
`moomoo/common/constant.py:72–94`) + project skill docs
(`.claude/skills/moomooapi/docs/`). Cross-checked; discrepancies noted inline.

## Role in eTape

moomoo OpenD already carries eTape's market data; the same gateway exposes a full
trading protocol family (`Trd_*`) over the identical 44-byte TCP framing. Earl's
FUTUSG (moomoo SG, `securityFirm=3`) margin account has US auth + funds, so moomoo
becomes a third execution venue alongside TradeZero and Alpaca. The **feed connection
stays trade-incapable**; order writes live only in a future `broker/moomoo` adapter on
its own OpenD connection.

## Protocol catalogue

| ID | Proto | Function |
|---|---|---|
| 2001 | Trd_GetAccList | account list (accID, trdEnv, markets, securityFirm, simAccType) |
| 2005 | Trd_UnlockTrade | unlock/lock — **eTape never implements this** |
| 2008 | Trd_SubAccPush | subscribe order/fill pushes (full accID list, not incremental) |
| 2101 | Trd_GetFunds | funds; universal accounts take a `currency` param (USD=2) |
| 2102 | Trd_GetPositionList | positions (`positionSide` LONG=0/SHORT=1) |
| 2111 | Trd_GetMaxTrdQtys | max tradable / `maxSellShort` / `maxBuyBack` |
| 2201 | Trd_GetOrderList | today's orders (OpenD-cached; `refreshCache=true` forces upstream) |
| 2202 | Trd_PlaceOrder | place order |
| 2205 | Trd_ModifyOrder | modify **and** cancel **and** cancel-all |
| 2208 | Trd_UpdateOrder | PUSH: full Order struct on any of 8 field changes |
| 2211 | Trd_GetOrderFillList | today's fills (live only) |
| 2218 | Trd_UpdateOrderFill | PUSH: per-execution fill (live only) |
| 2221/2222 | Trd_GetHistoryOrder/FillList | history queries |
| 2223 | Trd_GetMarginRatio | per-symbol `isShortPermit`, `shortPoolRemain`, `shortFeeRate`, im/mm ratios |
| 2225 | Trd_GetOrderFee | fees by orderIDEx (OpenD ≥8.2.4218) |
| 2226 | Trd_FlowSummary | account cash flow |
| 2227 | Trd_PlaceComboOrder | option combos (out of eTape scope) |

`Trd_ReconfirmOrder` / `Trd_Notify` ship as protos but are unwired in the SDK (no
protocol ID registered). No dedicated global-cancel protocol — cancel-all rides 2205.

## Place order (2202)

- **Anti-replay:** trade writes (2202/2205/2227) carry `Common.PacketID{connID (from
  InitConnect), serialNo (self-incrementing)}` in the C2S body, in addition to the
  frame-header serial.
- **Header:** every trade request carries `TrdHeader{trdEnv, accID, trdMarket}`;
  env/market must match the account's permissions or the call errors. `TrdEnv`:
  Simulate=0, Real=1. `TrdMarket_US=2`; PlaceOrder additionally takes
  `secMarket=TrdSecMarket_US(2)` with a bare ticker in `code` (SDK strips `US.`).
- **Key fields:** `trdSide` (send Buy=1/Sell=2 only — SellShort=3/BuyBack=4 are
  server-reported display values on US orders); `orderType`; `qty` (truncated to
  integer for stocks); `price` (3 dp truncation; US stocks <$1 allow 4 dp; still
  required-but-ignored for market orders); `adjustPrice` unneeded for US;
  **`remark` ≤64 bytes, echoed back in order pushes** → carries eTape's
  `"ET"+ULID` client order ID (28 chars, fits); `auxPrice` = trigger for
  stop/MIT/LIT; `trailType` (Ratio=1/Amount=2) + `trailValue` + `trailSpread`;
  `timeInForce`; `session`; `expireTime` (GTD only).
- **US order types:** Normal(limit)=1, Market=2, Stop=10, StopLimit=11,
  MarketIfTouched=12, LimitIfTouched=13, TrailingStop=14, TrailingStopLimit=15.
  Types 5–9 are HK-only. TWAP/VWAP (16–19) are **query-display only** — cannot be
  placed via API.
- **TIF:** DAY=0, GTC=1 (max 90 calendar days), IOC=2 (**crypto market orders
  only**), GTD=3. US market orders may be DAY or GTC (the DAY-only restriction is
  HK/A-share/futures).
- **Sessions:** `fillOutsideRTH` is **deprecated** → use `session`
  (`Common.Session`): NONE=0, RTH=1, ETH=2, ALL=3 (24-hour; OpenD ≥9.4.5408),
  OVERNIGHT=4. Place order supports RTH/ETH/OVERNIGHT/ALL. **Overnight/24×5 US
  trading is supported: limit orders only, Sun 20:00 → Fri 20:00 ET**; no short
  selling overnight. Note asymmetry: *quote* subscriptions support only RTH/ETH/ALL.
- **Response:** `orderID` (uint64) + `orderIDEx` (string, interchangeable).
- **Pacing:** min 20 ms between consecutive orders.
- **Per-order caps (moomoo SG, US stocks):** 500,000 shares and US$5,000,000 —
  far above eTape's gate caps.
- **Off-market queueing:** on FUTUSG live, US-stock orders placed outside
  trading+extended hours are rejected, not queued (paper queues them). Session.ALL
  mostly moots this except weekends/holidays.

## Modify / cancel / cancel-all (2205)

- `ModifyOrderOp`: Normal=1, Cancel=2, Disable=3, Enable=4, Delete=5.
  **US supports only Normal and Cancel.**
- **Native amend:** op Normal changes price/qty (and aux/trailing params) on a
  working order — no cancel+re-place. `qty` = new **total** desired quantity.
- **Cancel-all:** `forAll=true` + `orderID=0` (+ optional `trdMarket` scope; default
  all markets of the account). **Live only — paper does not support cancel-all.**
  No symbol-scoped cancel-all (adapter iterates).

## Pushes & order lifecycle

- **Subscribe:** `Trd_SubAccPush` (2008) per connection with the **full accID list
  each time** (replaces, doesn't add).
- **Order push (2208):** full `Order` struct whenever any of 8 fields changes
  (status, price, qty, fill qty, aux, trail×3). Struct includes `orderID`,
  `orderIDEx`, `remark` (client ID echo), `orderStatus`, `fillQty`, `fillAvgPrice`,
  `lastErrMsg`, `session`, create/update timestamps (string + epoch double).
- **Fill push (2218):** one per execution with **unique `fillID`/`fillIDEx`** +
  `orderID`, qty, price, timestamps, `OrderFillStatus` (OK=0, Cancelled=1,
  Changed=2 — fills can be busted/amended after the fact). **Live accounts only.**
- **`OrderStatus` enum:** Unknown=-1, Unsubmitted=0, WaitingSubmit=1, Submitting=2,
  SubmitFailed=3, **TimeOut=4 (result unknown — must reconcile, never assume
  terminal)**, Submitted=5, Filled_Part=10, Filled_All=11, Cancelling_Part=12,
  Cancelling_All=13, Cancelled_Part=14, Cancelled_All=15, Failed=21 (broker
  rejected), Disabled=22, Deleted=23, FillCancelled=24. Several are marked
  deprecated in proto comments but Python names remain active — treat all as
  receivable.
- **No push replay after reconnect** (nothing documented; assume none). Recovery =
  re-`InitConnect` → re-`SubAccPush` → re-poll order/fill/position/funds with
  `refreshCache=true` (the documented mechanism for "packet loss / data not
  latest"). Cached reads are unthrottled; `refreshCache=true` reads count against
  query limits.
- **Success judgment:** by `retType` (Succeed=0) + `retMsg`; `errCode` is for
  logging only. Pushes always carry retType=Succeed.

## Unlock semantics (the safety-rule question)

- `Trd_UnlockTrade` (2005): `unlock` bool + `pwdMD5` = lowercase-hex MD5 of the
  **trade password** (+ optional `securityFirm`). Limit 10/30 s per user.
- **Unlock state is owned by the OpenD process, not the connection**: "As long as
  one connection is unlocked, all other connections can call the transaction
  interface" (official unlock page). Survives until OpenD restarts; OpenD pushes
  "needs re-unlock" notifications to connections with `recvNotify=true`.
- **Paper accounts never need unlock.**
- ~~⚠️ Discrepancy~~ **Resolved 2026-07-06: the OpenD GUI DOES expose an
  unlock-trade control** (the skill's TROUBLESHOOTING.md was right; the official
  ops/CLI docs simply omit it). Runbook: unlock in the GUI once per OpenD
  restart. **The trade password never touches eTape** — the Go engine never
  implements 2005.
- Gotcha: Futu-token 2FA breaks API unlock (turn it off first). One-time API
  questionnaire/disclaimer per firm required before trading APIs work (QA Q11).

## Rate limits (per accID unless noted)

| Call | Limit |
|---|---|
| PlaceOrder (+combo, shared) | 15 / 30 s, min 20 ms gap |
| ModifyOrder (incl. cancel + cancel-all) | 20 / 30 s, min 40 ms gap |
| UnlockTrade | 10 / 30 s per user |
| Order/fill/position/funds/max-qty/history/fee queries | 10 / 30 s each — counted **only when `refreshCache=true`**; cached reads free |

No documented max-open-orders or daily cap.

## Paper trading (SIMULATE) — weak, drives the v1.x decision

> **Decided (moomoo-live-only-venue task): moomoo is no longer an eTape venue
> option in paper env.** Config validation and boot both reject `broker:
> "moomoo", env: "paper"` — the built-in sim broker covers the practice/
> simulate need instead. moomoo venues in eTape are live-only now; the
> weaknesses below remain accurate background on *why* (they're the reason
> paper was dropped rather than kept as an option), not a description of a
> live eTape feature.

- Order types: **limit + market only**; TIF **DAY only**; ops modify+cancel only
  (no cancel-all).
- **No fill data at all**: fill push (2218) and fill lists are live-only — track
  fills by diffing `fillQty` on order pushes (2208) + order-list polls.
- Earl's US margin paper account (`simAccType=STOCK_AND_OPTION=4`) additionally
  "may not receive push data temporarily" (skill docs) and needs
  `refresh_cache=True` on queries → plan poll-based fallback.
- Extended hours: contradictory — FAQ says ETH works only on the US margin paper
  account; the place-order page says paper US supports no irregular hours at all.
  OVERNIGHT unsupported on paper. Verify empirically.
- Shorting US stocks **is** supported on paper. Avg-cost/PL fields invalid for
  securities paper accounts. Fill model undocumented.

## Shorting (live, moomoo SG margin)

- Mechanics: send plain **SELL beyond position** — server makes it a short and
  reports `SellShort`; covering is BUY (reported `BuyBack`). **Two-step reversal**
  (close long first, then short; and vice versa); no simultaneous long+short
  ("locking position" unsupported). No overnight-session shorting.
- No locate workflow / shortable-list API. Per-symbol availability =
  `GetMarginRatio` (`isShortPermit`, `shortPoolRemain`, `shortFeeRate`) +
  `GetMaxTrdQtys.maxSellShort`. Funds exposes `maxPowerShort`.
- **PDT: SG/HK accounts are exempt** from FINRA PDT (QA Q12). `Funds` carries
  isPdt/pdtSeq/DTBP fields for US-broker accounts only.

## Fees (moomoo SG, US stocks)

US$0 commission + **US$0.99 platform fee per order** (+9% GST) + pass-throughs
(settlement $0.003/sh capped 1%/order; SEC + TAF on sells). API orders charged same
as app. The only per-order fee among eTape's three venues — a routing consideration.

## Design consequences for eTape

1. **Native replace + native cancel-all + unique `fillID`s** — moomoo sides with
   Alpaca on all three; TradeZero is the odd one out (settles `ReplaceOrder` moving
   behind the `Broker` interface).
2. **`remark` as client-order-ID** — `"ET"+ULID` correlation works end-to-end.
3. **Reconnect = reconcile-by-poll** with `refreshCache=true` (no replay) — same
   pattern as the TZ adapter.
4. **Unlock stays outside eTape** — process-level unlock means the engine never
   holds the trade password; mechanism (GUI vs one SDK call) TBD Monday.
5. **Paper can't validate fills** → moomoo adapter is v1.x, validated with tiny
   authorized live orders after the multi-venue chassis is proven on TZ+Alpaca paper.
   (Since decided: this weakness is also why moomoo paper was removed as an eTape
   venue option entirely — sim covers practice, moomoo venues are live-only.)
6. **Rate budget** 15 places + 20 modifies / 30 s: per-venue token buckets, ample
   for discretionary flow.
7. Trading rides a **separate OpenD connection** from the feed; framing/protobuf
   code is shared, `Trd_*` decoding lives only in the broker adapter.

## To verify empirically

Verified 2026-07-06 pre-market (`prototypes/moomoo_paper_side_checks.py`,
raw: `prototypes/captures/moomoo_paper_side_checks_*.json`):

- ✅ **Day-P&L in `Trd_GetFunds`: there is none.** On the universal account
  (REAL FUTUSG margin, queried read-only) `unrealized_pl`/`realized_pl` are N/A
  and no `today_pl`-like column exists among all 68 SDK columns. Per-currency
  blocks are present (`us_cash`, `usd_assets`, `usd_net_cash_power`, …) and
  `currency=USD` converts the whole view. **Gate rule 5 (global day-loss) must be
  computed from eTape's own fill/position ledger** (or position-level P&L), not
  from the funds call. **STILL UNBUILT as of 2026-07-11** (moomoo-broker-exec
  final review): `trdClient.snapshot` ships with `DayPnL` hardcoded to 0 (see
  the comment on `snapshot` in `engine/internal/broker/moomoo/trd.go`), which
  means `gate.go`'s `BreachedDayLoss`/`MaxDayLoss` is silently blind to
  moomoo-originated losses today. This is a real gap, deliberately deferred as
  its own future feature (ledger-derived day-loss for moomoo) rather than
  patched in as part of the initial adapter — **do not live-arm moomoo as the
  primary or only venue** until it's built or the gap is explicitly accepted.
- ✅ **US paper margin account DOES deliver order pushes**: SUBMITTING + SUBMITTED
  arrived 0.3–0.9 s after `place_order` returned (2/2 orders). Polling stays as
  fallback only. `place_order` RTT on paper: 118–133 ms.
- ✅ **Paper ETH**: `fill_outside_rth=True` is *accepted* on the US margin paper
  account (order reaches SUBMITTED). Whether ETH fills actually happen on paper
  remains unvalidated (paper has no fill data anyway).
- ✅ **`remark` echo confirmed** on order pushes and `order_list_query`
  (`ET-CHECK-…` round-tripped). Fill-side correlation (join `orderID` on the fill
  push) validated during the RTH live benchmark.
- ✅ **cancel-all on paper: unsupported** — `cancel_all_order` fails synchronously
  (ret=-1, "operation Cancel is not supported", 5 ms) with no pushes; per-order
  cancels work fine. Live cancel-all untested (no reason to send one).

- ✅ **OpenD GUI unlock control: EXISTS** (resolved 2026-07-06 — Earl unlocked
  via the GUI before the Monday session; skill docs were right, official docs
  just omit it). **Unlock runbook: GUI unlock once per OpenD restart**; no SDK
  one-liner needed; the trade password never touches eTape.
- Place→push ack latency: **in scope for Monday's benchmark** (amended
  2026-07-04) — run the moomoo **live** account alongside TZ paper + Alpaca paper
  in the same session, same measurements (place → order-push ack → fill push).
  Live because paper can't validate fills. Earl authorized live benchmark orders
  2026-07-04: 1-share marketable limits on a cheap liquid symbol, flatten
  immediately, RTH only. Requires trade unlock first (GUI or SDK one-liner —
  the unlock verification above is a prerequisite of the same session).
- `Order.remark` echo on **fill** correlation path (fill push has no remark — join
  via `orderID`).
- Whether cancel-all (`forAll`) acks synchronously or only via per-order pushes.
