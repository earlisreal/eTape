# Chart Risk Entry auto-send freshness validation

Date: 2026-10-06

Plan: [auto-send freshness](../../docs/plans/2026-10-06-chart-risk-auto-send-freshness.md).
Baseline: `fa302a5e` (includes the separate measure-preview change).

## Regression and fix

The first focused run with the new tests failed in three expected cases: delayed
drag completion, delayed click completion, and a failed submission-time refresh
with a still-fresh initial preview. The first two never auto-submitted; the third
continued through submission using the earlier preview.

The fix calls the existing submission path on initial auto-send completion,
returns immediately on a failed refresh, and deletes a previous preview error
after a successful refresh. Existing freshness, account, focus, venue, position,
reviewed quantity, busy, and unknown-outcome checks remain in place.

The focused suite passes all 22 tests. New coverage includes both delayed
placement gestures, unavailable/stale refreshed prints without automatic retries,
refresh failure with explicit Enter retry, clearing an earlier error, duplicate
Enter during a pending query, Escape/focus loss/symbol changes during a pending
query, and locked/disconnected/stale-account gates. Existing preview measurement,
quantity caps, endpoint edits, manual submission, and uncertain outcomes pass.

## Local checks

| Check | Result |
| --- | --- |
| `npx vitest run --project chart-panel src/chrome/panels/tv/ChartRiskEntry.test.tsx` | Passed: 22 tests; rerun after the final optional-property reset |
| `go test ./...` | Passed |
| `go test -race -short ./...` with `CGO_ENABLED=1` and installed UCRT64 GCC | Passed |
| `go vet ./...` | Passed |
| `golangci-lint run` (v2.12.2) | Passed: 0 issues |
| `mingw32-make -C engine gen-ts-check` | Passed: no generated contract drift |
| `npm ci` | Passed using the existing lockfile |
| `npm run lint` | Passed |
| `npm test` | Passed: 1,308 tests across 76 file runs, including goldens |
| `npm run build` | Passed: both TypeScript checks and production build |
| `git diff --check` and local Markdown link targets | Passed |
| Tracked Go line endings | No CRLF worktree files |

The Windows sandbox prevented Vite dependency resolution on the initial test
invocation. The normal commands passed when run outside that restriction. The
first build caught assignment of `undefined` to an optional property under
`exactOptionalPropertyTypes`; using `delete current.error` fixed it, and the
build and focused tests were rerun successfully.

No required CI-equivalent check was skipped. The separate browser E2E suite and
chart layout harness were not required for this submission-logic change; the
existing component fixture exercises the delayed pointer-to-command sequence
with simulated responses. No live orders were placed, modified, or canceled.
Dependencies, generated contracts, and runtime configuration were not changed.

## Delivery

Commit the scoped fix, executed plan, and validation record on the task branch,
integrate upstream main, merge/push main, and verify the task commit's hosted CI.
Hosted CI is checked after the push; the final handoff identifies that run and
its result.
