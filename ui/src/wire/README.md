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
