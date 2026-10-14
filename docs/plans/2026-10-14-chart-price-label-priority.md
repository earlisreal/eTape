# Chart price-label priority

Status: implemented on 2026-10-15.

Results: [validation](../../.scratch/chart-price-label-priority/validation.md).

## Goal

Keep the Bar Close Timer and the price label following the cursor readable when
they overlap a resting Order Price Pill. Bring the pill for a dragged line to
the front so its proposed order price remains readable.

Use the definitions in [CONTEXT.md](../../CONTEXT.md): Order Price Pill,
Native Crosshair Price Label, Preview Price Label, and Bar Close Timer.

## Decisions

- Apply to every Order Price Pill, including existing orders and selected
  Chart Risk Entry draft endpoints, on both buy and sell sides.
- Resting pills sit behind the Bar Close Timer, Native Crosshair Price Label,
  and colored Preview Price Label.
- During a line drag, the active pill takes priority over those labels; other
  pills retain their resting priority.
- Hovering or keyboard-focusing a pill also brings it forward so its price
  and × remain accessible when another label covers its resting position.
- Release ends drag priority immediately. A pending replacement does not
  itself keep the pill promoted; pending/rejected/unknown feedback still shows.
- Keep the colored preview at its actual prospective price, allowing it to
  cover resting pills. Retain spacing between resting pills.

## Interaction priority

Choose one foreground pill per chart: dragged pill first, otherwise hovered
pill, otherwise keyboard-focused pill. This includes the pill's price and ×.
On release, apply the hover/focus rule immediately: the pill stays in front if
it is still hovered or keyboard-focused, and returns behind when neither applies.
No waiting for order acknowledgement. Selecting a grouped order retains the
existing chooser behavior and promotes that group's pill for the active drag.

| Situation | Price-label priority, front to back |
| --- | --- |
| Ordinary cursor | Native Crosshair Price Label → Bar Close Timer → resting Order Price Pills |
| Chart order/risk price selection | Preview Price Label → Bar Close Timer → resting Order Price Pills |
| Drag, hover, or keyboard focus | The applicable Order Price Pill → cursor/preview label → Bar Close Timer → other resting pills |

Native horizontal feedback stays suppressed during colored order/risk previews
under the existing shared crosshair rules. Choosers and actionable warnings
remain readable. Hovering a line in the plot retains its current cursor/tooltip
behavior; it only promotes a pill when a drag starts. High/low, drawing and
indicator price labels retain their existing behavior.

## Current-code evidence

- [ChartOrderMarkers](../../ui/src/chrome/panels/tv/ChartOrderMarkers.tsx)
  puts working-order lines, pills, status and choosers inside a z-index 8 root.
  Its imperative drag reference and start/release/cancel paths own the proposed
  price. Release ends drag state before sending ReplaceOrder; keyboard edits
  directly request replacement.
- [ChartRiskEntry](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) places
  selected draft pills and its Preview Price Label in one z-index 10 root.
  Its collision layout includes both selected pills and the candidate preview,
  which can displace the preview from the actual price coordinate.
- [ChartConditionalOrderEntry](../../ui/src/chrome/panels/tv/ChartConditionalOrderEntry.tsx)
  paints its read-only preview in a z-index 9 overlay, at the actual candidate
  price with pane-edge bounds.
- [BarCloseTimer](../../ui/src/chrome/panels/tv/BarCloseTimer.tsx) is passive
  at z-index 1. [ChartPanel](../../ui/src/chrome/panels/ChartPanel.tsx) keeps
  it as the final React child so it paints after the native base canvas and
  beneath the native crosshair canvas at z-index 2.
- The native top axis canvas covers the whole axis and receives axis input.
  Lowering an entire order overlay beneath it makes DOM pill buttons unreachable
  even where its pixels are transparent. Raising an individual child inside an
  unchanged high-level stacking context cannot solve the resting overlap.

## Implementation outline

Use the current interactive pill buttons above the native canvas, and render
their visible graphics separately in a host-level layer. This lets the graphics
interleave with the native labels without sacrificing the existing input targets.

1. After ChartPanel initializes the native chart, position the pill owners'
   passive host visual slot after its native DOM and before Bar Close Timer.
   The slot itself must not create a stacking context. Handle assigned-symbol
   mounts, later chart creation, absent timer, rebuild and disposal explicitly.
2. In ChartOrderMarkers and ChartRiskEntry, keep real controls, keyboard/drag/
   cancel handlers, titles and accessible names in their existing input layers.
   Paint one synchronized, read-only visual for each whole pill into the host
   slot; keep those visuals hidden from accessibility, unfocusable, and
   pointer-pass-through. Reuse each pill's presentation rather than maintaining
   two independently styled implementations.
3. Put resting pill visuals at level 1 after the native base canvas but before
   the timer. Promote only the applicable visual above the current preview
   layers (currently 9 and 10), for example at level 11. Keep lines, choosers and
   warnings in their existing owners. Preserve native scale gestures outside
   pill controls and the existing crosshair/timer ordering.
4. Update both surfaces from existing imperative layout/proposal painting so
   text, snapped price, geometry, theme, groups and pending state agree. Use the
   existing start/release/cancel paths and hover/focus transitions to update
   priority. Clear promotion on all cancellations, context changes and disposal;
   every successful release returns to ordinary hover/focus selection.
5. In Risk Entry, remove the candidate preview from resting-pill spacing inputs
   and position it at its actual candidate coordinate, bounded at pane edges.
   Retain pill-to-pill spacing and current entry/stop validation. The conditional
   preview already follows its actual candidate coordinate.
6. Extend existing focused component and simulated-browser coverage; update the
   [chart renderer guide](../../ui/src/render/chart/README.md),
   [panel guide](../../ui/src/chrome/panels/README.md), and
   [E2E guide](../../ui/e2e/README.md) for the final behavior.

## Validation

Validate local Markdown links and run `git diff --check` alongside the
implementation checks below.

For implementation, reuse
[the chart layout harness](../../ui/e2e/chart-order-layout.mjs) and
[the timer regression](../../ui/e2e/bar-close-timer.spec.ts). Assert actual painted
overlap, not just computed stacking values. Cover timer/native crosshair/colored
preview over resting pills; only the active pill in front; proposed stop and
limit values; working orders and both draft endpoints; grouped/nearby pills;
covered controls; release, Escape, pointer cancellation, blur and context changes;
pending/rejected/unknown modification; light/dark and narrow/multiple-pane charts;
and unaffected native scale gestures outside pill controls.

Also cover drag taking precedence over a different hovered/focused pill, release
with a pill still hovered/focused versus neither, keyboard focus on a covered
pill, matching visual/control bounds and text, and absence of duplicate focus
stops or announcements. Replace the browser harness's current assertion that
Risk Entry previews never overlap selected pills with exact-price and painted
priority assertions; preserve its existing resting-pill spacing checks.

Run focused UI checks, `npm run e2e:chart-layout`, the timer browser regression,
and the complete [Windows CI-equivalent checklist](../../README.md#ci-equivalent-validation-on-windows)
after implementing the approved plan. Commit the executed plan with its changes,
integrate and push main, and verify commit containment and passing hosted CI per
[AGENTS.md](../../AGENTS.md#git).

## Boundaries, rollout and risks

This is a chart presentation change using existing price and input owners.
High-frequency pointer/price updates remain imperative. Order sizing, tick
snapping, submission, replacement, cancellation, routing and custody retain
their existing rules; no engine contract or dependency changes are planned.

Use simulated orders for validation. There is no persisted-data migration or
new setting. Reverting the focused implementation restores the prior display.
The main risks are unreachable or visually misleading controls, unintended
canvas input interception, displacement from the actual price, and promotion
left active after interaction ends. This reversible UI choice needs no ADR.
