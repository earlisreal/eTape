# Config

Loads, defaults, validates, and saves the selected runtime profile's
`config.toml`. The boot/profile resolver supplies the path; the real
`%USERPROFILE%\.eTape` root requires explicit opt-in. Missing sections receive
defaults; secrets belong in the profile's credentials store.
`[store].retention_days` controls boot-time 10s-bar calendar retention (30 by
default, 0 disables). `[news].yahoo_enabled` is an off-by-default experimental
Yahoo headline supplement. `[stockinfo].yahoo_metadata` enables the best-effort,
cached Yahoo profile Country/Sector lookup and Industry fallback; it defaults on
and can be disabled as a kill switch. Test: `go test ./internal/config`.
