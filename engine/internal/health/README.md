# Health

Aggregates service readiness/degradation for UI and logs. Inputs: component status; outputs: health snapshot/events. Health reporting must not own recovery policy. Test: `go test ./internal/health`.
