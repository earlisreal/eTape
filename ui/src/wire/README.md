# Wire

WebSocket client, codec, reconnect, subscriptions, command correlation, and contract helpers. Inputs/outputs: JSON matching generated Go-owned types. Reconnect reannounces subscriptions/demand; unsent requests remain buffered, while sent requests settle on disconnect rather than wedging callers. A clean engine-shutdown close becomes terminal `stopped` state instead of retrying; crashes and forced termination still reconnect. Lifecycle, malformed-frame, and rejected-command diagnostics use `src/logging/logger.ts` without logging payloads or high-frequency market-data traffic. Test: `npm test -- wire`.

In a Wails host, `WailsStream` obtains a fresh opaque session through the
generated `RuntimeService` binding, opens `etape.runtime`, and sends the
protocol/Workspace/session declaration as the first frame. It withholds the
socket's `open` callback until the runtime accepts that declaration, then
forwards the existing JSON snapshot/delta/ack/result/pong frames to
`WsClient`. This preserves its subscription replay and DemandRegistry seam:
new sessions reannounce only after admission, while Hub owns snapshot ordering,
coalescing, and session cleanup. High-frequency data still goes directly to
imperative stores/controllers and the Scheduler, never React state or Wails
events.

The adapter treats `accepted`, `rejected`, `stopping`, `restarting`, and
`disconnected` as transport control frames. It does not deliver application
frames before `accepted`; a rejection or transport exception closes the
current session and lets `WsClient` apply its existing reconnect policy.
Synchronous Wails `send`/`close` failures are caught so a renderer reload or
close race cannot become an unhandled promise or leave a half-open client.
The Go side copies queued frames before the asynchronous Wails send, bounds
lossless and latest-wins queues, and disconnects explicitly at overflow;
latest-wins values may be superseded, but ordered snapshots/events are never
silently dropped.
