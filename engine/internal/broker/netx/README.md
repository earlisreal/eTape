# Broker Network Helpers

Shared HTTP retry, backoff, and rate-limit primitives. Inputs: idempotency-aware requests/context. Never retry ambiguous non-idempotent order submissions without adapter reconciliation. Test: `go test ./internal/broker/netx`.

`RestartCooldown` saves limited market-data attempt timestamps and observed provider cooldowns atomically before allowing requests. OpenD and Alpaca history use it to avoid a fresh full wait after an expired request window, while unknown state retains a conservative startup wait. State is scoped by provider identity and covers this engine only.
