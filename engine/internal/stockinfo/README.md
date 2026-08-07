# Stock Info

Stock Info publishes the existing `stock.detail` snapshot by combining:

- Moomoo fundamentals, industry, and exchange
- local daily-bar archive EMA-200
- Alpaca asset metadata when an Alpaca venue is configured: borrow status,
  shortable, marginable, and tradable

Moomoo uses the broad focused/watch/scanner symbol universe. Alpaca asset
requests are intentionally restricted to active/focused symbols and are read
sequentially on the refresh cadence so scanner-only symbols do not consume the
shared Alpaca execution REST quota. Alpaca failures are supplemental and do
not block publication of Moomoo data. Borrow status is refreshed rather than
permanently cached because availability can change.

Test: `go test ./internal/stockinfo`.
