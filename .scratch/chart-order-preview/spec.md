# Chart order preview lines and price labels

Status: approved — Earl confirmed the complete design on 2026-10-07. Implementation pending.

## Vocabulary

Use the definitions in [CONTEXT.md](../../CONTEXT.md#order-entry):
Native Crosshair Price Label, Order Preview Line, Preview Price Label, and
Order Price Pill. A selected risk draft endpoint is placed but not submitted.

## Accepted behavior

- During selection, hide the native horizontal crosshair and Native Crosshair
  Price Label. Keep the vertical crosshair and time label in their normal style.
  Selection colors remain local to the chart receiving input.
- Chart Order Gestures for configured Stop-Limit/LIT templates show an Order
  Preview Line at the snapped prospective trigger price: green for BUY/COVER,
  red for SELL/SHORT. Show neither a Preview Price Label nor an Order Price Pill
  before placement. After a successful click, restore the normal crosshair and
  use the existing submitted-order pill. Keep one order per modifier press and
  existing validation, warnings, and blocked/unknown-outcome handling.
- Chart Risk Entry starts with a green Order Preview Line and Preview Price
  Label for BUY. After BUY placement, retain its selected line and Order Price
  Pill while a red SELL Order Preview Line and Preview Price Label follow the
  proposed SELL trigger price. Keep the arrow, shares, and estimated risk.
- Support both click BUY–move–click SELL and press–drag–release. Completing the
  pair restores the normal crosshair and shows both selected Order Price Pills.
  Enter and the existing default-off initial auto-send behavior retain their
  current submission rules.
- Editing a selected risk endpoint retains its existing Order Price Pill and
  moves the corresponding line and Preview Price Label with the proposed
  trigger price. Restore the normal crosshair when the edit finishes.
- Preview Price Labels have no × action. Selected risk draft pills retain their
  existing × action: discard the whole setup. Existing order pills retain their
  existing order/protection cancellation actions.
- Hide temporary previews outside the main price pane. Restore normal
  crosshair behavior on modifier release or cancellation; preserve existing
  Escape, right-click, focus-loss, and context-change cancellation rules.
  Selected draft prices retain their existing behavior when the pointer leaves.
- Reuse order tick rounding, sizing, routing, and input ownership. Ordinary
  submitted-order editing, Crosshair Sync behavior, and engine contracts retain
  their existing behavior.

## Current-code evidence

The initial pressed Risk Entry drag consumes `pointerdown` and updates from
captured `pointermove`, while the native order crosshair follows compatibility
`mousemove`. During that drag the native red line/label can remain at BUY.
The provisional SELL overlay is also intentionally hidden until completion.
Read-only simulated browser reproduction showed SELL sizing update at the new
pointer position while the native crosshair stayed at the initial BUY position.

Owning files: `ui/src/chrome/panels/tv/ChartRiskEntry.tsx`,
`ui/src/chrome/panels/tv/ChartConditionalOrderEntry.tsx`, and
`ui/src/render/chart/orderCrosshair.ts`. Use their existing imperative paint and
candidate-price paths; keep pointer updates outside React state.

This proposed presentation replaces the native selection-feedback decision in
[the earlier implemented spec](../chart-order-cursor/spec.md); that file remains
historical. The glossary records concepts, not implementation status.

## Checks for implementation

Extend existing component/browser checks for initial pressed-drag line/price
movement, click placement, endpoint editing, stationary-pointer modifier
activation, snapped prices above/below $1, and horizontal-only crosshair
suppression/restoration. Check price-pane boundaries, cancellation, blocked
submission, themes, and retained selected pills/arrow/readout. Run the repository's
required implementation validation before commit, main integration, push, and
hosted CI verification.
