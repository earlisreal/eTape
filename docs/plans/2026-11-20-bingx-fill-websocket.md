# BingX private order updates

Record fills promptly for BingX Live and Virtual using private order updates
and the existing durable order journal. Keep account snapshots independent
for equity, buying power, and broker-confirmed positions. Order submission,
cancel, and the UI protocol keep their existing contracts.

Both BingX environments delivered private fill events during exchange
verification. Live returned two notifications for each tested execution with
different trade-ID values. REST returned one fill for each execution. Use the
private events for order-level evidence and REST fill history for the
individual execution records. Keep Orders tied to the persisted fills.
[BingX order updates](https://bingx-api.github.io/docs-v3/#/en/Swap/Websocket%20Account%20Data/Order%20update%20push)

1. Connect each [BingX execution venue](../../engine/internal/broker/bingx/stream.go)
   with its own listen key. Renew the key, handle account heartbeats, and
   reconnect with backoff.
2. [Reconcile](../../engine/internal/broker/perps/stream.go) updates for a
   journaled order as soon as they arrive. Coalesce equivalent cumulative
   updates. Read order-filtered fill history directly when the event supplies
   the expected identity and quantity. Use signed order details when the
   journal or stream lacks evidence, including when position ownership is
   required. Retry delayed fill history within the venue's request budget.
3. Recover pending orders at startup and reconnect and check discrepancies
   from the regular account snapshot. Use the three-second order poll when
   the stream is unavailable. Keep the journal readable across an upgrade.
   [REST contracts and pacing](../external-apis.md)

Controlled tests cover both venue streams, duplicate notifications, delayed
fill history, a disappeared working order, and fill processing during an
in-flight account snapshot. For an app check, trade a minimum-sized Virtual
order and observe Orders and fill markers while account data continues to
refresh. A disabled or interrupted stream falls back to polling. Reverting
the change restores polling-only reconciliation using the same local journal.
