# Stop-limit trigger-state support by venue

Researched 2026-09-20 for the chart working-order marker plan. The question is narrow: can a venue produce an authoritative, distinct signal for **the stop condition has fired and the stop-limit order is now an active limit order**? A fill, a local market-price crossing, or a generic “working” status is not enough.

## Conclusion

No venue currently has end-to-end trigger-state support through eTape's existing execution contract.

| Venue | Broker/platform capability | Current eTape capability | Safe for the proposed marker switch? |
| --- | --- | --- | --- |
| Sim | Yes. The simulator itself performs the trigger transition and changes `STOP_LIMIT` to `LIMIT`. | No live event or trigger field is published; eTape's order model cannot represent the transition as it happens. | **Yes after a small engine change** that emits and folds an authoritative trigger transition. |
| Alpaca | **Yes through Alpaca FIX**: a stop trigger is an Execution Report with `ExecType=Restated` and `ExecRestatementReason=100`. The ordinary Trading API `trade_updates` documentation does not define an equivalent trigger event; its `stopped` event means a trade is guaranteed, not “stop price triggered.” | No. eTape uses REST + `trade_updates`, ignores `stopped`, and has no FIX adapter or trigger event. | **Not with the current adapter.** Supporting it authoritatively would require FIX ingestion (and account entitlement confirmation) or a newly documented Trading API signal. |
| moomoo | Partial signal only. Official docs say an advanced-order trigger causes an order push, but the pushed `Order` schema has no trigger-state/reason field, and `OrderStatus` has no triggered value. | No. The adapter maps only generic statuses and suppresses a push when that mapped status is unchanged; it does not inspect a trigger transition. | **No documented distinct signal.** Do not infer from price or from the mere arrival of a push. |
| TradeZero | Stop-limit placement is supported, but the documented REST/WebSocket lifecycle exposes generic states only (`PendingNew`, `Accepted`/`New`, partial fill, fill, cancel, reject, etc.). No documented stop-trigger event/state exists. | No. The adapter normalizes only those generic states and prices. | **No documented distinct signal.** |

Practical scope for the first implementation: switch stop-limit markers from trigger price to limit price only for **sim**, after adding an explicit domain transition. Keep live Alpaca, moomoo, and TradeZero stop-limit markers at the trigger price until an authoritative adapter signal is available. Never infer trigger state from local quote/last-trade data.

## Shared eTape limitation

`exec.OrderStatus` contains submission, fill, cancel, reject, expiry, block, and replace states, while `exec.Order` contains type/limit/stop prices but no triggered flag or timestamp ([`engine/internal/exec/types.go`](../engine/internal/exec/types.go#L152), [`engine/internal/exec/types.go`](../engine/internal/exec/types.go#L193)). The state fold updates status and replacement prices only; it has no trigger transition ([`engine/internal/exec/state.go`](../engine/internal/exec/state.go#L80)). Therefore, even a capable adapter needs a new broker event/domain field before the UI can distinguish pre-trigger from post-trigger authoritatively.

## Sim

The simulator owns the trigger decision. `actOnMarkLocked` checks the stop condition and mutates a triggered `TypeStopLimit` order to `TypeLimit` before attempting a fill ([`engine/internal/broker/sim/sim.go`](../engine/internal/broker/sim/sim.go#L573)). This is authoritative because the same component controls the simulated order lifecycle.

However, that mutation emits no dedicated event. If no fill occurs, the execution state that drives the UI receives no notification. A later broker snapshot contains the mutated type ([`engine/internal/broker/sim/sim.go`](../engine/internal/broker/sim/sim.go#L836)), but open-order reconciliation currently runs on recovery/reconnect rather than on every simulated trigger ([`engine/internal/exec/core.go`](../engine/internal/exec/core.go#L276)).

**Result:** the venue can support the UX with a small explicit trigger event; it does not support it end to end today.

## Alpaca

Alpaca's official FIX specification provides an unambiguous trigger signal: the Execution Report uses `ExecType (150) = D` (“Restated, for stop price triggers”) and `ExecRestatementReason (378) = 100` (“Stop triggered, for stop and stop limit orders”) ([Alpaca FIX specification](https://docs.alpaca.markets/us/v1.1/docs/fix-messages)). This is the strongest live-venue capability found.

Alpaca's ordinary Trading API explains that a triggered stop-limit becomes a limit order ([Orders at Alpaca](https://docs.alpaca.markets/us/docs/orders-at-alpaca)), but its documented `trade_updates` event list does not expose the FIX restatement. The similarly named `stopped` event means the order has been stopped and a trade is guaranteed, which is a different execution state ([Alpaca WebSocket streaming](https://docs.alpaca.markets/us/docs/websocket-streaming)).

eTape's Alpaca adapter decodes REST/order objects and `trade_updates`; it has no FIX path. `normalizeUpdate` explicitly ignores `stopped` and other rare events ([`engine/internal/broker/alpaca/normalize.go`](../engine/internal/broker/alpaca/normalize.go#L100)). REST snapshots preserve the reported `stop_limit` type and map generic lifecycle status only ([`engine/internal/broker/alpaca/rest.go`](../engine/internal/broker/alpaca/rest.go#L825)).

**Result:** Alpaca supports authoritative triggers at the platform level through FIX, but not through eTape's current Alpaca transport/adapter. FIX availability may depend on Alpaca account arrangements; the public specification proves the signal format, not a particular account's entitlement.

## moomoo

Moomoo's first-party FAQ says an advanced order being triggered causes an Orders Push Callback ([Moomoo transaction FAQ, Q13](https://openapi.moomoo.com/moomoo-api-doc/en/qa/trade.html)). That is evidence that OpenD notifies clients when a server-side trigger occurs.

The public protocol does not make the cause distinct. `Trd_UpdateOrder` carries only the full `Order` snapshot ([official `Trd_UpdateOrder.proto`](https://github.com/FutunnOpen/py-futu-api/blob/master/futu/common/pb/Trd_UpdateOrder.proto)). The official `OrderStatus` enum has no triggered/elected state, and `Order` has `orderType`, generic `orderStatus`, `price`, and `auxPrice` but no trigger flag/reason ([official `Trd_Common.proto`](https://github.com/FutunnOpen/py-futu-api/blob/master/futu/common/pb/Trd_Common.proto); [Moomoo trading definitions](https://openapi.moomoo.com/moomoo-api-doc/en/trade/trade.html)). The docs do not guarantee that `orderType` changes from `StopLimit` to `Normal` on trigger, so treating such a change as contractual would be unsafe.

The eTape decoder maps only generic moomoo statuses and returns early when the mapped status did not change, so a trigger push that remains `Submitted` is discarded ([`engine/internal/broker/moomoo/normalize.go`](../engine/internal/broker/moomoo/normalize.go#L213)). Snapshots preserve the reported order type, `price` as limit price, and `auxPrice` as stop price, but derive no trigger state ([`engine/internal/broker/moomoo/trd.go`](../engine/internal/broker/moomoo/trd.go#L523)).

**Result:** moomoo documents a trigger-caused push but not a distinct, reliably decodable trigger state. A captured real/paper protocol fixture showing a guaranteed field transition, plus first-party confirmation that the transition is contractual, would be needed before enabling the marker switch.

## TradeZero

TradeZero's official equity API supports `StopLimit`, but its documented order lifecycle lists generic order states only; the Portfolio stream is described as order state changes such as accepted, filled, partially filled, canceled, and rejected ([TradeZero equity trading](https://developer.tradezero.com/docs/documentation/trading); [TradeZero Portfolio stream](https://developer.tradezero.com/docs/websocket_api/portfolio)). The stream fields include `orderType`, `limitPrice`, `priceStop`, and generic `status`, with no trigger flag or trigger-specific event. The docs do not promise that `orderType` changes after election.

eTape mirrors that schema. `statusDomain` maps the documented generic lifecycle values, and `normalizeOrder` emits acceptance/fill/cancel/reject/expiry events only ([`engine/internal/broker/tradezero/normalize.go`](../engine/internal/broker/tradezero/normalize.go#L16)).

**Result:** no documented distinct trigger signal is available through the TradeZero API or current adapter. Treating `last trade >= stop`/`<= stop` as triggered would be non-authoritative because the broker's election source, timing, session, and routing rules may differ.

## Open questions before extending live-venue support

1. Whether the deployed Alpaca account can consume Alpaca FIX execution reports. If yes, the documented `150=D` + `378=100` pair is sufficient.
2. Whether moomoo guarantees a specific `Order` field transition on stop-limit trigger for the exact US-stock security firm/account in use. Current public docs guarantee a push, not how to identify its cause.
3. Whether TradeZero can provide an undocumented but supported trigger/election message. Nothing in the public REST/Portfolio schema currently establishes one.
