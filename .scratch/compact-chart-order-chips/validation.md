# Compact chart order chips validation

Validated on Windows, 2026-10-06.

## Passed

- `go test ./...`
- `go test -race -short ./...` with the existing MSYS2 UCRT64 compiler
- `go vet ./...`
- `golangci-lint run` (v2.12.2; zero issues)
- `mingw32-make -C engine gen-ts-check` (no wire-contract drift)
- `npm ci`
- `npm run lint`
- `npm test` (1,286 tests, including chart interaction and golden images)
- `npm run build` (includes both TypeScript checks)
- `npm run typecheck` (initial focused implementation check)
- Focused ChartOrderMarkers, ChartConditionalOrderEntry and ChartRiskEntry tests
- `npm run e2e:chart-layout` (17 simulated overlay states, five production chart
  states, gesture axis hover, separate side groups, same-side chooser, nearby
  price spacing, light/dark risk drafts and a narrow chart with a lower MACD pane)
- Screenshot inspection: compact side-colored axis controls, no routine left
  labels or risk amounts, separate controls for nearby prices, unchanged viewport
- `git diff --check`, LF Go worktree files and manual scope check for runtime data

No required CI-equivalent local check was skipped. The additional engine-backed
`npm run e2e` suite was not run: the simulated browser harness covers this chart
presentation change without an engine or broker connection.

Restricted Vitest startup could not resolve installed Vite paths; focused tests
passed with normal dependency access. The browser regression waits for the
asynchronous indicator-pane resize before checking narrow chart controls.

Hosted CI must pass after the main push; its result and run link are reported
in the final handoff. The unrelated `.scratch/chart-order-cursor/` draft is
excluded from this task's commits.
