# Monday 2026-07-06 live-session checklist

Everything that needs a live US market session, compiled from the open items of the
five research docs and three specs. Outcomes feed config defaults (engine/UI), the
venue-routing decision, and the moomoo unlock runbook.

**Safety rules for this session:**

- The benchmark places **real-money orders on two live venues**: TZ live (paper
  keygen failed 2026-07-04, Earl authorized live instead) and moomoo live
  (authorized 2026-07-04). Both legs: 1-share marketable limits, cheap liquid
  symbol, **long side only** (buy → sell, no shorts/locates), flatten
  immediately, RTH only — and **re-confirm Earl's explicit go-ahead in the
  conversation that runs them** (per the CLAUDE.md safety rule).
- Outside the benchmark orders, TZ live keys stay **read-only**.
- Trade unlock happens **outside eTape** (OpenD GUI or manual SDK one-liner) —
  the trade password never touches eTape code.

## 0. Prerequisites (before the session) — ✅ done 2026-07-06 ~04:30 ET

- [x] Three-venue bench written + connect-dry-run passed:
      `prototypes/venue_order_latency_bench.py` (TZ live + Alpaca paper + moomoo
      live; `--live-go` + RTH gates on the live legs)
- [x] OpenD up, quote + trade logged in, US LV3 active (sub quota 100, hist 100)
- [x] Benchmark symbol: **F** (pre-market NBBO 13.38/13.42, ~0.3% spread, passes
      the <$30 / <2%-spread guards). Fallbacks if spread degrades: SIRI, T.

## 1. Pre-market (from ~04:00 ET) — gap scanner verification — ✅ done

Full results in `docs/2026-07-03-premarket-scanner-api.md` (open-items section):

- [x] Rank refresh: rows change every **~1–2 s**; RTT ~83 ms; **staleness vs
      real-time snapshot median 7 s / p95 17 s** → poll ~2 s, re-snapshot on hit
- [x] Tiny prints: rows ranked >+10% on **1 share** of volume → volume floor
      mandatory (confirmed, worse than CLLS)
- [x] FLOAT_SHARE unit (thousands) confirmed via boundary inspection; ⚠️ V1
      filter returns set membership only (no float values) — values via snapshot
      or V2 screener

## 2. moomoo trade unlock (before the benchmark) — ✅ done

- [x] **Mechanism: the OpenD GUI exposes an unlock-trade control** (Earl used it
      2026-07-06; the skill docs were right, official docs just omit it).
      **Unlock runbook: GUI unlock once per OpenD restart.** No SDK one-liner
      needed; the trade password never touches eTape.
- [x] Unlocked by Earl before the 2026-07-06 session

## 3. Order-latency benchmark (three venues, one session) — ✅ done pre-market

Run ~04:42–04:47 ET (Earl amended RTH-only → pre-market in-session; explicit
go-ahead re-confirmed before the live legs). **Full results:
`docs/2026-07-06-venue-latency-benchmark.md`.** All venues verified flat.

- [x] **TZ live** (2 cycles): API 283–327 ms, ack 326–409 ms, **fill 338–426 ms**
      — fastest real fills; fills @ 13.4172/13.41
- [x] **Alpaca paper** (3 cycles): API 196–201 ms, ack ~210 ms, paper-sim fills
      0.4–1.0 s (not comparable)
- [x] **moomoo live** (2 cycles): API 268–312 ms, ack 266–307 ms (push lands
      before the RPC returns — OpenD hop adds ~nothing to ack), fills
      0.87–1.04 s; US$0.99+GST/order platform fee (≈$4.30 for the leg)
- [x] Warm vs cold: Alpaca cold TLS ~580–660 ms reconfirmed; keep-alive pool
      mandatory (without it every POST ≈ 590 ms)
- [ ] Optional: RTH re-run tonight for market-hours comparison + Alpaca IOC
      acceptance retest

## 4. Broker side-checks (while connected)

TradeZero (live account — the benchmark's own orders plus read-only endpoints;
note: **paper keys still wanted eventually** — keygen failed 2026-07-04, they
gate v1 integration/E2E tests, not this session):

- [x] Portfolio WS frames captured live (order lifecycle PendingNew→New→Filled
      + Position frames; avg price = `priceAvg`; ⚠️ `GET /orders` list omits
      fill fields) → `prototypes/captures/tz_bench_ws_*.json`
- [x] Partial-fill granularity: per-execution fields (`lastPrice`/`lastQuantity`/
      `leavesQuantity`) on every update ⇒ one update per execution by
      construction; true multi-execution order still unobserved (qty-1 bench)
- [x] `GET /routes` captured: SMART (stocks: Market/Limit/Stop/StopLimit/MOC/LOC/
      Range/TrailStop; Day/GTC/ATO/Day_Plus/GTC_Plus), SMARTO, more →
      `prototypes/captures/tz_routes.json`
- [x] WS staleness: protocol **pings every ~54 s** when idle (10-min passive
      observation) → Go adapter read-deadline ≈ 120 s (2 missed pings) then
      reconnect + re-snapshot. Side-find: subscription name `Account` is
      rejected (`INVALID_DATA`); only `Order`/`Position` are valid.
      Details in `docs/2026-07-06-feed-measurements.md`.

Alpaca (paper) — ✅ done pre-market except partial-fill granularity
(details in `docs/2026-07-03-alpaca-api.md`):

- [x] TIFs: FOK/OPG/CLS **accepted** on the standard account; IOC rejected as
      "market hours only" (time gate, not entitlement) — re-verify IOC at RTH
- [x] `client_order_id` permanently consumed after terminal states (40010001) —
      same as TZ R114
- [ ] `trade_updates` granularity: one `partial_fill` event per execution?
      (needs a real partial — benchmark uses qty 1; opportunistic)
- [x] Paper stream = **plain JSON in binary frames** (no msgpack)

moomoo (paper unless noted) — ✅ done pre-market except fill-side remark join
(details in `docs/2026-07-04-moomoo-trading-api.md`):

- [x] `Trd_GetFunds` has **no day-P&L field** on the universal account
      (unrealized/realized N/A; per-currency cash blocks present; `currency=USD`
      converts) → gate rule 5 computes day-loss from eTape's own ledger
- [x] Order pushes DO arrive on the US paper account (SUBMITTING/SUBMITTED in
      0.3–0.9 s; 2/2) → polling is fallback, not primary. `fill_outside_rth=True`
      accepted on paper.
- [x] `Order.remark` fill-correlation path **confirmed live** (benchmark leg):
      deal push has no remark → join via `order_id`; order push echoes `remark`
      + `session:"ETH"` + `fill_outside_rth:true`
- [x] cancel-all on paper: **unsupported** — synchronous ret=-1, no pushes;
      per-order cancels only

## 5. Feed measurements (engine/UI config defaults) — ✅ done pre-market

Full results: `docs/2026-07-06-feed-measurements.md` (measured on the live
movers FXHO/ZCMD/LHSW + MSS/YHC + mega-caps, per Earl's ask).

- [x] Push cadences: ORDER_BOOK hard **~300 ms** OpenD cadence (~3.3/s/symbol);
      QUOTE/TICKER event-driven down to **87 ms** median gap on the hottest
      mover (6.9/s, ~9.5 ticks/s, ticks batched per push) → **30 Hz UI
      coalescing default stands with ample headroom**; OpenD push interval is
      the DOM's real refresh ceiling (tunable in OpenD if 300 ms feels slow).
      Bonus: 10×10 book verified on all three movers.
- [x] Journal volume: **~204 MB/hour** on a hot 12-symbol watchlist (11-min
      engine run) → ~1.3 GB RTH / ~3.3 GB 16 h — ~6× the 100–500 MB/day
      estimate. **Books are 75% of bytes (7.1 KB/row)**; ticks/quotes/bars are
      cheap → encoding decision narrows to the book payload (compress/delta/
      truncate); carried to the next engine design touch, nothing blocks v1.
- [x] Intraday history depth for `request_history_kline` K_1M: **≥7 years**
      (probes at 1y/2y/7y all returned data from the requested start; 1,000
      bars/page @ ~150 ms; RTH-labeled, END-stamped) — depth is a non-issue
      for chart-open backfill (verified 2026-07-06 pre-market)
- [x] Quota under real load: exactly as documented — engine watchlist = 28
      slots (12×TICKER/K_1M + 2×QUOTE/BOOK), overlapping second-connection
      subs cost nothing new, released on connection close; historical 4/100

## 6. Decisions this session produces

| Decision | Fed by | Outcome (2026-07-06) |
|---|---|---|
| moomoo unlock runbook (GUI vs SDK one-liner) | §2 | **GUI unlock once per OpenD restart** |
| Venue-routing defaults | §3 | Input recorded (`docs/2026-07-06-venue-latency-benchmark.md`): Alpaca fastest ack (~210 ms), TZ fastest real fills (0.34–0.43 s), moomoo ack ≈ TZ but fills ~1 s + $0.99/order fee. Defaults set during Plan 5/6 config work. |
| UI coalescing rate + OpenD push frequency | §5 | **30 Hz default stands**; OpenD book cadence 300 ms is the DOM ceiling (tunable in OpenD later) |
| Journal payload encoding (JSON stays or not) | §5 | JSON stays for ticks/quotes/bars; **book payload (75% of bytes) needs compress/delta/truncate** — carried to next engine design touch |
| Scanner refresh cadence | §1 | **~2 s poll**, re-snapshot candidates on hit (rank lags real time ~7–19 s) |

Everything else is measurement recorded into the relevant research doc; update
each doc's "open items" section as items close.
