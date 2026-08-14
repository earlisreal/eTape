# Drawing Tools: Future Buffer, Click Measure, and Saved Styles

## Goal

Make every persisted chart drawing usable in the **Future Buffer**, add a
click-to-click Measure flow without removing drag-to-measure, restore saved
per-tool styles before a trader can place a drawing after restart, and show the
style toolbar as soon as a drawing is completed.

## Non-goals

- Add drawing kinds, persist Measures, or change the drawing JSON schema.
- Change chart navigation, the four-bar Future Buffer policy, or historical
  left-edge placement.
- Add a session-aware/calendar-aware future-time model. Future positions use the
  existing nominal timeframe extrapolation and become real-bar positions as data
  arrives.
- Add an engine API, database migration, or a second style store.

## Current-code evidence

- `ui/src/render/chart/drawings/interaction.ts` rounds a pointer logical index
  then clamps it to the loaded-bar range in both `snap()` and body dragging.
  This makes the Future Buffer indistinguishable from the newest loaded bar.
- `ui/src/render/chart/drawings/geometry.ts` already extrapolates an anchor's
  `timeMs` after the newest loaded bar, and `primitive.ts` projects that result.
  The renderer can therefore draw future anchors once interaction creates them.
- `DrawingInteraction` currently begins a Measure on every pointer-down and
  clears its active gesture on pointer-up, so a second click restarts rather
  than finishes a Measure.
- `ui/src/render/chart/drawings/toolStyles.ts` already persists global,
  per-drawing-kind color, width, and line-style defaults under
  `drawings.toolStyles` through `GetConfig`/`SetConfig`. Its initial load is
  asynchronous, so an early placement can use palette fallback before saved
  styles arrive.
- `DrawingInteraction.commit()` stores a new drawing and returns to select mode
  without selecting it. `ChartPanel` renders `TVFloatingToolbar` only for the
  current selection, so the toolbar appears only after a later hit-test click.

## Settled behavior

- A Drawing Anchor placed in the Future Buffer is attached to that future chart
  position. Incoming bars consume the buffer and eventually align with it.
- All persisted drawing kinds support future placement, handle movement, and
  body movement. Measure uses the same coordinate conversion but remains
  transient.
- Measure supports both drag-and-release and click, preview, click. After its
  first click, pan and zoom remain usable; a subsequent moved press navigates
  the chart, while a stationary click finishes the Measure. Escape, tool change,
  and right-click cancel a pending first point; right-click still reaches the
  chart context menu.
- Finished Measures remain visible until the next Measure attempt or Escape and
  are never stored as drawings.
- Color, width, and line style are workspace-wide defaults per drawing kind,
  shared across symbols and panels and retained across restarts. Editing a
  selected drawing updates both it and that tool's next default.
- Style-bearing drawing tools wait for initial saved-style hydration. Measure
  remains available because it has no persisted style.
- Completing a persisted drawing selects it immediately and opens its floating
  style toolbar. A two-anchor tool does this only after its second anchor;
  Measure never opens it.

## File-level implementation plan

1. Update `ui/src/render/chart/drawings/interaction.ts`.

   - Add one shared interaction-side conversion from a rounded logical slot to
     an Anchor time: use an actual loaded timestamp through the newest bar, then
     extrapolate after it by `timeframeMs`. Keep the existing first-bar lower
     bound; remove only the upper bound that blocks the Future Buffer.
   - Route `snap()`, handle dragging, and body dragging through that conversion
     so every user path creates or moves the same future anchor. Keep the
     persisted `Anchor { timeMs, price }` shape unchanged.
   - Split Measure state into an initial press, a pending first point, and a
     possible second press. Track a small pointer-movement threshold so a
     dragged first press retains current drag-to-measure behavior, while a
     released click leaves a live pending preview.
   - Do not finalize a pending Measure until a second stationary pointer-up.
     Leave pan/zoom enabled during that pending state so a moved second press
     navigates instead of accidentally completing the Measure. Preserve the
     first point and refresh the preview after navigation.
   - Cancel pending Measure state on Escape, tool change, symbol change, or a
     right-click without suppressing the context menu. Preserve the existing
     transient-only result and no-store-write behavior.
   - Select the just-committed persisted drawing through the existing selection
     callback before requesting its repaint; do not select a Measure.

2. Harden `ui/src/render/chart/drawings/toolStyles.ts` without changing its
   engine config key or payload.

   - Expose a small ready/subscription surface for the one-time hydration.
     Notify consumers after accepted, missing, malformed, or failed loads so a
     failed read cannot leave drawing tools disabled forever.
   - Preserve local remembered edits if they occur while the asynchronous load
     is in flight rather than overwriting them with the returned config.
   - Continue using the existing debounced whole-map `SetConfig` write and
     per-kind field merge; no browser-local duplicate cache.

3. Wire readiness and automatic selection through
   `ui/src/chrome/panels/ChartPanel.tsx` and
   `ui/src/chrome/panels/tv/TVDrawingRail.tsx`.

   - Subscribe to style readiness with ordinary low-frequency React state.
     Pass the result to the rail and refuse/disable only persisted drawing-tool
     activation until styles are ready; keep the Measure control usable.
   - Keep the existing imperative drawing/paint path. React state is used only
     for configuration readiness and the already-existing selection toolbar,
     never for pointer-move or bar updates.
   - Ensure auto-selection uses the existing `onSelectionChange` path so the
     toolbar positions from the newly committed drawing without polling or a
     second selection model.

4. Expand focused UI tests.

   - In `ui/src/render/chart/drawings/interaction.test.ts`, cover a Future
     Buffer placement, handle drag, and body drag using timestamps beyond the
     latest loaded bar; assert that normal loaded-bar behavior remains intact.
   - Cover drag Measure, click-click Measure, live pending preview, pan/zoom
     availability after the first click, second-press navigation, and every
     cancellation route. Assert that Measures never enter `DrawingStore`.
   - In `toolStyles.test.ts`, add deferred hydration coverage, ready notification
     on success/failure, and protection of a local edit made before the read
     resolves.
   - In `ChartPanel.test.tsx` and `TVDrawingRail.test.tsx`, verify that saved
     styles gate only style-bearing tools and that completing a drawing selects
     it and renders the floating toolbar with its style controls.

5. Update documentation after behavior is implemented.

   - Extend `ui/src/render/chart/drawings/README.md` with Future Buffer anchors,
     the two Measure gestures, transient lifetime/cancellation, per-tool style
     hydration, and automatic selection.
   - Extend `ui/src/render/chart/README.md` to state that drawings consume the
     Future Buffer as future chart positions rather than clamping to the latest
     bar.
   - `CONTEXT.md` was updated during this design session with Chart Drawing,
     Drawing Anchor, Measure, and Drawing Tool Style terminology.

## Validation

Run the focused drawing tests first:

```bash
cd ui && npm test -- drawings
```

Then run the affected UI checks and the repository's CI-equivalent Windows
checklist from `README.md#ci-equivalent-validation-on-windows`, because the work
changes chart interaction and persisted UI configuration. Confirm manually that
the first post-restart drawing uses its saved per-tool style, a completed
two-point drawing opens its toolbar, and a Future Buffer anchor is reached by
incoming bars.

## Rollout and rollback

This is UI-only: the existing engine config key and drawing anchor schema stay
compatible. No migration or rollout flag is required. Reverting leaves any
future `timeMs` anchors safe to render under the existing extrapolation logic;
the older UI simply cannot create or move additional ones into the Future
Buffer.

## Risks

- The chart and pending Measure share pointer events. The moved-second-press
  test must prove that navigation wins over accidental completion.
- Nominal timeframe extrapolation around market closures is intentionally not a
  trading-calendar promise; it matches the renderer's current behavior.
- Config loading or command failure must release the tool gate and fall back to
  palette defaults rather than leave drawing controls unusable.
