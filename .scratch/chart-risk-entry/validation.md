# Chart Risk Entry validation

Baseline: `8a763dfc9b59d34d6f532e81ad1f93afbcd1d3ce`.

## Required checks

- `go test ./...`: passed, including the execution and UI hub suites.
- `go test -race -short ./...`: passed with the MSYS2 UCRT64 compiler. The first run failed in the existing OpenD timing tests; a complete captured retry passed. After review fixes, execution/UI hub race tests passed again, including a final execution retry.
- `go build ./cmd/etape`, `go vet ./...`, pinned golangci-lint 2.12.2: passed. Lint passed again after the final recovery fix.
- `mingw32-make -C engine gen-ts-check`: passed; generated contract has no drift.
- `npm ci`, `npm run lint`, `npm test`, `npm run build`: passed. The full UI suite and build passed again after review fixes. Build retains the existing chunk-size warning.
- `git diff --check` and Go LF worktree check: passed.
- No required local check skipped. Hosted CI must pass on the pushed main commit before handoff.

The approved public seams cover engine commands, broker events/recovery, chart pointer/hotkey-to-wire behavior, and settings persistence. Review regressions were observed failing before their fixes: snapshot fills increasing paused protection, waiting edits exceeding the reviewed quantity, and reconciled late fills submitting before manual Resume with healthy quotes.

## Additional E2E check

The isolated demo run on port 18786 finished with 14 passed, 17 failed, 1 skipped. The untouched baseline was archived into a temporary checkout, installed with `npm ci`, and run on port 18787: 13 passed, 18 failed, 1 skipped. This optional suite is outside current hosted CI and remains failing. Failures include old Settings import/layout expectations and session-sensitive order flows; they are not claimed as passing feature validation. Temporary checkout and generated reports were removed.

## Standards

The polling-demand ADR contradicted the new grouped Chart Risk Entry account demand. ADR0005 and the execution README were corrected. Reviewer verification: zero outstanding standards findings.

## Spec

Reconciled entry fills initially did not update linked protection; waiting amendments could increase quantity beyond the preview. Both were fixed with public regression tests. Follow-up review found reconciled late fills could activate automatically after quotes recovered; reconciliation now creates those exits paused until manual Resume. Reviewer verification: zero outstanding spec findings.

Findings: Standards 1 resolved; Spec 2 original findings plus 1 follow-up resolved. No outstanding findings on either axis. No live orders were placed, modified, or canceled.
