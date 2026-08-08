# Stock Info

Stock Info publishes the existing `stock.detail` snapshot by combining:

- Moomoo fundamentals, industry, and exchange
- local daily-bar archive EMA-200
- a derived/manual Rule 201 short-sell restriction estimate from the same
  snapshot's regular-session low and prior close, with bounded recent daily-bar
  reads for next-trading-day carryover; this is not authoritative SIP/listing
  market status
- Alpaca asset metadata when an Alpaca venue is configured: borrow status,
  shortable, marginable, and tradable

Moomoo uses the broad focused/watch/scanner symbol universe. Alpaca's active
asset directory is loaded once during engine boot by the first configured
Alpaca adapter. Stock Info performs an in-memory lookup for every symbol in its
normal universe, so scanner/watch symbols can receive the same metadata without
focus-triggered requests, asynchronous refreshes, TTLs, or REST quota use.
The snapshot is session-static and remains unavailable when the startup load
fails or a symbol is not present.

SSR reuses the existing Qot_GetSecuritySnapshot response; it does not add a
market-data request or use Alpaca shortable/borrow fields.

Test: `go test ./internal/stockinfo`.
