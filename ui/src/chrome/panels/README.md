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
[TradingView integration](tv/README.md) backs chart surface. Test: `npm test -- panels`.

The Order Ticket embeds the Hotkey Deck beneath its manual action row. It
resolves the saved Deck Layout by Action Template id, preserves row and
within-row order, renders each row as a non-wrapping horizontal scroller, and
omits stale or empty placements. Bound hotkeys appear as Keycap badges only
when Hotkey Label Visibility is enabled. Deck Buttons remain references to
the shared Action Template execution path, not a separate action surface.
