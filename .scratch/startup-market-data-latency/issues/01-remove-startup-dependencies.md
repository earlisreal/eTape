# Remove startup dependencies

Type: task
Status: resolved

Implement the [approved scope](../spec.md) and
[plan](../../../docs/plans/2026-10-07-startup-market-data-latency.md).

## Comments

- Regression tests reproduced the subscription startup wait and managed REL VOL
  warming deadlock before their fixes. A pending-candle mock verifies independent
  book/ticker readiness; existing tests verify ticker cache-before-live ordering.
- Focused OpenD, Scanner, netx, Alpaca history, and engine-command suites pass.
  Native Windows tests require ordinary temporary-file access because the sandbox
  blocks atomic renames.

## Validation

Passed on Windows:

- `go test ./...`
- `go test -race -short ./...`
- `go vet ./...`
- `golangci-lint run` (2.12.2, zero issues)
- `mingw32-make -C engine gen-ts-check` (no generated drift)
- `npm ci`, `npm run lint`, `npm test` (including chart/Ladder/Tape goldens)
- `npm run build` (includes TypeScript checks)
- `go test -race ./internal/feed/opend -count=1` after final source review
- `go build -o <temporary validation binary> ./cmd/etape`
- `git diff --check`; tracked Go files have LF worktree line endings

No required local checks skipped. UI E2E was not run: no UI or contract changes,
and mocked provider regressions exercise the changed startup paths. The live
engine was not restarted; no live orders or provider requests were used for
verification. Real cold-start timing remains unmeasured. Hosted CI must pass
the integrated task commit before handoff; its run is referenced in the handoff.
