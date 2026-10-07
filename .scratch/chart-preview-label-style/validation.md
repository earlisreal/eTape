# Chart Preview Price Label validation

Status: local implementation checks passed on 2026-10-07.

## Checks run

- Focused Chart Order Gesture / Chart Risk Entry tests: passed, 49 tests.
- npm ci: passed with the existing lockfile; dependencies unchanged.
- npm run lint: passed.
- npm test: passed, including all UI and golden rendering suites.
- npm run build: passed, including both TypeScript projects.
- npm run e2e:chart-layout: passed, 17 simulated overlay states and five
  production Chart Panel states. Verifies axis-left alignment, normal 12px
  text, 21px height, white foreground, green/red background, both themes,
  risk placement/editing, pane boundaries, narrow multi-pane layout, retained
  pills, and crosshair suppression/restoration.
- Existing component tests retain four-decimal sub-$1 and two-decimal price checks.
- Gesture and risk-drag screenshots: inspected; labels match the approved design.
- git diff --check: passed. Generated WebSocket contracts unchanged.
- Go worktree line endings: LF. No credentials or runtime data introduced.

The initial extended browser check timed out because its cursor remained over
an order-cancel button when pressing Shift. Moving the test cursor back into
the chart fixed activation; the complete browser check then passed.

## Proportional scope

No required UI checks skipped. Local engine build, full/race tests, vet,
golangci-lint, and gen-ts-check were not rerun for this small, isolated UI
presentation change; engine code, build configuration, dependencies, and
contracts are unchanged. The complete engine-backed Playwright suite was also
skipped; the simulated chart browser harness covers the changed presentation.
Hosted CI runs the complete repository checks after main integration and push.
Task commit containment in local/remote main and the hosted CI result are
verified separately before handoff.
