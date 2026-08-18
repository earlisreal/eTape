# Alpaca Adapter

Paper/live REST execution and trade-update WebSocket normalization. Inputs:
venue-scoped keys and normalized orders; outputs: normalized lifecycle/account
data. Keep paper/live endpoints separate; history may reuse paper credentials
only.

The adapter also exposes a narrow read-only `AssetStatus` capability for Stock
Info and locate eligibility. During engine startup, every configured Alpaca
adapter loads the active directory with `GET /v2/assets?status=active` and
stores the returned `borrow_status`, `shortable`, `marginable`, and `tradable`
metadata in memory. Stock Info and `LocateEligibility` lookups are pure map
reads and do not make per-symbol REST requests; each snapshot is treated as
session-static until the next restart. This is informational only: it is not
real-time borrow availability, and hard-to-borrow support still requires the
explicit locate quote/reservation workflow.

`*Adapter` also implements the optional `locates.Provider` capability. Quote,
create, list, and get requests use `/v1/locates*`, retain decimal fee strings,
require a positive `limit_price`, and share the existing Alpaca REST auth and
rate limiter. Locate providers are registered by exact venue/account ID, so
multiple Alpaca accounts cannot silently share one account's locates. Locate
requests never submit short orders.

`AccountPoller` follows the single global active venue. When that venue is
Alpaca it performs an immediate and then 1 Hz low-priority `GET /v2/account`
refresh, emits the normal `BrokerAccount` event, and supplies the same
request RTT to health. It never polls inactive Alpaca accounts and has no
separate clock probe.

Test: `go test ./internal/broker/alpaca`.
