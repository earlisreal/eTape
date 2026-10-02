# moomoo Broker Adapter

Native OpenD `Trd_*` execution adapter for both paper and live accounts.
Inputs: account selection and normalized orders; outputs: normalized
pushes/snapshots. Trade unlock stays in OpenD GUI. Account discovery and
validation request universal securities accounts via
`Trd_GetAccList.needGeneralSecAccount=true`; disabled legacy accounts remain
excluded. The engine-wide account
poller reads `Trd_GetFunds` plus the daily flow summary, then calculates
close-to-close Day P&L from the persisted baseline after signed deposits and
withdrawals; the UI labels this source `Calculated`. Paper accounts are shown
but excluded from Max Day Loss. The optional `VenueInstrumentEligibility`
capability queries `Trd_GetMarginRatio` with the exact configured account and
environment, mapping long/short permits to marginable/shortable and leaving
tradable unsupported. Test: `go test ./internal/broker/moomoo`.
