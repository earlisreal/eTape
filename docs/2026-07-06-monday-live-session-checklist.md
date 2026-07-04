# Monday 2026-07-06 live-session checklist

Everything that needs a live US market session, compiled from the open items of the
five research docs and three specs. Outcomes feed config defaults (engine/UI), the
venue-routing decision, and the moomoo unlock runbook.

**Safety rules for this session:**

- TradeZero keys on disk are **LIVE** — read-only endpoints only; the TZ benchmark
  leg runs on **paper keys** (see prerequisites).
- moomoo live orders are authorized (2026-07-04) but **re-confirm Earl's explicit
  go-ahead in the conversation that runs them**: 1-share marketable limits, cheap
  liquid symbol, flatten immediately, RTH only.
- Trade unlock happens **outside eTape** (OpenD GUI or manual SDK one-liner) —
  the trade password never touches eTape code.

## 0. Prerequisites (before the session)

- [ ] Generate **TradeZero paper API keys** — blocks the TZ benchmark leg and all
      v1 integration tests
- [ ] Extend `prototypes/tz_order_latency_bench.py` to three venues in one run:
      TZ paper + Alpaca paper (REST) + moomoo live (OpenD, Python SDK)
- [ ] OpenD running, quote + trade logged in; confirm US LV3 entitlement active
- [ ] Pick the benchmark symbol (cheap, liquid, tight spread)

## 1. Pre-market (from ~04:00 ET) — gap scanner verification

- [ ] Measure live refresh latency of `Qot_GetUSPreMarketRank` (3410) — expect
      seconds; this sets the scanner refresh cadence in the UI
- [ ] Confirm whether rank rows appear for symbols with tiny pre-market prints
      (CLLS appeared on 1,831 shares) → validates the mandatory volume floor
- [ ] Sanity-check `Qot_StockFilter` (3215) `FLOAT_SHARE` low-float universe
      against a known list (remember: unit = **thousands** of shares)

## 2. moomoo trade unlock (before the benchmark)

- [ ] **Does the OpenD GUI expose a trade-unlock control?** (skill docs say yes,
      official docs silent) → decides the unlock runbook: GUI per restart vs
      manual SDK one-liner
- [ ] Unlock via whichever mechanism exists

## 3. Order-latency benchmark (RTH, three venues, one session)

Routing input, not a broker decision. Record **place→ack** and **place→fill**
separately for every venue (moomoo's path is local TCP → OpenD → moomoo servers
vs direct REST for TZ/Alpaca — keep the OpenD hop visible).

- [ ] **TZ paper**: order POST → Portfolio-stream ack → fill
- [ ] **Alpaca paper**: order POST → `new` ack → `fill` on `trade_updates`
- [ ] **moomoo live**: `Trd_PlaceOrder` → order push ack → fill push
      (⚠️ real money — re-confirm go-ahead; 1 share, marketable limit, flatten
      immediately)
- [ ] Warm vs cold connection numbers (Alpaca cold TLS was ~430 ms; keep-alive
      pool assumed)

## 4. Broker side-checks (while connected)

TradeZero (paper, plus read-only on live keys):

- [ ] Capture real Portfolio/P&L WebSocket frames → freeze the Go structs
- [ ] Does the Portfolio stream emit one update per partial fill?
- [ ] `GET /routes` — actual routes on Earl's account tier
- [ ] WS staleness detection threshold

Alpaca (paper):

- [ ] Do IOC/FOK/OPG/CLS TIFs work on a standard (non-Elite) account?
- [ ] `client_order_id` reuse semantics after terminal states
- [ ] `trade_updates` granularity: one `partial_fill` event per execution?
- [ ] Paper binary-frame payload encoding (msgpack? JSON in binary frames?)

moomoo (paper unless noted):

- [ ] `Trd_GetFunds` day-P&L source field — USD `cashInfoList` shape on the
      universal account (needed for gate rule 5, global day-loss)
- [ ] Paper ETH contradiction; do order pushes arrive reliably on the US paper
      account? (decides whether polling is the primary fallback there)
- [ ] `Order.remark` echo on the fill-correlation path (fill push has no remark —
      join via `orderID`)
- [ ] Does cancel-all (`forAll`) ack synchronously or only via per-order pushes?
      (**paper only**)

## 5. Feed measurements (RTH, engine/UI config defaults)

- [ ] Steady-state push cadences for QUOTE / ORDER_BOOK / TICKER on hot symbols
      → tunes UI coalescing rate (default 30 Hz) and the OpenD push-frequency
      setting
- [ ] Journal volume with a realistic watchlist → MB/day (estimate 100–500)
      → decides whether the journal's JSON payload encoding stays
- [ ] Intraday history depth limit for `request_history_kline` K_1M (how far
      back does backfill actually reach?)
- [ ] Quota behavior under real load (subscription slots, historical slots)

## 6. Decisions this session produces

| Decision | Fed by |
|---|---|
| moomoo unlock runbook (GUI vs SDK one-liner) | §2 |
| Venue-routing defaults | §3 |
| UI coalescing rate + OpenD push frequency | §5 |
| Journal payload encoding (JSON stays or not) | §5 |
| Scanner refresh cadence | §1 |

Everything else is measurement recorded into the relevant research doc; update
each doc's "open items" section as items close.
