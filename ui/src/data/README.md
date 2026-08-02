# Data Stores

Non-React mutable stores for bars, books, ticks, scanner, execution, watchlist, and session state. Wire registry routes topics; subscribers receive cheap invalidation callbacks. Inputs: snapshots/updates; outputs: synchronous reads for controllers. Preserve ordering/dedupe and bounded buffers. Test: `npm test -- data`.
