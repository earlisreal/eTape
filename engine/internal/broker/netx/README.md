# Broker Network Helpers

Shared HTTP retry, backoff, and rate-limit primitives. Inputs: idempotency-aware requests/context. Never retry ambiguous non-idempotent order submissions without adapter reconciliation. Test: `go test ./internal/broker/netx`.
