# Alpaca Adapter

Paper/live REST execution and trade-update WebSocket normalization. Inputs:
venue-scoped keys and normalized orders; outputs: normalized lifecycle/account
data. Keep paper/live endpoints separate; history may reuse paper credentials
only.

The adapter exposes the narrow read-only `AssetStatus` capability for Stock
Info and the shared `VenueInstrumentEligibility` capability for the Top Bar.
During engine startup, every configured Alpaca adapter loads the active
directory with `GET /v2/assets?status=active` and stores the returned
`borrow_status`, `shortable`, `marginable`, `tradable`, `fractionable`, and the
`fractional_eh_enabled` asset attribute in memory.

Fractional long orders use DAY and accept share quantities up to nine decimal
places. Alpaca Paper accepts fractional submissions for testing. Pre-market and
after-hours require an eligible DAY limit. Live fractional openings,
extended-hours fractional routes, and price-only fractional replacement await
Paper execution verification. Live accounts can submit regular-hours DAY
reductions of confirmed fractional long positions.
Stock Info, venue instrument eligibility, and the locate workflow's borrow
lookup are pure map reads and do not make per-symbol REST requests; each
snapshot is treated as session-static until the next restart. The generic
capability excludes borrow status and remains informational only: it is not
real-time borrow availability, account authorization, or order approval.

The directory also seeds Stock Info's long and short margin percentages from
the decimal-string asset fields. Stock Info revalidates stale percentages with
`GET /v2/assets/{symbol}` through the same REST limiter, coalescing concurrent
reads per symbol. The refreshed margin cache leaves the startup eligibility
and borrow metadata intact.

`*Adapter` also implements the optional `locates.Provider` capability. Quote,
create, list, and get requests use `/v1/locates*`, retain decimal fee strings,
require a positive `limit_price`, and share the existing Alpaca REST auth and
rate limiter. Locate providers are registered by exact venue/account ID, so
multiple Alpaca accounts cannot silently share one account's locates. Locate
requests never submit short orders.

The engine-wide account poller requests each live Alpaca account required by
risk and each Alpaca account selected by an open Account panel (deduplicated by
venue). It performs an immediate and then 1 Hz low-priority `GET /v2/account`
refresh, emits the normal `BrokerAccount` event, and supplies the same request
RTT to health. Alpaca's broker-reported Day P&L is the displayed Day P&L;
eTape's cycle ledger supplies Realized P&L.

Test: `go test ./internal/broker/alpaca`.
