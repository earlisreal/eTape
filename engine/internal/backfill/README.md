# Backfill

Provider chains stop quietly when the caller cancels, without warning about a
provider failure or attempting a fallback. Actual provider failures still log
and advance through the configured chain.

Coordinates history providers, archive coverage, demand, and merges. `PrepareChart` owns focused chart seeding on a dedicated foreground slot; `WarmArchive` owns scanner/watch persistence and never seeds the market-data core. `ten_second_days` limits archived 10s bars by calendar days; its default `0` loads only the current trading cycle beginning at the latest NYSE close/post-market start. `intraday_days` is the calendar-day 1m retention window for focused charts and generic scanner/watch archive warming only; it is not a REL VOL lookback. Scanner REL VOL fetches its own Alpaca SIP history and never reads or writes `bars_1m`. `daily_years` is a calendar-year lookback, clamped to the provider floor. Focused preparation reads OpenD's 1m and daily caches concurrently, clips all three archive windows, and submits one ordered core message and prepared barrier. Older missing ranges fill the archive in the background and appear on the next symbol open. Preparation timing is one structured DEBUG summary, with a WARN only for successful loads slower than three seconds. Test: `go test ./internal/backfill`.
