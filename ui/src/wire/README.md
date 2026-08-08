# Wire

WebSocket client, codec, reconnect, subscriptions, command correlation, and contract helpers. Inputs/outputs: JSON matching generated Go-owned types. Reconnect reannounces subscriptions/demand; stale socket events cannot mutate current session. Lifecycle, malformed-frame, and rejected-command diagnostics use `src/logging/logger.ts` without logging payloads or high-frequency market-data traffic. Test: `npm test -- wire`.
