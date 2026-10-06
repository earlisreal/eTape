# Chart Order Selection Crosshair validation

Status: implementation complete; local checks passed.

- `go test ./...`: passed.
- `go test -race -short ./...` with MSYS2 UCRT64 GCC: passed.
- `go vet ./...`: passed.
- `golangci-lint run` (2.12.2): passed, zero issues.
- `mingw32-make -C engine gen-ts-check`: passed, no generated contract changes.
- `npm ci`: passed with the existing lockfile; no dependency changes.
- `npm run lint`: passed.
- `npm test`: passed, including chart, execution, chrome, and golden rendering suites.
- Targeted chart checks after the final test additions: passed; exact-price native crosshair, immediate modifier activation, buy/sell phases, first-BUY editing, nearest/tied line selection, modifier precedence, pointer ownership, and cancellation covered.
- `npm run build`: passed, including both TypeScript projects.
- `npm run e2e:chart-layout`: passed; 17 simulated overlay states, five production Chart Panel states, direct line dragging and resize feedback, native crosshair/price-label colors, light/dark and narrow multi-pane risk setup.
- `git diff --check`: passed.
- Go worktree LF check: passed.

No required local checks skipped. The full real-engine Playwright suite is not a current CI requirement; the focused simulated chart browser suite covers this UI change. No live order actions were performed.

The commit is integrated into local and remote main; hosted CI results are verified before handoff.
