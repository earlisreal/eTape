# UI Hub

Config commands include typed `GetConfig`, `SetConfig`, and `DeleteConfig`; the workspace catalog remains a UI-owned versioned document in the existing config store.

Locate eligibility, quote, list, and recovery reads are UIHub queries. The
fee-bearing `RequestLocate` path is a command and returns the broker-confirmed
locate record in `AckMsg.Value`. UIHub receives an optional exact-venue locate
provider registry; unsupported venues fail closed rather than falling through
to another Alpaca account. Broker-backed locate queries and requests run off
the connection reader with correlation-preserving deferred replies, so a slow
Alpaca REST call cannot delay unrelated commands.

Local HTTP/WebSocket bridge. Publishes topic snapshots/updates; dispatches typed commands. Go `wsmsg/` structs own contract; generated TypeScript follows generator. Mirror supplies snapshot-on-subscribe and forwards the core-stamped Reported Print condition, raw type symbol, delivery source, and eligibility permissions unchanged; Significant Print remains the existing classifier. `md.tape.status` is a separate low-frequency per-symbol read model for pool, warmup, thresholds, and closed state. WebSocket pongs optionally carry the latest OpenD upstream-clock offset, sample age, and request RTT so managed charts can share one boundary clock; clients without that source fall back to browser time. Test: `go test ./internal/uihub`; `make gen-ts-check`.
