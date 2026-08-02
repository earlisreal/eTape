# Watchlist

Owns ordered membership, persistence, snapshot polling, and UI publication. Membership is authoritative; rows may lag and use placeholders. Normalize US symbols, dedupe, cap batch size. Test: `go test ./internal/watchlist`.
