# Alpaca normalization fixtures

All six fixtures here are **authored** from the documented `trade_updates` shape
(`docs/2026-07-03-alpaca-api.md`: event, execution_id, price, qty, timestamp,
position_qty, plus the full order object with filled_qty/filled_avg_price),
not captured from a real paper stream — no eTape session has driven an Alpaca
paper order through a full fill yet (Task 13/14 add the REST + WS clients that
would produce one).

Replace/augment with real captured frames once the Alpaca paper WS client is
running an order end-to-end. Keep the `tradeUpdate`/`auOrder` decoders tolerant
of unknown fields (plain `encoding/json`, ignores anything not in the struct)
so a real capture's extra fields don't break decoding.

- `fill.json` / `partial_fill.json`: two executions of the same working order
  (`ET01J0000000000000000000BB`, AAPL, 100-share buy limit) — 40 then 20 more
  shares, distinct `execution_id`s (`e-1`, `e-2`), `position_qty` reflecting
  the position after each execution (40, then 60).
- `new.json`, `canceled.json`, `replaced.json`, `rejected.json`: one order
  lifecycle event each, on their own distinct orders/symbols since these event
  types carry no per-execution price/qty/execution_id (zeroed here).
