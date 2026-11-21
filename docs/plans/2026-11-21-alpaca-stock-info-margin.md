# Alpaca margin in Stock Info

Show Alpaca's reported long and short percentages for the selected US stock in
expanded Stock Info. The reference works in pinned or linked panels and does
not estimate account buying power or change order handling.

- In the Alpaca adapter, seed the new percentages from the startup asset
  directory. Share a per-symbol refresh using the existing REST limiter after
  15 minutes. Retain and timestamp the last value on a failed refresh.
- In engine boot, prefer the first Alpaca Live adapter. Use the first Alpaca
  Paper adapter only in paper-only setups. Expose the value through a
  symbol-scoped UI hub query independent of Moomoo fundamentals.
- In Stock Info, show the source, report each missing side with a dash, and show
  unavailable if there are no percentages for a configured Alpaca source.
  Hide the row when Alpaca is unconfigured. Reject late results for old symbols.
- Validate with broker fixtures, race tests, generated contract checks, and a
  browser fixture that switches symbols while a query is outstanding.

The cache is in memory and requires no migration. Rollback removes the query
and display. Alpaca house requirements can change during the cache interval, so
Stock Info presents the reported percentages as a reference.
