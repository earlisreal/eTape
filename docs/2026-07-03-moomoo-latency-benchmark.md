# moomoo OpenD market-data latency benchmark

Measured 2026-07-03 12:25 HKT (HK lunch break; US closed — July 4 observed);
**rerun in-session 14:03 HKT (HK afternoon session)** — all numbers reproduced
within noise, in-session deltas noted inline and in the rerun section at the end.
All numbers are session-independent request/response round-trips via the Python SDK
against local OpenD (`127.0.0.1:11111`). Script: `prototypes/moomoo_latency_bench.py`;
raw samples: `prototypes/captures/moomoo_latency_20260703_122627.json`.
Live push cadence (tick streaming rate) still needs an in-session measurement —
already covered separately by the 10s-bar verification (`prototypes/tick_to_10s_bars.py`).

## Results

| Operation | Latency | Notes |
|---|---|---|
| `OpenQuoteContext` create (InitConnect) | 1–3 ms | local handshake, negligible |
| `subscribe()` US symbol, QUOTE+ORDER_BOOK+TICKER | 45–49 ms | cold, per call |
| `subscribe()` HK symbol, same 3 types | 2–8 ms | reproduced in-session after quota release — genuine HK-vs-US difference, not cache warmth |
| `subscribe()` batch: 5 symbols × TICKER, one call | 50 ms total | **cost is per call, not per symbol** |
| subscribe → first cached push (QUOTE) | 3–312 ms | all sub-second |
| subscribe → first cached push (ORDER_BOOK) | 136–301 ms | |
| subscribe → first cached push (TICKER) | 83–92 ms | tight distribution |
| `get_stock_quote` 1 symbol | med 5.2 ms | OpenD local cache |
| `get_stock_quote` 6 symbols | med 5.3 ms | batch size ~free |
| `get_rt_ticker` num=1000 | med 31–33 ms | first call ~300 ms (SDK warm-up), then 25–36 ms |
| `request_history_kline` K_1M, 1000 bars (page 1) | 124–165 ms | upstream round-trip |
| `request_history_kline` K_1M page 2 | 61–70 ms | |
| `request_history_kline` K_DAY, 1 year (~250 bars) | 60–100 ms | |
| `unsubscribe_all` | 11 ms | |

History quota consumed: 3 slots (97/100 remain).

## Supplement (same day, 12:31 HKT): K_1M subscription + order book reads

Script: `prototypes/moomoo_latency_bench_supplement.py`;
raw: `prototypes/captures/moomoo_latency_supplement_20260703_123231.json`.

| Operation | Latency | Notes |
|---|---|---|
| `subscribe()` US symbol, K_1M+ORDER_BOOK | 42–46 ms | consistent with run 1: cost is per call |
| subscribe → first push (K_1M) | **261–369 ms in-session** (HK) | off-session: no push (fires on bar updates) — verified live 14:04 HKT |
| `get_cur_kline` K_1M num=1000 | med ~9 ms | **returned full 1000 bars** on both US.AAPL and HK.00700 — the sub cache spans ~2.5 US sessions of 1m bars, no history quota |
| `get_order_book` num=10 | med ~2.5 ms | US.AAPL: 10 levels (LV3 confirmed), HK.00700: 1 level (LV1 confirmed) |

**Revised chart-open path**: subscribe K_1M (+50 ms) → `get_cur_kline` seeds up to
1000 recent 1m bars in ~9 ms **locally, without touching the 100-slot history
quota**. `request_history_kline` K_1M is only needed for intraday depth beyond
1000 bars; K_DAY still fetched for daily. `get_order_book` at ~2.5 ms makes
initial DOM paint effectively free.

## In-session rerun (14:03 HKT, HK afternoon session)

Raw: `captures/moomoo_latency_20260703_140336.json` and
`captures/moomoo_latency_supplement_20260703_140456.json`. Deltas vs lunch-break run:

- **K_1M live push verified**: first 1m-bar push 261 ms (HK.00700) / 369 ms
  (HK.09988) after subscribe. US symbols: still none (market closed) — kline
  pushes fire on bar updates, so an idle symbol pushes nothing. Last caveat closed.
- **QUOTE first push is ~0 ms in-session** (was 3–312 ms off-session) — the
  cached snapshot arrives effectively together with the subscribe ack.
- **HK subscribe 3–8 ms reproduced** after full quota release → genuinely faster
  than US (~45–50 ms), not residual warmth from earlier testing.
- **History re-request of same symbols consumed 0 additional quota** (3/100
  before and after) — confirms the 30-day same-symbol dedup rule empirically.
- Everything else within noise of the first run (queries, order book, batch
  subscribe, history round-trips).

## Design consequences for eTape

- **Pre-subscribe the whole watchlist in ONE `subscribe()` call** at session start:
  a 5-symbol batch costs the same ~50 ms as a single symbol. A 20-symbol watchlist
  is one round-trip, not twenty.
- **Chart-open backfill is cheap**: full intraday 1m history (2 pages, 1560 bars)
  ≈ 230 ms + 1 year of daily ≈ 100 ms → a cold chart can be fully seeded in
  **~350 ms** on top of an already-subscribed symbol; a brand-new symbol adds
  ~50 ms subscribe + ~85 ms to first tick push.
- **Quote/tick queries hit OpenD's local cache** (5 ms / 30 ms) — no upstream cost,
  so the Go engine can poll-recover after a reconnect without quota or latency worry.
  The ~30 ms of `get_rt_ticker` is largely Python/pandas deserialization of 1000 rows;
  the Go protobuf client should come in well under that.
- Latency is dominated by per-call round-trips, not payload size → coalesce requests,
  don't fear big `max_count`.
