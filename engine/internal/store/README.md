# Store

SQLite journal, bars, execution state, settings, replay queries. Single writer owns writes; WAL permits readers; batching preserves order. Failure surfaces without blocking market flow. Runtime DB stays under `~/.eTape/`. Test: `go test ./internal/store`.
