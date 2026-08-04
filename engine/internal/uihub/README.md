# UI Hub

Config commands include typed `GetConfig`, `SetConfig`, and `DeleteConfig`; the workspace catalog remains a UI-owned versioned document in the existing config store.

Local HTTP/WebSocket bridge. Publishes topic snapshots/updates; dispatches typed commands. Go `wsmsg/` structs own contract; generated TypeScript follows generator. Mirror supplies snapshot-on-subscribe. Test: `go test ./internal/uihub`; `make gen-ts-check`.
