# OpenD Quota

Tracks subscription/history usage and publishes contention state. Inputs: OpenD quota snapshots plus demand; outputs: admission/status. Centralize accounting; never infer availability from panel state. Test: `go test ./internal/quota`.
