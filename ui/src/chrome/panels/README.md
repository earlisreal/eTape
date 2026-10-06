# Panels

Dockable chart, ladder, tape, scanner, watchlist, stock-info, locates,
account, order, and settings surfaces. The symbol-bearing Locates panel uses
only `exec.status`, follows the existing PanelFrame symbol and venue
selection, and explicitly requests Alpaca quotes before showing a confirmation
for the fee-bearing reservation. It never creates a short order or declares a
market-data demand. Ambiguous request failures retain the idempotency key for
safe retry; definitive broker rejections start a new request. The Account panel shows custom NYSE close-to-close
Day/Realized P&L and a persisted flat Fills table for the selected venue. It
backfills `QueryCycleFills` and merges deduplicated live `exec.fills`. Panels
acquire/release topics and symbol demand; data stays in stores/controllers.
The Account panel shows live selected-venue Cash between Equity and Buying
Power; only Equity and Buying Power use the flat-position hold behavior.
[Account tables](./AccountPanel.tsx) persist independent column widths per
table; drag a header separator to resize and double-click it to auto-fit. The
widths are shared when switching the selected venue, then scale proportionally
to the panel width with per-column minimums before horizontal scrolling is
needed.
Open Orders and Closed Orders use compact uppercase headers: TIME, SYM, SIDE,
QTY, PRICE, STOP, TYPE, and STATE. Closed Orders also shows FILLED, AVG FILL,
and REASON. Header tooltips and accessible names provide the full meanings.
PRICE holds the limit price for LIMIT, STOP_LIMIT, and LIT; STOP holds the
trigger for STOP, STOP_LIMIT, and LIT. Unused price fields show `—`.
TYPE uses MKT/LMT/STP/STPLMT/LIT. Saved Stop Limit sorting maps to PRICE with its
direction preserved; existing widths for surviving columns remain in use.
Open QTY remains the remaining share count when available; Closed QTY is the
original quantity alongside FILLED and AVG FILL. TIME shows submitted time
for Open Orders and closed time for Closed Orders. Both show
`HH:MM:SS` for the current US Eastern calendar date and `MM/DD HH:MM:SS`
otherwise, with the full timestamp on hover. The date display refreshes at
minute boundaries; timestamp columns widen when dates are needed, including
across midnight, while narrow panels scroll horizontally.
[TradingView integration](tv/README.md) backs chart surface. Test: `npm test -- panels`.

The Order Ticket embeds the Hotkey Deck beneath its manual action row. It
resolves the saved Deck Layout by Action Template id, preserves row and
within-row order, renders each row as a non-wrapping horizontal scroller, and
omits stale or empty placements. Bound hotkeys appear as Keycap badges only
when Hotkey Label Visibility is enabled. Deck Buttons remain references to
the shared Action Template execution path, not a separate action surface.

Grouped charts overlay working LIMITs and pretrigger STOP_LIMITs/LITs, filtered
to the chart's exact symbol and venue. Compact price-axis chips use chart green
for BUY/COVER and red for SELL/SHORT, exposing shares and order details on hover.
Risk amounts and routine left-edge labels are hidden; actionable warnings remain
visible. The chip's × cancels its existing target. Dragging sends the modification
on release; arrow keys send one price step. Escape/right-click cancels the drag.
Engine-held stop-limit markers switch to
the child LIMIT after trigger; LIT uses the same phase-dependent price. Paused
parents stay visible and require explicit Resume. The Order Ticket shows local
custody and the session deadline before submit, including the no-broker-order
disclosure for live local
custody. The order overlay and clipped live announcement stay absolutely
positioned in every state so they do not move the native chart origin or
invalidate host-relative price coordinates.
