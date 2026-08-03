# Alpaca History

Fetches daily and one-minute market-data history. Free-SIP requests are capped at `now - 24h` for daily bars and `now - 16m` for one-minute bars. Only paper credentials may be reused automatically; live execution keys stay isolated. Normalize pagination, ordering, sessions. Test: `go test ./internal/hist/alpaca`.
