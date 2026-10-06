# Chart Order Selection Crosshair

Status: implemented — Earl approved the complete spec on 2026-10-06.

## Goal

Use the existing chart crosshair and price-axis label to identify the action whose trigger price the trader is selecting, without a duplicate moving horizontal preview line or floating B/S price chip.

## Settled decisions

- Color the chart crosshair and price-axis label, rather than the native mouse pointer.
- Reuse the chart's green BUY color and red protective SELL color. This distinction identifies order side, not order type: both Chart Risk Entry endpoints are STOP_LIMIT orders.
- Replace both the moving preview line and floating B/S price chip. Retain already selected draft prices and existing order markers.
- For a Chart Order Gesture, activate feedback immediately when its exact bound modifier is held over the main price pane. Restore the normal crosshair after the gesture's click, modifier release, cancellation, or leaving that pane.
- For Chart Risk Entry, use green while selecting BUY, then red while selecting SELL, including the SELL leg of press-and-drag. Restore the normal crosshair once both points are fixed. Color the corresponding endpoint again while it is being dragged.
- Retain the action color when validation blocks submission, with a visible blocking reason. Color identifies the selected action; it does not authorize an order.
- Snap selection feedback to the actual order tick (0.01 at prices >= 1, 0.0001 below 1), so the crosshair and its price label agree with the price that will be submitted.
- Expose order details through hover tooltips, rather than adding compact bottom order-summary text. Earl can inspect order details in the Account Panel. Existing blocking messages and warnings remain visible.
- Make both selected Chart Risk Entry draft lines and editable existing order lines draggable across the main price plot. Retain price-chip dragging and the current eligibility rules.
- When nearby lines can be hit, drag the nearest line. Open the order chooser only for exact ties in the nearest-line hit distance, including coincident orders of different sides or types.
- A bound Chart Order Gesture modifier takes priority over grabbing an existing order line. Drag existing order lines with the gesture modifier released.
- Allow dragging the first selected BUY line before SELL is fixed. Editing that BUY does not complete the pair; the trader still selects SELL separately.

## Interaction details

- Use the existing eight-pixel draft-line hit tolerance for line grabbing in the main price plot, with an up/down resize pointer over the selected editable line. Hover feedback and dragging identify the same nearest line.
- Use the chosen order side's crosshair color during line dragging. Restore normal styling on release or cancellation. Price chips keep their existing accessible keyboard controls.
- Existing order drags preview the proposed price and send one existing replacement command on release. Escape, right-click, window blur, pointer cancellation, and context changes cancel the proposal. Pending, unknown, paused, or otherwise non-editable order states retain their current restrictions.
- Risk draft drags edit draft prices and recalculate sizing under the existing submission rules. Selecting the initial SELL completes the pair; editing the already selected BUY first does not.
- Active Chart Risk Entry owns its draft interaction; ordinary modifier gestures cannot submit a second action during it. Active drawing tools and an open order chooser suppress new-order selection feedback.
- Keep action feedback local to the chart receiving the input. Crosshair Sync continues to share time/price, with normal styling on receiving charts. Theme changes restore the correct normal colors when selection ends.
- Hover tooltips retain available quantity, order type, trigger/limit, venue/session/custody details. Blocking reasons, acknowledgement instructions, immediate-trigger warnings, and uncertain outcomes remain visible rather than requiring hover.

## Minimal implementation

1. Extend the existing chart facade's native crosshair handling for temporary selection colors, exact order-price feedback, and restoration; preserve ordinary pointer movement and Crosshair Sync.
2. Replace gesture hover lines/chips with that feedback and tooltips. Activate from keyboard modifier changes as well as pointer movement; preserve exact bindings and one-order-per-press rearming.
3. Use the same feedback for risk selection. Render only selected endpoints, permit editing the first BUY, and retain selected endpoint chips.
4. Route full-line order grabs through the existing order drag and replacement handlers, using host hit testing so transparent lines do not intercept the chart canvas. Add nearest-line/tie selection and pointer ownership without a second modification path.
5. Update the chart guide and extend existing component/browser checks for the new interactions.

## Code evidence gathered before implementation

- `ui/src/chrome/panels/tv/ChartConditionalOrderEntry.tsx` owns the gesture preview line, chip, route preview, submission, and modifier consumption. Its modifier activation currently depends on pointer movement.
- `ui/src/chrome/panels/tv/ChartRiskEntry.tsx` owns the two-point draft, endpoint lines/chips, sizing, and submission. The SELL hover after the first click is a candidate until completion.
- Completed risk drafts already support line dragging within eight pixels. `ChartOrderMarkers.tsx` currently starts working-order drags only from price-chip and chooser buttons; its existing preview and release handlers can serve line dragging too. Its chooser currently covers exact same-side/type/price groups only.
- `ui/src/chrome/panels/ChartPanel.tsx` adapts the native chart crosshair through `ChartApiFacade`, and owns Crosshair Sync integration.
- `ui/src/render/chart/chartTheme.ts` configures a free grey crosshair. `tvTheme.ts` supplies the existing green/red colors.
- `ui/src/render/chart/orderMarkers.ts` supplies `snapOrderMarkerPrice`; normal chart quote precision can be finer than the order tick.

## Validation intent

Verify gesture activation without pointer movement, exact displayed/submitted prices above and below one dollar, buy/sell phases, first-BUY editing before SELL selection, endpoint dragging, reset paths, blocked-state messages, and one-order-per-modifier-press behavior in the existing component checks. Verify line and chip dragging use the same command path, the nearest line is selected, exact ties use the chooser, active modifiers take priority, and cancellation sends no replacement. Verify visible crosshair/axis colors, hover tooltips, and absence of moving duplicate previews in the existing simulated chart browser harness, in light/dark charts and with lower indicator panes.

Preserve existing submission, sizing, acknowledgements, route checks, and uncertain-outcome behavior. Run the required CI-equivalent Windows validation from `README.md` for the implemented change, then complete the repository's scoped commit, main integration, push, and hosted CI verification. Exercise order interactions only against simulated test data.

Validation results are recorded in `validation.md`. This approved spec is committed with its implementation.
