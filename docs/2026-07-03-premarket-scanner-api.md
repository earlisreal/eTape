# Pre-market gap scanner — API research (verified 2026-07-03)

Goal: real-time scanner for Earl's pre-market workflow — US stocks up **≥10%**, filterable by
**price** and **float**, caught early while moving. Verdict: **moomoo OpenD covers this natively;
no external API needed.** All findings below verified live against OpenD (note: verified on the
July 4th-observed holiday, so data was Thursday 07-02's pre-market board — mechanics and payloads
confirmed, live refresh latency still to be checked Monday pre-market).

## The three APIs that compose the scanner

### 1. US Pre-Market Rank — `Qot_GetUSPreMarketRank` (proto **3410**)

SDK: `get_us_pre_market_rank`. The whole US universe ranked by pre-market change (sort_dir 0 =
gainers). **This is the scanner's engine.**

- Returns per stock: `pre_market_price`, `pre_market_change_ratio` (vs prev close),
  `pre_market_change_amount`, `pre_market_volume`, `pre_market_turnover`, plus RTH
  `close_price` / `change_ratio`.
- Paging: `count` ≤ 35/request, `offset`, `all_count` returned.
- Rate limit: **60 req / 30 s** → polling every 1–2 s costs 1–2 req (only the top 1–2 pages
  matter for a ≥10% cutoff). No subscription quota consumed.
- Server-side filters (`SimpleRankFilter`) are **coarse only**: `PRICE` (enum buckets `<1`,
  `1–10`, `10–100`, `>100`), `MARKET_CAP` (interval), `PE` (interval). ⚠️ The skill doc's
  `CHANGE_RATE` filter example is wrong — not in `SimpleRankIndicatorType`. Change/price/volume
  thresholds are applied client-side (trivial: the list arrives sorted by pre-market %).
- Verified filtered call (price $1–10 bucket + mktcap $10M–500M): universe 17,795 → 984,
  top of board = CWD +126% on 179M pre-market vol, USDE +86.5% on 21.8M — exactly the target list.
- Siblings for other sessions: `Qot_GetUSAfterHoursRank`, `Qot_GetUSOvernightRank`,
  `get_top_movers_rank` (RTH) — same shape, gives session continuity later.

### 2. Float filter / universe — `Qot_StockFilter` (proto **3215**, screener V1)

SDK: `get_stock_filter`. Supports `FLOAT_SHARE` and `FLOAT_MARKET_VAL` as native filter fields,
plus `CUR_PRICE`, `MARKET_VAL`, and accumulate `CHANGE_RATE(days)`.

- ⚠️ **`FLOAT_SHARE` unit is THOUSANDS of shares** (both filter input and returned value),
  despite the SDK comment claiming 单位:股. Verified: AAPL `float_share` = 14,642,591.8 ≡
  snapshot `outstanding_shares` 14,642,591,784. So "float ≤ 20M shares" → `filter_max = 20_000`.
- `MARKET_VAL` is raw dollars (AAPL ≈ 4.53e12). Percent fields are raw percent (10% → 10.0).
- Limits: **10 req / 30 s**, ≤ 200 results/page.
- Verified live query — US, price $1–20, float ≤ 20M, 1-day change ≥ +10%: 50 matches, correctly
  ordered, float values correct.
- SDK quirk (irrelevant to a Go protobuf client): accumulate results live in
  `FilterStockData.__dict__[("change_rate", days)]`, not as a plain attribute.
- ⚠️ `CUR_PRICE` / `CHANGE_RATE` here are **regular-session values** — during pre-market they are
  stale (prev close). That's why the rank API (3410) exists; V1's job in the scanner is only the
  **float/price universe**, not pre-market movement.

### 3. Float per symbol — `Qot_GetSecuritySnapshot` (proto 3203)

- `equityExData.outstandingShares` = **true free float** (raw shares), not shares outstanding.
  Proof: DJT issued 276.95M vs outstanding 163.6M (Trump's locked stake excluded);
  YRD issued 87.5M vs outstanding 15.0M. `issuedShares` = total; `outstandingMarketVal` = float mktcap.
- ≤ 400 codes/request, 60 req / 30 s, no subscription needed.
- ⚠️ **One bad code fails the whole batch**: OTC symbols without quote rights (e.g. delisted
  small caps — hit VNJA, BCLI live) error the entire snapshot call. Engine must split/retry
  dropping offending codes, or pre-filter OTC.

## Proposed scanner design (Go engine)

1. **Warm-up (daily, ~04:00 ET or engine start):** V1 filter dump of the low-float universe
   (e.g. float ≤ 50M, price $0.5–$50) → `map[symbol]float`. A few thousand rows = 15–25 pages
   ≈ 1–1.5 min at 10 req/30s. Floats change rarely → persist to SQLite, refresh daily.
2. **Poll loop (pre-market 04:00–09:30 ET):** rank API top 1–2 pages every ~2 s with the coarse
   server filters (price bucket + mktcap). Client-side: `pre_market_change_ratio ≥ 10`, precise
   price range, `pre_market_volume` floor (liquidity gate), float lookup from cache — miss →
   snapshot batch (handles fresh IPOs/uncached names; also naturally drops ETFs like the 2x MSTR
   funds that show up in the rank, since they have no equity float data).
3. **On hit:** emit scanner event to UI (new symbol row, flash) + optionally auto-subscribe
   TICKER/ORDER_BOOK/K_1M — dovetails with the existing "pre-subscribe the day's watchlist at
   session start" tick-persistence design (10s bars accumulate from detection moment).
4. Rank + filter + snapshot are all request/response — **zero subscription quota**, so the
   100-slot budget stays for TICKER/book/kline of actual watched symbols.

## Open items (Monday live session) — CLOSED 2026-07-06 pre-market

Measured live 04:12–04:16 ET (239 polls @1 Hz, top-35 rank + parallel real-time
snapshots of the top 5; raw: `prototypes/captures/premarket_rank_latency_*.json`,
script: `prototypes/premarket_rank_latency.py`):

- **Refresh latency**: hot symbols' rank rows change every **~1–2 s** (median 2.0 s
  per-symbol change interval, n=976) → the planned ~2 s poll cadence is right.
  Poll RTT median 83 ms (p95 120 ms).
- **Staleness vs real time**: rank values lag the LV3 snapshot by **median 7 s,
  p95 17 s, max 19 s** (measured via monotonic pre-market volume). Consequence:
  on a scanner hit, immediately re-snapshot the candidate — never trust rank-row
  price/volume for the alert payload.
- **Tiny prints: confirmed, worse than CLLS** — rows ranked >+10% on pre-market
  volume of **1 share** (XRTX, DK; several more <200 sh). The client-side volume
  floor is mandatory, full stop.
- **FLOAT_SHARE filter sanity (unit = thousands): confirmed** — `filter_max=50_000`
  returns a 3,888-symbol universe (with price $0.5–$50; 600 rows in 10.4 s ≈ well
  inside the warm-up budget) whose sampled snapshot floats all fall 3.7M–49.2M,
  hugging the 50M cap. ⚠️ The V1 filter does **not echo FLOAT_SHARE values** (even
  as sort field — `Unknown key`) → warm-up gives *set membership only*; float
  values come from snapshots on demand (or the V2 screener's `retrieves`).
  Also reconfirmed: OTC codes fail snapshot per-code ("US OTC market quote is not
  available"), preferreds/units return float 0 — drop both row types.
- Entitlement: worked with US LV3; not tested whether LV1 suffices (irrelevant for us).

## Alternatives considered (not needed)

Polygon.io (movers + shares outstanding, but no true float; $), FMP (has float; $), Alpaca
(top-movers API, no float, no pre-market depth). All add cost/integration for data moomoo already
provides with true float and verified pre-market volume. Decision: **build on moomoo 3410 + 3215 + 3203.**
