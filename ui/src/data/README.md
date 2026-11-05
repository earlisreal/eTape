# Data Stores

`ScreenerStore` keeps low-frequency `screener.state` progress/results snapshots
isolated by workspace/panel identity. Superseded deltas are ignored; reconnect
snapshots can restart engine run IDs. Completed rows stay stable until an
explicit new screen. It never consumes live ticks or quote subscriptions.

Non-React mutable stores for bars, books, ticks, scanner, execution, watchlist, and session state. Wire registry routes topics; subscribers receive cheap invalidation callbacks. Inputs: snapshots/updates; outputs: synchronous reads for controllers. Preserve ordering/dedupe and bounded buffers; IndicatorStore series are strictly ascending and unique by timestamp, including late deltas. Test: `npm test -- data`.
