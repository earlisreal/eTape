# Stock Info

Stock Info publishes the existing `stock.detail` snapshot by combining:

- Moomoo fundamentals, industry, and exchange
- optional Yahoo profile Operating Country and Sector, with Yahoo Industry as
  a fallback only when Moomoo's Industry plate is blank
- local daily-bar archive EMA-200
- a derived/manual Rule 201 short-sell restriction estimate from the same
  snapshot's regular-session low and prior close, with bounded recent daily-bar
  reads for next-trading-day carryover; this is not authoritative SIP/listing
  market status
- Alpaca asset metadata when an Alpaca venue is configured: borrow status,
  shortable, marginable, and tradable

Moomoo uses the broad focused/watch/scanner symbol universe. Alpaca's active
asset directory is loaded once during engine boot by the first configured
Alpaca adapter. Stock Info performs an in-memory lookup for Alpaca status for
every symbol in its normal universe, so scanner/watch symbols can receive the
same metadata without focus-triggered requests or REST quota use.
The snapshot is session-static and remains unavailable when the startup load
fails or a symbol is not present.

Yahoo profile metadata is controlled by `[stockinfo].yahoo_metadata` (enabled
by default). It is fetched in the background at most once per symbol per day,
with a one-hour retry delay after failures. Rate-limit admission may wait for
the shared Yahoo bucket, while each session/profile request is bounded by an
8-second timeout. Cached values are used immediately; missing values hide the
Country/Sector rows and never delay Moomoo fundamentals. The Yahoo endpoint is
undocumented, so this path is deliberately optional and has no effect on quote
polling when disabled or unavailable.

SSR reuses the existing Qot_GetSecuritySnapshot response; it does not add a
market-data request or use Alpaca shortable/borrow fields. New live triggers
require the provider's latest-price update timestamp to be on the current ET
date and at or after that day's regular-session open; incomplete archive reads
use a temporary retryable cache.

Test: `go test ./internal/stockinfo`.
