# Engine Scripts

Build/generation helpers used by Make targets. Inputs: Go sources/toolchain; outputs: generated TypeScript or release artifacts. Generated output must reproduce cleanly. Verify: `make gen-ts-check`.

`python scripts/calibrate_protective_prices.py <segment.sqlite> [...]` performs
read-only, bounded protective-price diagnostics against tick archives, with normal
SQLite WAL consistency. It prints aggregates only and does not label bad prints
or simulate fills. See [calibration limits](../../.scratch/protective-stop-spike-filter/calibration.md).
