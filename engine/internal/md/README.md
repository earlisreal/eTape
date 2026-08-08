# Market Data Core

Builds books, quotes, bars, ticks, indicators from normalized events. TICKER creates exchange-time 10-second bars; one-minute K-lines support larger intraday resolutions; daily history supports daily/weekly/monthly. Same ordered events must reproduce state. `DropStats` distinguishes inbox/live-event drops from outgoing UI-update drops; keep-latest marks/books are intentionally not counted. Test: `go test ./internal/md`.
