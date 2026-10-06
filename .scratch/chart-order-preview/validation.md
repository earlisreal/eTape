# Chart order preview validation

Status: implementation complete; local checks passed on 2026-10-07.

## Checks run

- `go build ./cmd/etape`: passed.
- `go test ./...`: passed on the full rerun.
- `go test -race -short ./...` with MSYS2 UCRT64 GCC: passed.
- `go vet ./...`: passed.
- `golangci-lint run` (2.12.2): passed, zero issues.
- `mingw32-make -C engine gen-ts-check`: passed; no generated contract drift.
- `npm ci`: passed with the existing lockfile; no dependency changes.
- `npm run lint`: passed.
- `npm test`: passed, including chart, execution, chrome, and golden rendering suites.
- Focused risk/gesture/crosshair checks: passed. Covered candidate movement from
  captured pointer events, selected pills, endpoint edits, tick rounding,
  stationary modifier activation, theme changes, horizontal visibility requests,
  cancellation, and blocked/submitted outcomes.
- `npm run build`: passed, including both TypeScript projects.
- `npm run e2e:chart-layout`: passed. Covered 17 simulated overlay states and
  five production Chart Panel states, both risk placement gestures, actual native
  vertical movement/horizontal suppression and restoration, line/label geometry,
  light/dark themes, pane boundaries, and narrow multi-pane risk setup.
- `ETAPE_UIHUB_PORT=8897 npm run e2e -- e2e/crosshair-sync.spec.ts`: passed;
  Crosshair Sync across two windows and clearing on leave, using the isolated demo
  engine and a temporary database.
- `git diff --check`: passed.
- Go worktree LF check: passed. No credentials or runtime data introduced.

The first full engine run failed in the unchanged Windows single-instance
subprocess lock test (`TestAcquireCrossProcessAndAutoRelease`). Its independent
`-count=1` rerun and both complete test/race runs passed. No engine change was
made for that failure. A Chart Panel visibility-options assertion initially
failed; narrowing the shared visibility setter fixed it, and the helper/Panel
checks plus the full UI suite passed afterward.

## Scope and handoff

No required local checks skipped. The complete Playwright suite is an additional
proportional check, not a CI requirement; targeted chart layout and Crosshair
Sync browser checks cover the changed flow. No live order actions were performed.

Commit, local/remote main containment, and hosted CI are verified before handoff.
