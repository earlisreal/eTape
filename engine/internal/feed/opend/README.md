# OpenD Feed

BasicQot preserves provider health separately from price data: `isSuspended`
is an affirmative suspension signal, while optional `secStatus` maps to the
source-neutral `ProviderStatusUnknown`, `ProviderStatusNormal`, or
`ProviderStatusNonnormal`. Presence matters: an absent status is neutral, an
explicit normal status is healthy, and every explicit non-normal or unknown
enum is abnormal. The market-data core uses these signals only to freeze or
restart its display-only Estimated LULD approximation; they are not official
halt or LULD state.

Native TCP framing and protobuf client. Handles handshake, serial-number correlation, keepalive, subscriptions, snapshots, K-lines, scanner/news, and trade messages. Ticker updates preserve OpenD's raw `TickerType`, `typeSign`, and `PushDataType`, while the market-data core applies the centralized Trade-Report Condition eligibility table; known unusual/derived reports retain their evidence and may be volume-only or conditionally price-forming, and unrecognized values fail closed as unknown. Cached ticker seeds are sorted oldest-first and capped at 1,000 before they share the live event path. The reader counts/rate-limits raw push-buffer overflow warnings; established disconnects and successful reconnect resyncs are logged. Default downstream: `127.0.0.1:11111`. `pb/` is generated; never hand-edit. Preserve reconnect resync. Test: `go test ./internal/feed/opend`.
