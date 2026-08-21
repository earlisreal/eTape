# Market Data Core

`EstimatedLULD` is a display-only derived value. It uses only stamped
Last-Eligible prints, a bounded five-minute local window, the injected core
clock, Moomoo's previous close for the documented price bucket, and the dated
allowlist in `luld_registry.json`. It is available only during scheduled RTH;
unknown or expired symbols are unavailable, and provider/transport interruptions
freeze the last local result. It never gates orders, risk, trading state, or
official LULD claims. `EstimatedLULDUpdate` is coalesced by visible value, so
ordinary ticks do not create a derived update for every print.

Builds books, quotes, bars, ticks, indicators from normalized events. TICKER creates exchange-time 10-second bars after one centralized Trade-Report Condition policy stamps Range-Eligible, Last-Eligible, and Volume-Eligible permissions; one-minute K-lines support larger intraday resolutions; daily history supports daily/weekly/monthly. Same ordered events must reproduce state. Volume-Only Bars use eligible volume and a trusted prior last-eligible close; unanchored buckets remain held, and older history cannot replace a newer trusted anchor. `DropStats` distinguishes inbox/live-event drops from outgoing UI-update drops; keep-latest marks/books are intentionally not counted. Test: `go test ./internal/md`.
