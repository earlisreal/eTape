# Chart Drawings

Drawing models, interaction, persistence, and chart primitives. Inputs: pointer/tool events and chart coordinates; outputs: persisted drawing state plus imperative rendering. Test: `npm test -- drawings`.

## Interaction

Persisted Chart Drawings use a Drawing Anchor made from a rounded chart slot and
price. Loaded slots use their actual bar timestamp; slots in the Future Buffer
extrapolate from the newest bar by the nominal timeframe. Placement, handle
movement, and body movement use the same conversion, so a future anchor remains
attached to that future chart position as incoming bars consume the buffer.

Measure is a session-only drawing layer: it is not written to DrawingStore or
restored after a reload, symbol switch, or timeframe switch. Drag from the first
press to release, or click once for a pending first point and click again to
finish. The Measure tool stays armed so multiple Measures can be created; a
direction arrow points from the first anchor to the second. A moved second press
stays chart navigation and leaves the pending preview. Escape, tool changes,
symbol/timeframe changes, and right-click cancel a pending first point. Select
mode exposes the normal drawing handles, movement, floating style toolbar,
clone, and delete actions. Hide all and clear all drawings include session
Measures.

Drawing Tool Styles are workspace-wide per-kind color, width, and line-style
defaults. The rail gates persisted-style tools until the asynchronous config
hydration succeeds or fails; Measure remains available during that wait. A
completed persisted drawing is selected immediately and opens the floating style
toolbar; editing it also updates that tool's next default.
