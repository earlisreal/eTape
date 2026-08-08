# Panels

Dockable chart, ladder, tape, scanner, watchlist, stock-info, locates,
account, order, and settings surfaces. The symbol-bearing Locates panel uses
only `exec.status`, follows the existing PanelFrame symbol and venue
selection, and explicitly requests Alpaca quotes before showing a confirmation
for the fee-bearing reservation. It never creates a short order or declares a
market-data demand. The Account panel shows custom NYSE close-to-close
Day/Realized P&L and a persisted flat Fills table for the selected venue. It
backfills `QueryCycleFills` and merges deduplicated live `exec.fills`. Panels
acquire/release topics and symbol demand; data stays in stores/controllers.
[TradingView integration](tv/README.md) backs chart surface. Test: `npm test -- panels`.
