# Chart order preview lines and price labels

Status: implemented on 2026-10-07.

Design: [approved spec](../../.scratch/chart-order-preview/spec.md).
Results: [validation](../../.scratch/chart-order-preview/validation.md).

## Goal and scope

Render side-colored Order Preview Lines from actual candidate prices. Chart Risk
Entry also gets a plain Preview Price Label. Hide the native horizontal
crosshair/price label during selection and retain normal vertical/time feedback.
Keep selected pills, existing sizing/submission/cancellation rules, and working
order editing in their current owners. No engine contract or dependency changes.

## Evidence and decisions

Risk Entry already receives captured pointer moves, holds the proposed SELL,
and paints selected lines/pills imperatively. Native order crosshair movement
only receives compatibility mouse events, suppressed during its initial drag.
Conditional entry already holds the snapped trigger/projection but removed its
moving overlay. Crosshair Sync also owns horizontal visibility, so suppression
must compose with that visibility request in the shared crosshair helper.
Q1–Q3 and the full design are confirmed in the spec; Earl requested implementation.

## Steps

1. Reuse risk endpoint lines for initial candidates and retain selected-pill
   filtering separately. Add one read-only axis label for the actively selected
   price; paint it from the same proposed price, including endpoint edits.
2. Restore a pointer-pass-through conditional preview line without a price label
   or pill. Use its existing imperative snapshots and snapped trigger projection.
3. Extend the existing crosshair setter to accept current pointer events. Replay
   those into the native chart during captured risk drags. Keep native horizontal
   visibility suppressed for risk/gesture owners, compose Crosshair Sync requests,
   and preserve working-order drag behavior and normal theme/format restoration.
4. Update existing helper/component/browser checks, relevant UI/chart READMEs,
   and the spec's implementation status. Commit this executed plan with the code.

## Validation

Exercise both risk placement gestures, initial drag movement, plain label versus
placed pill, selected endpoint edits, keyboard adjustments, stationary-pointer
modifier activation, price snapping, pane boundaries, theme changes, cancellation,
and successful/blocked submission. Use the existing simulated chart browser
harness to check native vertical movement and horizontal visibility/restoration.
Run focused checks first, then the complete Windows CI-equivalent checklist and
`npm run e2e:chart-layout`; finish with scoped commit, main integration/push,
containment verification, and a passing hosted CI run.

## Rollback and risks

Revert the focused implementation commit to restore the earlier presentation.
The main risks are stale native pointer feedback, visibility conflicts with
Crosshair Sync, and label clipping/overlap. Use current pointer events, one
shared visibility owner, actual trigger coordinates, and bounded price-axis
labels; retain the existing selected controls and input ownership.
