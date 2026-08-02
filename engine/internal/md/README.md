# Market Data Core

Builds books, quotes, bars, ticks, indicators from normalized events. TICKER creates exchange-time 10-second bars; one-minute K-lines support larger intraday resolutions; daily history supports daily/weekly/monthly. Same ordered events must reproduce state. Test: `go test ./internal/md`.
