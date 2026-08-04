# Panels

Dockable chart, ladder, tape, scanner, watchlist, stock-info, account, order, and settings surfaces. The Account panel shows custom NYSE close-to-close Day/Realized P&L and a persisted flat Fills table for the selected venue. It backfills `QueryCycleFills` and merges deduplicated live `exec.fills`. Panels acquire/release topics and symbol demand; data stays in stores/controllers. [TradingView integration](tv/README.md) backs chart surface. Test: `npm test -- panels`.
