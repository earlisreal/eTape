# Chart Risk Entry acceptance validation

Date: 2026-10-15

Design: [approved spec](spec.md).
Plan: [implementation](../../docs/plans/2026-10-15-chart-risk-accept-without-ticks.md).
Baseline: `97526f63`.

## Regression evidence

Before production changes, the new focused tests failed on missing/stale-trade
acceptance, a print expiring during subscription, the waiting label, and automatic
submission after a completed price correction. The UI run had 11 failing tests;
the engine run rejected missing/stale data and masked the ticker failure behind
the old market-freshness gate. Existing checks passed alongside these failures.

The fix admits a durable WAITING pair after ticker acquisition and preserves
fresh-print activation checks. Account age and session expiry are rechecked
after subscription acquisition; immediate activation also checks freshness and
expiry after persistence. Missing data has a visible waiting reason. Completed
draft endpoint corrections auto-send only after an actual price change and mouse
release. Incomplete BUY selection, unchanged clicks/horizontal drags, manual
keyboard submission, quantity caps, and unknown-outcome guards remain intact.

## Local validation

| Check | Result |
| --- | --- |
| Focused chart Risk Entry and order marker tests | Passed: 48 tests |
| Full execution package tests | Passed |
| Engine executable build (`go build ./cmd/etape`, temporary output) | Passed |
| `go test ./...` | Passed against the final implementation |
| `go test -race -short ./...` | Passed with `CGO_ENABLED=1` and UCRT64 GCC |
| `go vet ./...` | Passed |
| `golangci-lint run` | Passed: v2.12.2, 0 issues |
| `mingw32-make -C engine gen-ts-check` | Passed: no generated contract drift |
| `npm ci` | Passed; lockfile/dependencies unchanged |
| `npm run lint` | Passed |
| `npm test` | Passed: 1,338 tests across 78 file runs, including goldens |
| `npm run build` | Passed: both TypeScript checks and production build |
| Local Markdown targets and `git diff --check` | Passed |
| Tracked Go line endings | LF; no CRLF/mixed worktree files |

The initial sandboxed full engine/race runs could not rename temporary files or
reliably use local fixture servers; the sandboxed race runtime also failed to
restore an address. Both complete suites passed outside that restriction. The
first sandboxed contract check hit a Cygwin namespace permission error; the same
required command passed outside the restriction. No workaround changed source.

No required CI-equivalent check was skipped. The additional browser E2E suite
was not run: component fixtures exercise the actual pointer/keyboard-to-command
path, and engine fixtures cover acceptance, subscription delay/failure, trigger
activation, feed-gap pause, and existing recovery/protection behavior. No real
orders were placed, modified, or canceled. Credentials/runtime data, dependencies,
and the generated WebSocket contract were not changed.

## Delivery

Commit the scoped code, guides, approved spec/plan, and this validation record;
integrate upstream main, merge/push main, and verify hosted CI. The final handoff
reports that post-push run and its outcome.
