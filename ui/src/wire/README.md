# Wire

WebSocket client, codec, reconnect, subscriptions, command correlation, and contract helpers. Inputs/outputs: JSON matching generated Go-owned types. Reconnect reannounces subscriptions/demand; unsent requests remain buffered, while sent requests settle on disconnect rather than wedging callers. Lifecycle, malformed-frame, and rejected-command diagnostics use `src/logging/logger.ts` without logging payloads or high-frequency market-data traffic. Test: `npm test -- wire`.
