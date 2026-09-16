# Watchlist

Owns ordered membership, persistence, snapshot polling, and UI publication.
Membership is authoritative; rows may lag and use placeholders. Normalize US
symbols, dedupe, and cap snapshot work at eight attempts per poll. The shared
OpenD client paces 3203 requests at 550 ms minimum spacing. Binary splitting is
used only when the provider identifies a symbol-specific failure; transport,
decode, throttle, permission, and unknown failures leave rows cached and are
retried on the next poll. Test: `go test ./internal/watchlist`.
