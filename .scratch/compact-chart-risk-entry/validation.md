# Compact Chart Risk Entry validation

Validated on Windows, 2026-10-06.

## Passed

- `go test ./...`
- `go test -race -short ./...` with the existing MSYS2 UCRT64 compiler
- `go vet ./...`
- `golangci-lint run` (v2.12.2; zero issues)
- `mingw32-make -C engine gen-ts-check` (no generated contract drift)
- `npm ci`
- `npm run lint`
- `npm test` (1,279 tests, including chart interaction and golden images)
- `npm run build` (includes both TypeScript checks)
- `npx vitest run --project chart-panel src/chrome/panels/tv/ChartRiskEntry.test.tsx` (three tests)
- `npm run e2e:chart-layout` (17 simulated order states, five production chart states, light/dark risk previews, and an active narrow resize with a lower MACD pane)
- Screenshot inspection: transparent text, no buttons, readable light/dark/narrow layouts, clear time axis and price gutter
- `git diff --check`, LF Go worktree files, and manual scope check for credentials/runtime data

No required CI-equivalent local check was skipped. The additional engine-backed
`npm run e2e` suite was not run: this presentation change is covered by the
simulated chart layout regression without a broker or engine connection.

Initial restricted-sandbox test startup failed; the same checks passed using
the existing normal dependency caches and installed Chromium. A first browser
run exposed missing indicator-subscription support in the test harness; adding
its simulated acknowledgement allowed the lower-pane scenario to pass.

Hosted CI must pass after the main push; its final result is reported at handoff.
