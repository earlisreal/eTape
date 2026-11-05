# Chart Risk Entry position adds validation

Design: [approved specification](spec.md).
Implementation: [plan](../../docs/plans/2026-11-05-chart-risk-position-adds.md).

## Covered behavior

- Fixed and trailing shared protection, inherited mode/cushion, and separate
  per-add sizing with existing position caps and the reviewed quantity ceiling.
- Two-point drag and click gestures, combined-position preview, keep-or-raise,
  stale-owner rejection and cancellation-pending admission blocking.
- First-fill tightening, actual partial-fill coverage, canceled/rejected buys,
  repeated adds after completion/cancellation, and confirmed partial sales.
- Shared-stop cancellation of pending buying, frozen main exits, late-fill
  cleanup, and absolute position updates arriving before fixed exit fills.
- Manual flat cancellation, durable shared links/first-fill markers, restart
  pause behavior, trailing progress, and stable pending-add execution pricing.

## Local checks

| Check | Result |
| --- | --- |
| `go test -p 1 ./...` | Passed, full engine suite |
| `go vet ./...` / `go build ./...` | Passed |
| `go test -race ./internal/exec ./internal/uihub` | Passed |
| `go test -race ./internal/exec -run TestRiskAdd -count=2` | Passed, including final fixed-exit attribution regression |
| `golangci-lint run` | Passed, zero issues |
| `npm test` | Passed, 177 files / 2,209 tests |
| `npm run lint` / `npm run build` | Passed; build includes typecheck |
| `npm run e2e:chart-layout` | Passed, including shared-stop add in a narrow chart |
| `mingw32-make gen-ts-check` | Passed against the staged Go-generated contract |
| Markdown links | Passed, 63 local targets including this record |
| `git diff --check` | Passed |

TypeScript was regenerated from the Go WebSocket types. Full hosted CI on
pushed main is the remaining integration gate and is reported in the handoff.

The Windows sandbox initially denied temporary-file renames in Go/Vitest;
affected checks passed when rerun with filesystem access. Browser validation
also repaired its missing ChartPreferencesProvider and outdated chrome-color
expectations. The full UI run exposed a saved-workspace test that clicked before
the asynchronous catalog loaded; it now awaits the workspace button.

Browser screenshot reviewed: `ui/e2e/.report/chart-risk-position-add-narrow.png`
(generated, ignored). No live-order or connected-broker verification was run;
this task authorizes implementation and simulator/fixture checks only.
