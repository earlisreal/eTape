# Performance Evidence

Measurements describe Earl's network, entitlements, symbols, and market sessions; they are evidence, not service guarantees. Raw scripts and captures remain under [prototypes](../prototypes/README.md).

## Market data and quotas

- **2026-10-09 tick recording fixture (Windows):** 50 symbols, 500 Reported
  Prints/second for ten seconds, then a 500-print burst; 5,500 prints total.
  This is one demonstrated healthy workload, not a maximum-capacity guarantee.
  Recording on: maximum receipt-to-commit 252 ms, accounted work peak 7.8 MB,
  zero source losses and MD inbox drops. Maximum core chart barrier was 2.85 ms
  versus 0.80 ms off; execution-journal flush was 0.53 ms versus 0.33 ms off.
  Go user+GC CPU estimate was 0.425 seconds versus 0.194 seconds off, over
  roughly ten seconds. Allocations were 416 MB versus 348 MB, and final Go heap
  in-use was 413 MB versus 351 MB; those whole-process figures include the
  50-symbol tape/series fixture and are not recorder RSS. Sharing immutable
  source references reduced the disabled fixture's allocations from about
  480 MB to 348 MB compared with inline provenance.
  Sealed capture files plus metadata occupied 15.34 MB. Straight-line scaling
  at this fixture's bytes/print and a continuous 500 prints/second reaches
  10 GiB in about 2.1 hours, well before 30 calendar dates; real retention
  depends on report mix, sparse/periodic basis records, BOOK and symbol churn.
  This fixture measures core readiness, not browser painting or order latency,
  and uses synthetic frames and temporary local SQLite journals. Reproduce
  with `go test ./internal/tickstore -run TestRecordingLoadComparison -count=1 -v`
  from `engine/`; see [archive operations](../engine/internal/tickstore/README.md).

- **2026-07-03 OpenD request benchmark:** US subscribe calls measured 42-49 ms; five-symbol batched TICKER subscribe measured about 50 ms total. Cached one-symbol and six-symbol quote reads both measured about 5 ms. `get_cur_kline` for 1,000 one-minute bars measured about 9 ms. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-03-moomoo-latency-benchmark.md`.
- **2026-07-03 quota probe:** repeated history requests for the same symbols consumed no additional history slot. The account then reported 100 subscription slots and 100 historical K-line slots. Same source and `prototypes/moomoo_latency_bench*.py`.
- **2026-08-31 quota recheck:** OpenD reported 300 total stock subscription slots and 300 historical K-line slots. The 14-slot live subscription total exactly matched its per-subtype entries, including separate `K_DAY` and `K_1M` slots for the same symbol. Current moomoo v10.10 documentation defines stock history as one slot per symbol across periods in a rolling seven-day window and documents tier totals of 100, 300, 1,000, and 2,000. See [quota rules](https://openapi.moomoo.com/moomoo-api-doc/en/intro/authority.html), [subscription status](https://openapi.moomoo.com/moomoo-api-doc/en/quote/query-subscription.html), and [historical quota](https://openapi.moomoo.com/moomoo-api-doc/en/quote/get-history-kl-quota.html). eTape's runtime setting and built-in `feed.quota_slots` default were raised from 100 to the observed 300-slot entitlement after this recheck.
- **2026-07-03 pre-market rank:** poll RTT median 83 ms, p95 120 ms; hot-row change interval median 2.0 s; rank volume lag versus LV3 snapshot median 7 s and p95 17 s. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-03-premarket-scanner-api.md`; script `prototypes/premarket_rank_latency.py`.
- **2026-07-06 push cadence:** session-specific quote, ticker, order-book, and K-line observations live in `prototypes/push_cadence_measure.py` and captures. Do not generalize cadence beyond measured symbols/session. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-06-feed-measurements.md`.

## Execution

- **2026-07-06/07 venue runs:** observed real-fill latency was about 0.23 s Alpaca, 0.33-0.44 s TradeZero, and 0.9-1.0 s moomoo. Runs mixed venue, session, routing, and small-order conditions; comparisons are directional. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-06-venue-latency-benchmark.md`; harness `prototypes/venue_order_latency_bench.py`.
- Network-only checks earlier measured Alpaca REST around 210-214 ms and TradeZero around 272-301 ms, with TradeZero outliers. These are transport floors, not fill quality. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-03-alpaca-api.md`.

## Journal

- **2026-07-12 boot probe:** journal seal/vacuum timing and database-volume observations depend on retained days, event mix, storage, and SQLite state. Use source methodology before quoting any number: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-12-journal-seal-vacuum-boot-timing.md`.
- Production invariant matters more than snapshot size: one writer owns writes, WAL permits readers, failures surface without blocking market-data flow. See [store guide](../engine/internal/store/README.md).

## Captured Volume Profile (2026-10-10, Windows)

Go 1.26.5, Node 24.18.0, local Windows fixture; no broker traffic. `go test ./internal/tickstore -run TestRecordingLoadComparison -count=1 -v` adds four charts at one query/second each to the existing 50-symbol, 500-print/s workload for ten seconds plus a 500-print burst. With normal 256 MiB segments: 40 requests, maximum read+calculation 10.947 ms, maximum commit lag 247 ms, MD chart barrier 1.164 ms, zero recorded losses and no pause. Recording-only commit lag was also 247 ms (barrier 1.075 ms). This is fixture evidence, not a production latency guarantee.

The profiles variant deliberately rotates at 1 MiB. The final concurrent run (`-run TestRecordingLoadComparison/profiles`) uses four independent 1 Hz workers sharing the two-slot reader. It produced 40 requests: 15 populated results, 22 explicit Busy responses and three shutdown cancellations, with no timeouts. Maximum request time was 1,685.293 ms, commit lag 375 ms, MD chart barrier 7.794 ms, journal flush 4.878 ms, recorder working-byte peak 7,807,885; zero recorder loss/pause. Busy results retain a same-range Stale profile. Handles are closed between chunks, and waiting for maintenance owns no SQLite handle. The earlier 10.947 ms figure came from a serialized four-chart workload with normal segments; it is not concurrency evidence. Timings vary with concurrent test activity.
`TestVolumeProfileMaximumWorkingSet` allocates 22,037,592 bytes for 96,000 reports plus compact identity maps/calculation (including input); the reader separately caps SQLite cache at 1 MiB per open chunk and discards source references. Whole-engine load-fixture heap figures include journal/feed history and are not profile working-set measurements. `TestProfileReaderBoundsBusySymbolWithoutPresentingTruncatedVolume` rejects a 96,001-report sealed busy-symbol fixture within the two-second deadline; the UI query publishes no prefix bins. All relevant ET dates are scanned for conflicting identities, so even a narrow viewport on a very busy date may remain over budget. Rotation, late receipt-day prints, cancellation and uncoordinated-owner refusal also have public integration tests.