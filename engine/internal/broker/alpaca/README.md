# Alpaca Adapter

Paper/live REST execution and trade-update WebSocket normalization. Inputs:
venue-scoped keys and normalized orders; outputs: normalized lifecycle/account
data. Keep paper/live endpoints separate; history may reuse paper credentials
only.

The adapter also exposes a narrow read-only `AssetStatus` capability for Stock
Info. During engine startup, the first configured Alpaca adapter loads the
active directory with `GET /v2/assets?status=active` and stores the returned
`borrow_status`, `shortable`, `marginable`, and `tradable` metadata in memory.
Stock Info lookups are pure map reads and do not make REST requests; the
snapshot is treated as session-static until the next restart. This is
informational only: it is not real-time borrow availability, and future
hard-to-borrow short support still needs its own current execution validation
and locate workflow. This adapter capability does not submit orders or make
locate requests. The request uses the normal shared Alpaca REST rate limiter.

Test: `go test ./internal/broker/alpaca`.
