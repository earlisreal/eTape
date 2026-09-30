# Restore the intraday price-axis countdown

Status: ready-for-agent
Completed: 2026-09-30

## Accepted scope

Restore the merged price/countdown badge on 10s, 1m, 5m, 15m, 30m, and 60m.
Preserve the enable setting, current-ET-day candle eligibility, weekday
04:00–20:00 ET session rules, and native crosshair precedence. D/W/M remain
excluded. Earl accepted the grilled recommendation on 2026-09-30.

## Diagnosis and fix

The badge mounted successfully, but the base axis canvas painted above it.
Both use z-index 1; React inserted the badge before an existing overlay sibling,
while Lightweight Charts appended its native DOM after those React children.
Changing only the badge's stacking level restored its pixels in the browser
probe. The final fix keeps the badge at z-index 1 and makes it the last React
child, ensuring it is appended after the base canvas and stays below the native
crosshair canvas at z-index 2.

The minimized browser check uses one initially assigned Chart Panel, a fixed
weekday RTH clock, and two fixed candles through QueryChartWindow. Before the
fix, the expected green badge pixel was white. The regression also checks all
six intraday intervals and the native crosshair painting above the badge.

Reproduction: `cd ui; $env:ETAPE_UIHUB_PORT='18686'; npm exec playwright -- test e2e/bar-close-timer.spec.ts --reporter=list`

## Validation

- Passed: `go test ./...`, `go test -race -short ./...`, `go vet ./...`,
  `golangci-lint run` (2.12.2), and `mingw32-make -C engine gen-ts-check`.
- Passed: `npm ci`, `npm run lint`, `npm test` (1,179 tests), and
  `npm run build` including both TypeScript projects, run by the E2E server.
- Passed: the final timer E2E file, two scenarios covering all six intraday
  intervals and native crosshair precedence. The Trading preset assigns a
  symbol, saves, and reloads to exercise initial chart mount.
- Passed: final test-file lint, `git diff --check`, Go worktree LF inspection,
  generated-contract drift inspection, and scoped review for runtime data.
- The full E2E suite reported 10 passed and 19 failed. Two new-test selector
  failures were corrected and both timer scenarios subsequently passed.
  The other 17 failures are in older reconnect, settings, preset/header,
  ticketless, and trade-history scenarios; this run does not establish that
  the whole E2E suite is green.
- Baseline comparison against unchanged ChartPanel at `fdb652b1`: both final
  timer scenarios fail on white pixels where the green badge should paint.
  The older reconnect-overlay, General import-control, and singleton-header
  scenarios reproduce their failures on the baseline as well. The remaining
  older failures were not individually rerun on the baseline.
- No required Windows CI-equivalent check was skipped. Hosted CI is verified
  after merge/push and reported in the handoff.

## Comments

- The unassigned-chart path loaded after mount already rendered the badge;
  the regression therefore starts with a symbol assigned at chart mount.
