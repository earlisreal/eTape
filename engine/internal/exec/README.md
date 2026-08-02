# Execution Core

Broker-neutral lifecycle, gates, routing, reconciliation, round-trip tracking. Inputs: UI commands; outputs: adapter requests and normalized state. Armed venue plus global/per-venue checks required. Stable client IDs prevent duplication. Test: `go test ./internal/exec`.
