# Chart Drawings

Drawing models, interaction, persistence, and chart primitives. Inputs: pointer/tool events and chart coordinates; outputs: persisted drawing state plus imperative rendering. Test: `npm test -- drawings`.

## Interaction

Persisted Chart Drawings use a Drawing Anchor made from a rounded chart slot and
price. Loaded slots use their actual bar timestamp; slots in the Future Buffer
extrapolate from the newest bar by the nominal timeframe. Placement, handle
movement, and body movement use the same conversion, so a future anchor remains
attached to that future chart position as incoming bars consume the buffer.

Measure is transient and never enters DrawingStore. Drag from the first press to
release for the original drag-to-measure gesture, or click once for a pending
first point and click again to finish. A moved second press is treated as chart
navigation; the first point and live preview remain. Escape, a tool or symbol
change, and right-click cancel a pending first point. A finished Measure remains
visible until the next Measure attempt or Escape.

Drawing Tool Styles are workspace-wide per-kind color, width, and line-style
defaults. The rail gates persisted-style tools until the asynchronous config
hydration succeeds or fails; Measure remains available during that wait. A
completed persisted drawing is selected immediately and opens the floating style
toolbar; editing it also updates that tool's next default.
