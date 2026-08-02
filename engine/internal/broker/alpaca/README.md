# Alpaca Adapter

Paper/live REST execution and trade-update WebSocket normalization. Inputs: venue-scoped keys and normalized orders; outputs: normalized lifecycle/account data. Keep paper/live endpoints separate; history may reuse paper credentials only. Test: `go test ./internal/broker/alpaca`.
