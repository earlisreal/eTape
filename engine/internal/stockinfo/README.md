# Stock Info

Stock Info publishes the existing `stock.detail` snapshot by combining:

- Moomoo fundamentals, industry, and exchange
- local daily-bar archive EMA-200
- Alpaca asset metadata when an Alpaca venue is configured: borrow status,
  shortable, marginable, and tradable

Moomoo uses the broad focused/watch/scanner symbol universe. Alpaca asset
requests are intentionally restricted to active/focused symbols. One
sequential, asynchronous refresh pass runs at a time, rotates its starting
symbol for fairness, and uses a small Alpaca asset sub-budget; scanner-only
symbols do not consume the shared execution REST quota. Moomoo publishes
immediately using the last successful in-memory status for the active symbol.
Alpaca failures or slow responses never block fundamentals; a completed refresh
is visible on the next Stock Info tick. Cached metadata expires after two Stock
Info refresh intervals, so prolonged failures and inactive symbols eventually
return to unavailable/null rather than displaying stale borrow status.

Test: `go test ./internal/stockinfo`.
