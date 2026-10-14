# Chart price-label priority validation

Plan: [approved design](../../docs/plans/2026-10-14-chart-price-label-priority.md).

## Changes

Order Price Pill graphics now interleave with the native axis canvases in one
passive chart-host slot. Original controls retain input, titles and accessibility;
their graphics are inert, hidden from accessibility and synchronized imperatively.
One foreground pill follows drag, then hover, then keyboard-focus priority.
Risk Entry previews remain at the actual candidate price, bounded at pane edges.

## Checks

- Test-first component regressions reproduced the missing passive graphics and
  the Risk Entry preview moving to 222px instead of the candidate's 209px.
- Focused order-marker and Risk Entry tests passed: 41 tests, including interaction
  priority, release/pending replacement, cancellation, context change and disposal.
- Typechecking passed after each integration slice.
- The simulated real-chart layout regression passed: 17 overlay states, five
  production PanelFrame/ChartPanel states, light/dark Risk Entry and narrow
  multi-pane layout. Pixel samples verify timer, native crosshair and colored
  preview over resting pills; hover/focus/drag expose the active pill. Graphic
  and control bounds agree; native price-scale gestures remain usable.
- Additional browser samples verify native cursor precedence over both draft
  endpoints, a SELL preview over a nearby selected BUY, and hover revealing each
  endpoint. Working-order release over the replaced pill retains hover priority;
  leaving it ends that priority. Only one pill is in front.
- A review regression reproduced consumed Risk Entry arrow edits leaving the
  focused pill behind. Window capture now observes those keys before Risk Entry
  consumes them; pointer input ends keyboard promotion. The regression passes.
- Passed: `go build ./cmd/etape`, `go test ./...`, `go test -race -short ./...`,
  `go vet ./...`, and `golangci-lint run` with the pinned version 2.12.2.
- Passed: `mingw32-make -C engine gen-ts-check`; generated sources have no drift
  and all tracked Go worktree files use LF.
- Passed: `npm ci`, `npm run lint`, the full `npm test` suite (1,331 tests), and
  `npm run build` including both TypeScript projects.
- Passed: both saved-chart and Trading-preset timer regressions on isolated demo
  port 18687, covering all six intraday intervals and native crosshair precedence.
- Standards review: zero remaining documented breaches or actionable smells.
  The stale TradingView guide was corrected.
- Spec review: zero remaining actionable findings after the keyboard fix and
  expanded browser coverage.
- Passed: local Markdown links/anchors and `git diff --check`.
- No required Windows CI-equivalent check was skipped. The broader E2E suite
  was not run; the chart layout harness and timer cases provide proportional
  browser coverage for this presentation change.
- Hosted CI is a post-push gate; its final run/result is reported in the handoff.

## Hosted CI repair

The first implementation run passed UI and Windows engine jobs, but Linux's
race-enabled `TestForegroundRequestOvertakesQueuedScannerWork` timed out while
waiting for a background request after fake-clock advancement. Engine code and
workflow configuration were unchanged from the previously green plan commit.
The same failure reproduced locally twice in 30 race-enabled repetitions.

The test advanced its fake clock concurrently with the pacer's separate `Now`
and `After` calls. Advancing between those calls registers a timer relative to
the later fake time, beyond the test's final advance. Test clock advancement now
holds the pacer mutex, matching the timer-registration critical section. Its
foreground precedence and reserved-spacing assertions remain intact.

The repaired test passed 1,000 race-enabled repetitions, followed by the full
engine suite, race-enabled short suite, vet, and lint. Standards and spec reviews
found no remaining issues. This CI repair is a separate test-only commit;
the final hosted CI result is reported in the handoff.

All order commands used by validation are simulated. No live orders are involved.
