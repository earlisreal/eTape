# UI Hub

Config commands include typed `GetConfig`, `SetConfig`, and `DeleteConfig`; the workspace catalog remains a UI-owned versioned document in the existing config store.

Locate eligibility, quote, list, and recovery reads are UIHub queries. The
fee-bearing `RequestLocate` path is a command and returns the broker-confirmed
locate record in `AckMsg.Value`. UIHub receives an optional exact-venue locate
provider registry; unsupported venues fail closed rather than falling through
to another Alpaca account.

Local HTTP/WebSocket bridge. Publishes topic snapshots/updates; dispatches typed commands. Go `wsmsg/` structs own contract; generated TypeScript follows generator. Mirror supplies snapshot-on-subscribe. Test: `go test ./internal/uihub`; `make gen-ts-check`.
