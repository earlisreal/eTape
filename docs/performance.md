# Performance Evidence

Measurements describe Earl's network, entitlements, symbols, and market sessions; they are evidence, not service guarantees. Raw scripts and captures remain under [prototypes](../prototypes/README.md).

## Browser-host migration baseline

The reproducible pre-Wails fixture is
[prototypes/browser-baseline/fixture.json](../prototypes/browser-baseline/fixture.json),
with its validation and run protocol in
[the fixture README](../prototypes/browser-baseline/README.md). It fixes a
Windows 11 x64 host, display setup, demo seed 42, twelve fictional symbols,
four browser Workspaces, twelve Panels per Workspace, a simulated-only order
intent, five minutes of warm-up, three fifteen-minute runs, one-second samples,
and ten open/close recovery cycles.

Each raw result records startup, bridge-to-store and simulated
order-intent-to-result latency, process-tree CPU/private memory, frame
intervals, queue high-water marks, coalesces, overflows, disconnects, drops,
and recovery. Repeat the exact fixture and protocol for the later Wails result;
compare p95 latency, steady CPU/private memory, frame intervals, queue behavior,
and recovery without hiding a lossless gap or disconnect inside an average.

The fixture, protocol, and logs contain no credentials, account data, or
captured private runtime data. Development, test, prototype, replay, demo,
server, and migration runs resolve isolated roots; the real `%USERPROFILE%\\.eTape` profile is
available only through the explicit `-profile user -allow-real-profile` opt-in.

## Market data and quotas

- **2026-07-03 OpenD request benchmark:** US subscribe calls measured 42-49 ms; five-symbol batched TICKER subscribe measured about 50 ms total. Cached one-symbol and six-symbol quote reads both measured about 5 ms. `get_cur_kline` for 1,000 one-minute bars measured about 9 ms. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-03-moomoo-latency-benchmark.md`.
- **2026-07-03 quota probe:** repeated history requests for same symbols consumed no additional slot during probe; subscription batching cost tracked calls more than symbol count. Base entitlement observed as 100 subscription slots and 100 historical K-line slots. Same source and `prototypes/moomoo_latency_bench*.py`.
- **2026-07-03 pre-market rank:** poll RTT median 83 ms, p95 120 ms; hot-row change interval median 2.0 s; rank volume lag versus LV3 snapshot median 7 s and p95 17 s. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-03-premarket-scanner-api.md`; script `prototypes/premarket_rank_latency.py`.
- **2026-07-06 push cadence:** session-specific quote, ticker, order-book, and K-line observations live in `prototypes/push_cadence_measure.py` and captures. Do not generalize cadence beyond measured symbols/session. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-06-feed-measurements.md`.

## Execution

- **2026-07-06/07 venue runs:** observed real-fill latency was about 0.23 s Alpaca, 0.33-0.44 s TradeZero, and 0.9-1.0 s moomoo. Runs mixed venue, session, routing, and small-order conditions; comparisons are directional. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-06-venue-latency-benchmark.md`; harness `prototypes/venue_order_latency_bench.py`.
- Network-only checks earlier measured Alpaca REST around 210-214 ms and TradeZero around 272-301 ms, with TradeZero outliers. These are transport floors, not fill quality. Source: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-03-alpaca-api.md`.

## Journal

- **2026-07-12 boot probe:** journal seal/vacuum timing and database-volume observations depend on retained days, event mix, storage, and SQLite state. Use source methodology before quoting any number: `41aa9993777cab4ea59e711775094c516032ebf2^:docs/2026-07-12-journal-seal-vacuum-boot-timing.md`.
- Production invariant matters more than snapshot size: one writer owns writes, WAL permits readers, failures surface without blocking market-data flow. See [store guide](../engine/internal/store/README.md).
