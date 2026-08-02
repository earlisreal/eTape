# Synthetic Market

Generates deterministic demo universe, history, ticks, books, scanner movement, and execution inputs. Same feed/domain boundaries as live mode. Seed controls reproducibility; demo state cannot leak into live persisted workspace. Test: `go test ./internal/synth`.
