# Store

The execution store also persists close-to-close account-cycle checkpoints; `QueryCycleFills` returns one venue's boundary, carried quantities, and chronological fills.

SQLite journal, bars, execution state, settings, replay queries. Single writer owns writes; WAL permits readers; batching preserves order. At boot, `bars_10s` is pruned to the rolling calendar-day window configured by `[store].retention_days` (30 by default, 0 disables); large freelists reuse the existing conditional vacuum. `bar_archive_ranges` records successfully explored provider intervals, including empty results; `MissingRanges` merges overlapping/adjacent rows and returns only uncovered gaps without changing the compatible schema. Failure surfaces without recording coverage. Runtime DB stays under `~/.eTape/`. Test: `go test ./internal/store`.
