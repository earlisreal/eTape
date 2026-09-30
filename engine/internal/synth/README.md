# Synthetic Market

Generates deterministic demo universe, history, ticks, books, scanner movement, and execution inputs. Same feed/domain boundaries as live mode. Seed controls reproducibility; demo state cannot leak into live persisted workspace. Test: `go test ./internal/synth`.

Fresh ticks emitted during live generator steps carry realtime provenance so
they can drive execution-grade held-order triggers; warmed history remains
seed data and is never treated as a trigger print.
