# Backfill

Coordinates history providers, archive coverage, demand, and merges. Inputs: symbol/resolution/range; outputs: normalized bars to archive/UI. Preserve ordering, dedupe, cancellation, provider boundaries. Test: `go test ./internal/backfill`.
