# Chart Drawings

Drawing models, interaction, persistence, and chart primitives. Inputs: pointer/tool events and chart coordinates; outputs: persisted drawing state plus imperative rendering. Test: `npm test -- drawings`.

## Interaction

Persisted Chart Drawings use a Drawing Anchor made from a rounded chart slot and
price. Loaded slots use their actual bar timestamp; blank slots before the oldest
bar and in the Future Buffer extrapolate from the nearest loaded bar by the
nominal timeframe. Placement, handle movement, and body movement use the same
conversion, so blank-space anchors remain attached to their chart positions as
the viewport changes.

Measure is a session-only drawing layer: it is not written to DrawingStore or
restored after a reload, symbol switch, or timeframe switch. Drag from the first
press to release, or click once and move the cursor to preview a measurement;
click again without moving to finish. Completion selects the Measure, opens the
floating style toolbar, and returns the tool to Select; reselect Measure to
create another. A direction arrow points from the first anchor to the second.
Pending Measure and Trend Line previews keep chart
pan/zoom available. A moved second press stays chart navigation and leaves the
pending preview. Escape, tool changes, symbol/timeframe changes, and right-click
cancel a pending first point. Select mode exposes the normal drawing handles,
movement, floating style toolbar, clone, and delete actions. Hide all and clear
all drawings include session Measures.

Drawing Tool Styles are workspace-wide per-kind color, width, and line-style
defaults and synchronize across open workspace windows. The rail gates persisted-style tools until the asynchronous config
hydration succeeds or fails; Measure remains available during that wait. A
completed persisted drawing is selected immediately and opens the floating style
toolbar; editing it also updates that tool's next default. Rectangles additionally
persist an independent fill toggle, color, and 0–100 opacity; fills render beneath
the outline and use the same style during placement preview.
