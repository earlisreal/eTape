# Specifications

Current durable domain decisions:

- Go engine owns feed normalization, market state, persistence, scanning, execution, and WebSocket snapshots.
- UI keeps high-frequency data in imperative stores and canvas/chart controllers, outside React state.
- Go WebSocket structs are source of truth; generated TypeScript is read-only.
- Symbol demand is centralized, reference-counted, and quota-aware. Snapshot-on-subscribe prevents blank panels.
- Live and synthetic feeds enter the same market-data core; bars and execution state persist locally.
- Execution remains broker-agnostic above adapters, with global/per-venue gates and explicit venue arming.
- Demo state must not poison persisted live workspace or symbols.
- Exchange timestamps control bar buckets. Ticks create live 10-second bars; finalized one-minute K-lines conservatively bound completed 10-second highs/lows when open and close remain valid, and feed larger intraday resolutions. Daily history feeds daily/weekly/monthly.
- SQLite uses single-writer batching and WAL. Journal/archive failure is visible but must not stop live market flow.
- Orders use stable client IDs and normalized lifecycle events. Ambiguous submit outcomes require reconciliation, never blind duplicate submission.
- Watchlist membership is authoritative engine state; row snapshots may lag membership and render placeholders.
- Time & Sales Significant Prints are Go UI-hub annotations: separate RTH/Extended 2,000-print learning pools reset on the 20:00 ET cycle, emit low-rate `md.tape.status`, and carry `none`/`large`/`exceptional` on generated tick contracts. Minimum Trade Size is display-only.

Create focused approved specifications here as `YYYY-MM-DD-short-feature.md`. After implementation, keep durable decisions current and remove obsolete proposal detail.
