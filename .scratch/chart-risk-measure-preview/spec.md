# Chart Risk Entry measure preview

Status: implemented — Earl confirmed the design and requested implementation on
2026-10-06. See [validation](validation.md).

## Goal

Show the distance from the selected BUY trigger to the candidate protective SELL
with a Measure-style arrow and visible shares/estimated dollar risk as the mouse
moves. Do not draw a rectangle, shaded region, or square outline.

## Confirmed decisions

- Retain both existing placement gestures: press–drag–release and
  click BUY–move–click SELL.
- Draw a centered vertical arrow from BUY toward protective SELL, matching
  Measure's arrow geometry.
- Show calculated whole shares and estimated total dollar risk, after Limit
  Cushions, tick rounding, and available cash/buying-power constraints. Example:
  `500 shares · Est. risk $100.00`.
- Retain Enter submission and the existing default-off auto-send setting.
- Put the readout beside the arrow, near its midpoint, clamped inside the main
  price pane.
- Retain the arrow/readout through completed-draft review and edits. Update on
  draft edits; clear on accepted submission or cancellation. Rejected/unknown
  submissions retain the draft and its existing visible status.
- Preserve Risk Entry's current navigation lock. Measure-like presentation does
  not change Risk Entry's input/navigation semantics.

## Confirmed implementation boundaries

- Apply this presentation to the initial risk draft and its edits. Existing
  submitted-order marker behavior remains outside this change.
- Before BUY is selected, keep the existing short instruction. Once BUY and a
  candidate SELL exist, update arrow/readout without waiting for the second
  click or release.
- Use a protective-SELL-colored arrow and ordinary theme text with a small halo.
  The arrow/readout have no filled box or border and never intercept input.
  Retain the two chart-local horizontal placement positions; price-scale changes
  reproject their trigger-price Y coordinates. This is a transient draft aid,
  rather than a persisted time-anchored Chart Drawing.
- Keep selected trigger lines, axis chips, side-colored native crosshair,
  keyboard endpoint adjustments, cancellation, blockers, immediate-trigger
  warnings, and unknown-outcome behavior.
- Do not present invalid or unavailable sizing as a valid zero-risk order. Keep
  the blocking reason visible. Use `— shares · Est. risk —` for invalid prices or
  unavailable/stale sizing inputs; zero affordable shares also show a blocking
  reason. Valid estimates may remain visible alongside unrelated trading gates,
  such as a disarmed engine, without implying submission is allowed.
- A provisional SELL outside the main price pane hides the moving arrow/readout
  until the pointer returns. Already selected BUY and completed-draft controls
  retain their current behavior. Hide the arrow/readout when its prices cannot
  be projected into the main pane; keep blockers and axis controls available.
- For a completed draft, displayed shares must match the quantity eligible for
  submission: the smaller of the captured preview cap and current calculated
  shares. Compute displayed risk from that same quantity. Endpoint edits refresh
  the cap through the existing flow; account changes cannot silently increase it.
- Reuse the current sizing helper and imperative animation-frame rendering.
  A small native SVG and text inside the existing risk overlay are sufficient;
  do not add a drawing type, persistence, dependency, or engine contract.
- Follow chart painting/resizing so the arrow uses the actual trigger-price
  coordinates and never the vertically spaced axis-chip positions.

## Comments

Earl accepted the recommendations for Q1–Q6 on 2026-10-06.
Earl confirmed the complete design and boundaries above in the final
shared-understanding check, choosing to keep this a planning-only draft.
Earl subsequently requested implementation on 2026-10-06.
