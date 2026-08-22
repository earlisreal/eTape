# UI Hub

Config commands include typed `GetConfig`, `SetConfig`, and `DeleteConfig`; the workspace catalog remains a UI-owned versioned document in the existing config store.

Locate eligibility, quote, list, and recovery reads are UIHub queries. The
fee-bearing `RequestLocate` path is a command and returns the broker-confirmed
locate record in `AckMsg.Value`. UIHub receives an optional exact-venue locate
provider registry; unsupported venues fail closed rather than falling through
to another Alpaca account. Broker-backed locate queries and requests run off
the connection reader with correlation-preserving deferred replies, so a slow
Alpaca REST call cannot delay unrelated commands.

Local HTTP/WebSocket bridge. Publishes topic snapshots/updates; dispatches typed commands. Go `wsmsg/` structs own contract; generated TypeScript follows generator. Mirror supplies snapshot-on-subscribe and forwards the core-stamped Reported Print condition, raw type symbol, delivery source, and eligibility permissions unchanged; Significant Print remains the existing classifier. `md.tape.status` is a separate low-frequency per-symbol read model for pool, warmup, thresholds, and closed state. WebSocket pongs optionally carry the latest OpenD upstream-clock offset, sample age, and request RTT so managed charts can share one boundary clock; clients without that source fall back to browser time. On final clean engine shutdown, live WebSockets receive close code `1001` with reason `engine stopped`; self-restarts use `1000/restarting` so the preserved startup window reconnects; crashes and forced termination retain the normal reconnect behavior. Test: `go test ./internal/uihub`; `make gen-ts-check`.

The Wails `etape.runtime` Stream uses the same connection boundary: the
runtime validates the protocol, native Workspace identity, and opaque session
before `Server.HandleWailsStream` adapts it to `conn`. From that point Hub
registration, mirror snapshots, ordered outbox/coalescing, topic and demand
ownership, and disconnect cleanup are shared with the browser bridge. The
frontend application handshake is completed before `WsClient` enters `open`,
so its existing subscription and demand reannounce provides snapshot-before-
delta behavior without routing high-frequency data through React state or
ordinary Wails events.

Transport policy is deliberately bounded and loss-aware. `ServerConfig.OutBuf`
is the lossless FIFO frame cap; the latest-wins lane has at most 256 unique
keys, and eTape retains at most 8 MiB across both lanes. Frames are copied when
queued and again at the Wails `TrySend` ownership boundary. A replacement keeps
its original FIFO position; a new latest key or any lossless/byte overflow
closes the connection with an explicit overflow reason instead of silently
dropping an ordered frame. Beta.11 adds a 256-frame/8 MiB per-StreamConn queue,
so the declared per-session high-water bound is `OutBuf + 512` frame slots and
16 MiB of transport buffering (subject to Wails' application-wide ceiling).
Write timeout, frame-too-large, and transport-queue failures are disconnects;
the Hub remains non-blocking and reports its own lossless drop diagnostic to
surviving sessions.

The headless Wails server is test-only. `go test -tags "wails,server" ./cmd/etape`
starts the same bindings and `etape.runtime` handler on a loopback random port
in a child process with `ETAPE_PROFILE=server` and a temporary `ETAPE_DATA_ROOT`.
`/health` proves the HTTP listener is accepting requests; the authoritative
engine readiness signal is `RuntimeService.Capabilities().EnginePhase ==
"ready"`. The server build is never selected by the packaged desktop build,
which passes `noLegacyHTTP=true` and starts no historical localhost listener.
The test registry and stream sessions are process-local and must not be reused
across profiles or test cases.

The display-only Estimated LULD value is nested in the existing `md.book`
payload as optional `estimatedLuld`. The mirror caches it by symbol, merges it
into the cached book, republishes the ordinary book replacement, and includes
it in snapshots even when the derived update arrived before the first book.
There is no new WebSocket topic and no client-side market-data merge.

Feed connectivity is surfaced to subscribed UIs as low-frequency `sys.events`
`feed-up`/`feed-down` transitions. The periodic `sys.health` OpenD RTT probe
remains diagnostic and does not override the feed state shown to users.
