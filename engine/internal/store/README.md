# Store

SQLite journal, bars, execution state, settings, replay queries. Single writer owns writes; WAL permits readers; batching preserves order. `bar_archive_ranges` records successfully explored provider intervals, including empty results; `MissingRanges` merges overlapping/adjacent rows and returns only uncovered gaps without changing the compatible schema. Failure surfaces without recording coverage. Runtime DB stays under `~/.eTape/`. Test: `go test ./internal/store`.
