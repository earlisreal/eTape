# Chart Risk Entry measure preview

Status: implemented — 2026-10-06.

Design record: [approved spec](../../.scratch/chart-risk-measure-preview/spec.md).
Validation: [checks](../../.scratch/chart-risk-measure-preview/validation.md).

## Goal and non-goals

Add a centered BUY-to-protective-SELL arrow and live shares/estimated dollar risk
to the existing Chart Risk Entry preview, without a highlighted rectangle.

Keep sizing, venue routing, custody, submission, and order lifecycle in their
existing owners. Do not extend this into a new Chart Drawing, submitted-order
visualization, or setting. No new dependencies or live-order tests.

## Current-code evidence

- [ChartRiskEntry](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) already stores
  a moving SELL candidate, coalesces pointer updates with animation frames, and
  supports both placement gestures. Its presentation hides provisional shares
  and dollar risk; draft state retains prices rather than horizontal anchors.
- [riskEntrySize](../../ui/src/chrome/exec/riskEntry.ts) already returns limit
  prices, budget, funding-constrained whole shares, and estimated dollar risk.
  Submission additionally caps quantity at the last completed/edited preview.
- [Measure rendering](../../ui/src/render/chart/drawings/primitive.ts) draws its
  vertical arrow at the midpoint of the two anchor X coordinates. Its private
  renderer also draws a rectangle; using it wholesale would add unwanted visuals.
- [ChartPanel](../../ui/src/chrome/panels/ChartPanel.tsx) already invokes a layout
  callback after chart painting for working order markers. Risk Entry currently
  repaints on pointer/execution updates, host resize, and a one-second timer.
- [component tests](../../ui/src/chrome/panels/tv/ChartRiskEntry.test.tsx) and the
  [chart layout harness](../../ui/e2e/chart-order-layout.mjs) intentionally assert
  hidden risk figures. These expectations need revision for the requested UI.

## Design decisions

Q1–Q6 are confirmed in the draft spec: retain both gestures, use a centered
vertical arrow without a rectangle, show calculated shares/estimated dollar
risk beside its midpoint, retain feedback through draft edits, and keep the
current navigation lock. Earl confirmed the complete design and implementation
boundaries in the final shared-understanding check and subsequently requested
implementation. Commit this plan with the resulting changes.

## File-level steps

1. In `ChartRiskEntry.tsx`, retain the horizontal placement coordinates needed for
   the centered arrow. Add native SVG/text elements to the existing transparent,
   pointer-pass-through overlay. Update them from the current imperative paint
   path as SELL moves and draft endpoints change. Use actual snapped trigger
   coordinates, a protective-SELL-colored arrow, theme text/halo, pane bounds,
   and submission-consistent quantity/risk. Keep the arrow/readout through draft
   review and edits; invalid/unavailable sizing uses a placeholder with the
   existing blocking reason. Hide the moving preview outside the price pane.
2. In `ChartPanel.tsx` and `ChartRiskEntry.tsx`, reuse the existing post-paint
   layout-ref pattern: create/pass a dedicated risk layout ref, bind it to the
   effect-local paint function with identity-guarded cleanup, and invoke it beside
   the order marker layout callback after controller synchronization. This keeps
   price-scale/viewport changes aligned without new facade APIs or navigation
   behavior. Resize and pointer updates retain the existing scheduled paint path.
3. Extend existing component tests and `chart-order-layout.mjs`; revise the
   old hidden-risk assertions rather than creating a parallel test harness.
   Use queued animation-frame callbacks with explicit flushing for repeated
   pointer moves; the existing synchronous stub leaves the scheduler marked as
   pending after its first move. Add the layout ref to the mount fixture and
   exercise changed price projection without another pointer event.
4. Update `ui/README.md` and `ui/src/chrome/panels/tv/README.md` to describe the
   visible draft feedback. Record the intentional reversal of hidden draft risk
   in the new spec; retain earlier specs as project history.

## Validation

- Moving candidate: after BUY selection, mouse movement changes visible shares
  and risk before completion; cover drag placement and click placement.
- Math/display: exercise existing Limit Cushions, tick rounding, funding caps,
  endpoint edits, and completed-draft quantity caps after account changes.
  Invalid price relationships, unavailable account data, and zero shares stay
  blocked without misleading risk feedback.
- Lifecycle: preserve Enter, initial auto-send, no-send endpoint edits,
  cancellation, and non-retryable unknown outcomes.
- Layout: check arrow direction/alignment, absence of rectangle fill/outline,
  readable label placement, viewport repaint, light/dark themes, narrow charts,
  lower indicator panes, pointer leave/re-entry, and invalid/unavailable sizing
  using simulated data.
- During implementation, run the focused component check from `ui`:
  `npx vitest run --project chart-panel src/chrome/panels/tv/ChartRiskEntry.test.tsx`.
  Run the existing chart layout harness before the complete validation suite.
- After implementation, run the full
  [Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows)
  and `npm run e2e:chart-layout`. List any skipped required check with its reason.
  Complete scoped commit, upstream integration, main merge/push, and hosted CI
  verification under the standing repository instructions.

## Rollout, rollback, and risks

Use the existing Risk Entry hotkey and configuration; no migration is needed.
Rollback is a revert of the focused UI/documentation commit. Keep engine-owned
pairs and existing orders independent of this presentation.

The main risks are displaying a larger quantity than submission permits, stale
price-scale alignment, label clipping, and accidental input interception. Use
the same sizing/cap path, post-paint layout, bounded positioning, and existing
input ownership to address them. Horizontal positions remain chart-local during
the existing locked-navigation draft; persistent drawing/time anchors are not
needed for this placement aid.
