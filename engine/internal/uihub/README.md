# UI Hub

Config commands include typed `GetConfig`, `SetConfig`, and `DeleteConfig`; the workspace catalog remains a UI-owned versioned document in the existing config store.

Venue instrument eligibility, locate, quote, list, and recovery reads are UIHub
queries. Venue instrument eligibility is an optional exact-venue capability
with a shared 60-second engine cache; unsupported venues fail closed rather
than falling through to another account, and provider failures are diagnostic
only. The fee-bearing `RequestLocate` path is a command and returns the
broker-confirmed locate record in `AckMsg.Value`. Broker-backed queries and
requests run off the connection reader with correlation-preserving deferred
replies, so a slow external call cannot delay unrelated commands.

Local HTTP/WebSocket bridge. Publishes topic snapshots/updates; dispatches typed commands. Go `wsmsg/` structs own contract; generated TypeScript follows generator. The `exec.positions` payload carries `openedMs` when a position's opening fill is known, and `0` when it is unavailable. Mirror supplies snapshot-on-subscribe and forwards the core-stamped Reported Print condition, raw type symbol, delivery source, and eligibility permissions unchanged; Significant Print remains the existing classifier. `md.tape.status` is a separate low-frequency per-symbol read model for pool, warmup, thresholds, and closed state. WebSocket pongs optionally carry the latest OpenD upstream-clock offset, sample age, and request RTT so managed charts can share one boundary clock; clients without that source fall back to browser time. On final clean engine shutdown, live WebSockets receive close code `1001` with reason `engine stopped`; self-restarts use `1000/restarting` so the preserved startup window reconnects; crashes and forced termination retain the normal reconnect behavior. Test: `go test ./internal/uihub`; `make gen-ts-check`.
The `SetAccountDemand` command is connection- and panel-scoped: Account panels
announce their selected Link Group venue and release it on venue change,
unmount, or disconnect. The engine deduplicates these demands before polling;
the account row carries one Day P&L plus its broker/calculated source marker,
provisional-baseline marker, and timestamp.

The display-only Estimated LULD value is nested in the existing `md.book`
payload as optional `estimatedLuld`. The mirror caches it by symbol, merges it
into the cached book, republishes the ordinary book replacement, and includes
it in snapshots even when the derived update arrived before the first book.
There is no new WebSocket topic and no client-side market-data merge.

Feed connectivity is surfaced to subscribed UIs as low-frequency `sys.events`
`feed-up`/`feed-down` transitions. The periodic `sys.health` OpenD RTT probe
remains diagnostic and does not override the feed state shown to users.
