# Config

`[tick_recording]` is a bootstrap-only section: `enabled = true`,
`retention_days = 30`, `max_bytes = 10737418240`, `min_free_bytes = 2147483648`.
These configure the separate [tick archive](../tickstore/README.md); they do not
change bar retention. Missing sections receive these defaults. Disabled or
`-demo` runs create no archive or recorder BOOK demand. Settings → Data & storage
edits the archive limits along with history, scanner, news and OpenD settings;
saved changes take effect on an explicit engine restart. Demo uses its own
runtime defaults, so file edits apply on the next live-mode launch.

Loads, defaults, validates, and saves `~/.eTape/config.toml`. Outputs typed settings for boot/services. Missing sections receive defaults; secrets belong in credentials store. Broker venues, including moomoo paper and live accounts, are validated before writes. `[store].retention_days` controls boot-time 10s-bar calendar retention (30 by default, 0 disables). `[news].yahoo_enabled` is an off-by-default experimental Yahoo headline supplement. `[stockinfo].yahoo_metadata` enables the best-effort, cached Yahoo profile Country/Sector lookup and Industry fallback; it defaults on and can be disabled as a kill switch. Test: `go test ./internal/config`.

Data settings writes check the original file values to reject stale edits and
preserve other typed sections. The legacy `[scan]` filter defaults are persisted
to the database on live boot before a subsequent UI/seed write removes their
TOML keys; saved per-panel filters remain authoritative. Demo and failed
migrations retain the old keys. `scan.rank_pages` and
`news.catalyst_min_score` are retired and ignored if present in older files.
