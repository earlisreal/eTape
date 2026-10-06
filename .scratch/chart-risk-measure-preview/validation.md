# Chart Risk Entry measure preview validation

## Required local checks

- `npm ci`: passed using the locked dependency versions.
- `go build ./cmd/etape`: passed.
- `go test ./...`: passed.
- `go test -race -short ./...`: passed with the installed Windows toolchain.
- `go vet ./...`: passed.
- `golangci-lint run` (2.12.2): passed, zero issues.
- `mingw32-make -C engine gen-ts-check`: passed, no generated contract drift.
- `npm test`: passed, including the Windows golden-image suites.
- `npm run build`: passed, including both TypeScript projects.
- `npm run lint`: passed.
- `git diff --check`: passed. Draft spec/plan local links and whitespace passed;
  tracked Go worktree files remain LF. No runtime credentials/config were added.

## Focused interaction and layout checks

- Risk Entry component suite: nine tests passed. Covers repeated provisional
  mouse updates, actual arrow coordinates, pointer leave/re-entry, relayout
  without pointer movement, cushions/funding constraints, the completed quantity
  cap, stale account data, invalid/zero sizing, sub-dollar tick rounding, draft
  line/chip/keyboard edits, Enter/auto-send, cancellation and unknown outcomes.
- `npm run e2e:chart-layout`: passed, covering 17 simulated overlay states,
  five production ChartPanel states, moving risk feedback before the SELL click,
  centered arrow geometry, unfilled/no-rectangle styling, and retained draft
  feedback in light/dark/narrow/multiple-pane layouts.
- Inspected the generated light, dark and narrow screenshots. The label remains
  readable and clear of the price chips; chart dimensions and viewport remain
  unchanged. Screenshots stay in ignored `ui/e2e/.report/`.

## Scope and integration

All required local CI-equivalent checks ran. The full general Playwright suite
was not run: this UI-only change uses the dedicated simulated chart harness as
its proportional browser check. No live orders were used.

Scoped commit, upstream/main integration and push follow the repository guide.
Hosted CI must pass for the exact pushed main commit before final handoff; that
handoff supplies the commit/run link.
