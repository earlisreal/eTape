# Config

Loads, defaults, validates, and saves `~/.eTape/config.toml`. Outputs typed settings for boot/services. Missing sections receive defaults; secrets belong in credentials store. `[store].retention_days` controls boot-time 10s-bar calendar retention (30 by default, 0 disables). `[news].yahoo_enabled` is an off-by-default experimental Yahoo headline supplement. Test: `go test ./internal/config`.
