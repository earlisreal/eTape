# Config

`[tick_recording]` is a bootstrap-only section: `enabled = true`,
`retention_days = 30`, `max_bytes = 10737418240`, `min_free_bytes = 2147483648`.
These configure the separate [tick archive](../tickstore/README.md); they do not
change bar retention. Missing sections receive these defaults. Disabled or
`-demo` runs create no archive or recorder BOOK demand. Relaunch after edits;
there is no recorder settings UI in this milestone.

Loads, defaults, validates, and saves `~/.eTape/config.toml`. Outputs typed settings for boot/services. Missing sections receive defaults; secrets belong in credentials store. Broker venues, including moomoo paper and live accounts, are validated before writes. `[store].retention_days` controls boot-time 10s-bar calendar retention (30 by default, 0 disables). `[news].yahoo_enabled` is an off-by-default experimental Yahoo headline supplement. `[stockinfo].yahoo_metadata` enables the best-effort, cached Yahoo profile Country/Sector lookup and Industry fallback; it defaults on and can be disabled as a kill switch. Test: `go test ./internal/config`.
