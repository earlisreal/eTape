# Historical Bars

Provider-neutral history used by backfill/chart demand. Children: [Alpaca](alpaca/README.md), [Yahoo](yahoo/README.md). Inputs: symbol, resolution, range; outputs: ordered normalized bars. Callers merge/dedupe provenance. Test: `go test ./internal/hist/...`.
