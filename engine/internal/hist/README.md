# Historical Bars

Provider-neutral history used by backfill/chart demand. Children: [Alpaca](alpaca/README.md), [Yahoo](yahoo/README.md). Inputs: symbol, resolution, range; outputs: normalized bars. The backfill boundary clips and deduplicates all results before persistence; out-of-window Yahoo context bars do not count as data, so fallback remains Alpaca first and Yahoo only after Alpaca has no in-range daily bars. Test: `go test ./internal/hist/...`.
