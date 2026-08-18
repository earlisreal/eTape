# Health

Aggregates service readiness/degradation for UI and logs. Inputs: component status and cached active-Alpaca account health; outputs: health snapshot/events. Health reporting never performs an Alpaca REST request. The `engine-alpaca` link is present only while the global active venue is Alpaca and uses the latest `/v2/account` RTT. Test: `go test ./internal/health`.
