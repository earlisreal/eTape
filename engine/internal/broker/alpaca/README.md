# Alpaca Adapter

Paper/live REST execution and trade-update WebSocket normalization. Inputs:
venue-scoped keys and normalized orders; outputs: normalized lifecycle/account
data. Keep paper/live endpoints separate; history may reuse paper credentials
only.

The adapter also exposes a narrow read-only `AssetStatus` capability backed by
`GET /v2/assets/{symbol}` for Stock Info. It reports Alpaca's current
`borrow_status`, `shortable`, `marginable`, and `tradable` metadata without
using the deprecated `easy_to_borrow` field. This is informational only:
future hard-to-borrow short support still needs its own current execution
validation and locate workflow; this adapter method does not submit orders or
make locate requests. Asset reads use a non-blocking low-priority sub-budget of
50 requests/minute (burst 5) and leave one token in the shared 200 requests/minute
pool reserved for execution. When either budget has no capacity, Stock Info
skips that refresh instead of waiting; order/account endpoints retain the
blocking shared-pool admission path.

Test: `go test ./internal/broker/alpaca`.
