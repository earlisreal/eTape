# Restore Chart Order Gesture price preview

Type: task
Status: resolved

Holding Shift displayed an Order Preview Line without its price. Earl requested
the price preview on 2026-10-07, superseding the gesture-only omission in the
[earlier preview spec](../../chart-order-preview/spec.md).

## Resolution

The conditional gesture overlay now displays a read-only Preview Price Label on
the right axis, using its existing snapped trigger snapshot and side color.
It appears on stationary-pointer modifier activation, follows pointer movement,
and hides with the existing preview on release, cancellation, or submission.
The label uses four decimals below $1 and two otherwise, stays within the main
pane, and has no order action. Native horizontal feedback remains suppressed.

## Validation

- Regression reproduced before the fix: Shift preview expected `100.00`, received
  no label. The same focused check passed after the fix.
- Focused gesture tests: passed, 24 tests including stationary Shift activation,
  all four sides, Stop-Limit/LIT, tick rounding below/above $1, and release/cancel.
- `npm run lint`, `npm test`, and `npm run build`: passed. Build includes both
  TypeScript projects; all UI suites, including golden checks, passed.
- `npm run e2e:chart-layout`: passed, covering 17 simulated overlay states and
  five production Chart Panel states. Price text, side color, right-axis geometry,
  and alignment with the trigger line were verified in the rendered chart.
- `git diff --check`: passed. Generated contracts are unchanged; no credentials
  or runtime data were introduced.
- Local Go build/test/race/vet/lint/contract regeneration and the full engine-backed
  E2E suite were skipped for this isolated UI presentation change. `npm ci` was
  skipped because existing dependencies and their lockfile are unchanged. Hosted
  CI still validates the complete repository after main integration and push.
