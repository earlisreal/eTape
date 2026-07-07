# Venue order-latency benchmark — 2026-07-06 pre-market

The Monday checklist §3 benchmark, run **pre-market** (~04:42–04:47 ET; Earl
amended the RTH-only guardrail for this run in-session). Routing **input**, not a
broker decision.

Method: `prototypes/venue_order_latency_bench.py` — symbol **F**, 1-share
marketable limits (ask+3¢ / bid−3¢), long only, flatten immediately, ETH order
forms (TZ TIF `Day_Plus`, Alpaca `extended_hours:true`, moomoo
`fill_outside_rth:true`), keep-alive REST (the Go adapters will pool connections,
so the bench does too). Cycles: Alpaca paper ×3, TZ live ×2, moomoo live ×2.
All venues verified flat afterwards. Raw timelines + WS/push captures in
`prototypes/captures/venue_latency_20260706_*.json` (+ `tz_bench_ws_*`,
`moomoo_bench_pushes_*`, `alpaca_bench_ws_*` — ⚠️ contain account ids/balances,
sweep before committing).

## Results (ms after order send; warm connections)

| venue | place→API return | place→ack push | place→fill push | fills (buy/sell) |
|---|---|---|---|---|
| **Alpaca paper** | **196–201** | **207–215** | 433–1002 *(simulator)* | 13.41 / 13.38 |
| **TZ live** | 283–327 | 326–409 | **338–426** | 13.4172 / 13.41 |
| **moomoo live** | 268–312 | **266–307** (lands *before* the RPC returns) | 870–1038 | 13.4168 / 13.41 |

## Reads

- **Ack path:** Alpaca ~210 ms < moomoo ~285 ms ≲ TZ ~345 ms. moomoo's order
  push arrives on the OpenD TCP connection a few ms *before* `place_order()`
  itself returns — the OpenD hop does not add meaningful ack latency on top of
  its RPC.
- **Real fill completion (live venues only):** TZ was strikingly fast —
  **0.34–0.43 s** place→fill on every order; moomoo took **0.87–1.04 s**.
  Alpaca's fills are paper-simulated and not comparable; live fill quality
  stays unknown until a live account exists *(measured 2026-07-07 — see the
  Alpaca LIVE addendum: ~0.23 s, fastest of the three)*.
- **Fill prices:** effectively identical across live venues (buys 13.4168 vs
  13.4172, sells both 13.41) — at 1-share scale price improvement is noise.
- **Cold connects:** Alpaca first request ~580–660 ms (TLS; pool mandatory —
  matches the 2026-07-03 measurement); without keep-alive *every* Alpaca POST
  cost ~590 ms in a control run.
- **Fees:** moomoo charged its US$0.99+GST platform fee per order (≈US$4.30
  for the leg) — the only per-order fee among the three; a standing routing
  consideration, not a latency one.
- **Caveats:** pre-market conditions (04:46 ET), one cheap liquid symbol,
  qty 1, 2–3 cycles per venue. An RTH re-run is cheap and optional for
  comparison; expect fills to only get faster.

## Broker-adapter facts captured en route (feed the Go structs)

- **TZ order lifecycle over the Portfolio WS:** `PendingNew → New → Filled`,
  one frame per state; avg fill price in `priceAvg`; per-execution fields
  `lastPrice`/`lastQuantity`/`leavesQuantity` present (⇒ partial fills emit
  per-execution updates; unverified with a true multi-execution order).
  **Position frames** stream too (`priceAvg`/`priceClose`/`shares`) — free
  position reconciliation. ⚠️ `GET /orders` list rows omit fill fields
  (`filledQuantity`/`averagePrice` absent) — fill data comes from the WS (or a
  per-order endpoint), not the list.
- **moomoo fill correlation confirmed live:** deal push (2218) has **no
  remark** — join to the client order via `order_id`; the order push (2208)
  echoes `remark`, `session:"ETH"`, `fill_outside_rth:true`. ETH live fills
  work on the FUTUSG universal account.
- **Alpaca:** ack (`new`) push trails the REST response by ~13 ms; paper stream
  again all JSON-in-binary frames.

## RTH re-run — 2026-07-06 09:34–09:40 ET, symbol NVDA (per Earl: "something more volatile")

Same script, RTH order forms (plain Day, no ETH flags), 1-share marketable
limits on NVDA ~$194.5 (price guard raised via `--price-guard` with explicit
authorization). Two lessons and one venue anomaly:

| venue | API return | ack push | fill push | fills (buy/sell) |
|---|---|---|---|---|
| **TZ live** | 284–308 | 328–363 | **328–439** | 194.48/194.45, 194.30/194.2501 |
| **moomoo live** | 260–282 | 256–278 (still beats the RPC return) | **898–962** | 194.71/194.6735, 194.6119/194.6401 |
| **Alpaca paper** | 198 | 291 | *(no fills — see below)* | — |

- **RTH ≈ pre-market on the wire.** Both live venues' API/ack numbers are
  within noise of the pre-market run; fills likewise (TZ 0.33–0.44 s,
  moomoo 0.90–0.96 s). Venue infrastructure latency is session-independent.
- **Marketable-limit buffer lesson:** a flat 3¢ buffer failed on NVDA (1.5 bps
  — price outran the limit; buys sat unfilled and were cancelled). Fixed to
  `max(3¢, 0.2% of price)`; moomoo then filled 4/4. The eTape order ticket's
  "marketable limit" preset should scale with price, not be a fixed offset.
- **Alpaca paper matching engine stalled during RTH**: buys 39¢ above the ask,
  then a plain **market order**, sat at `new` for >5 s with `clock.is_open`
  true (pre-market fills earlier the same day were instant, 0.4–1.0 s).
  Consequence for Plan 5/6 testing: **do not assume prompt Alpaca paper fills
  in integration tests** — assert on `new` acks, treat fills as eventual.
  Live fill quality remained unmeasurable until a live account existed —
  measured 2026-07-07, see the Alpaca LIVE addendum.
- **IOC closed:** accepted during RTH on the standard account (HTTP 200) —
  the "contact sales"/Elite footnote does not gate IOC submission. (Its
  instant-cancel semantics couldn't be observed due to the same paper stall.)
- Bench hardening added en route: pre-run orphan sweep (cancels any resting
  `ET-BENCH*` orders from an interrupted run — one such orphan was found and
  cleaned after a user-interrupted start), `--price-guard` flag.

## Alpaca LIVE addendum — 2026-07-07 (09:25–09:35 ET, symbol F)

Live keys added by Earl 2026-07-07 (`~/.eJournal/credentials.json` key
`alpaca-live`; paper stays under `alpaca`), small starter deposit the same
morning. Bench gained an `alpaca-live` venue (live base URL + creds key, same
guardrails). Account facts verified en route: **Alpaca has no retail cash
accounts** — every account opens as margin; under $2k equity it runs as
**limited margin** (`multiplier:1`, `shorting_enabled:false`,
`buying_power == cash`) but **unsettled funds are tradable** (the 09:08 ET
instant-ACH deposit traded at 09:34). **PDT is fully gone** — FINRA retired it
2026-06-04 (Alpaca "Intraday Margin Rule": unlimited day trades, deficit-based
calls only); confirmed `daytrade_count`/`pattern_day_trader` are now absent
from `/v2/account` (removed 2026-07-06). Day-trade count is no longer a bench
constraint; buying power per cycle is.

### Place+cancel probe (pre-market 09:25 ET; 3 reps, non-marketable $10 buy limits, 0 fills)

| leg | API return | push |
|---|---|---|
| place → `new` | 199–204 | 231–235 |
| cancel → `canceled` | 194–196 | 229–236 |

Live ≈ paper on the ack path (~230 ms vs paper ~210 ms); the ack push trails
the RPC return by ~30 ms (opposite of moomoo). Cancels are symmetric with
places.

### RTH round trip (09:34 ET, 1-share marketable limits)

| leg | API return | ack push | fill push | fill |
|---|---|---|---|---|
| buy (cold) | 209 | 239 | **239** | 13.7099 |
| sell (warm) | 196 | 232 | **232** | 13.7032 |

- **Ack and fill arrive in the same push.** Place→real-fill ≈ **0.23–0.24 s**
  — **Alpaca live is the fastest venue to actual execution**: Alpaca ~0.23 s <
  TZ 0.33–0.44 s < moomoo 0.87–1.04 s. Closes the "live fill quality unknown"
  caveat from 07-06. Both fills price-improved; round-trip cost 0.67¢ at
  1-share scale, no per-order platform fee (vs moomoo's US$0.99+GST).
- **Delayed-open lesson (first attempt 09:31):** a marketable buy (13.80 vs
  13.77 ask) rested `new` for 33 s and was cancelled by the bench timeout —
  **F's NYSE opening auction was delayed to ~09:33** (only 1–2.5k shares/min
  of stray off-primary prints until a 77.9k-share opening bar; meanwhile the
  consolidated NBBO looked normal and IEX quoted 13.08×14.57). Wholesalers
  hold marketable retail flow until the primary opening print. Consequence for
  eTape: **fill-timeout / marketable-order logic must not assume marketable ⇒
  filled**, especially in the minutes after 09:30 — cancel-on-timeout is the
  correct guardrail, and a "primary hasn't opened yet" state is worth
  surfacing in the order ticket.

Raw: `prototypes/captures/alpaca_live_place_cancel_20260707_*.json`,
`venue_latency_20260707_*.json`, `alpaca-live_bench_ws_*.json` — ⚠️ contain
the live account id; sweep before committing.
