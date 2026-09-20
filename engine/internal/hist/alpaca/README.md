# Alpaca History

Fetches daily and one-minute market-data history. Chart daily requests retain their `now - 24h` safety cap; chart one-minute requests retain `now - 16m`. Scanner REL VOL uses the raw daily entry point for one bounded range covering up to the prior 50 NYSE sessions, excludes the represented snapshot date, and discards raw bars after profile construction. Only paper credentials may be reused automatically; live execution keys stay isolated. Normalize pagination, ordering, sessions. Test: `go test ./internal/hist/alpaca`.
