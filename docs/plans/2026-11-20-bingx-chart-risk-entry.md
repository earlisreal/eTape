# BingX Chart Risk Entry

Approved for BingX Virtual and BingX Live. This plan records the original IOC
rollout. The current workflow uses cached opening terms and regular limits.
[Current execution guide](../../engine/internal/exec/README.md#bingx-chart-risk-entry)

## Goal

Use Chart Risk Entry on a one-way, isolated BingX USDT perpetual with either
BingX Virtual or BingX Live selected in its Link Group. Allow a protected
position to grow through linked chart adds and shrink through partial closes.
One eTape-held stop owns the entire BingX position until it goes flat. A
same-side fill placed directly on BingX becomes part of that position and is
included in the full close. Virtual collateral displays VST and Live displays
USDT. [Link Group decision](../adr/0005-link-group-owned-execution-venue.md) ·
[BingX position modes](https://bingx.com/en/support/articles/26343882391833-perpetual-futures-introducing-one-way-mode)

## Position-ID close verification

A BingX Virtual BTC-USDT test in one-way, isolated mode opened 0.0002 BTC with
an IOC, added 0.0002 BTC with a second IOC, and sold half through a sized
reduce-only market order. Both IOC order records and the signed position
snapshots reported the same position ID. A full market close by that ID filled
the remaining 0.0002 BTC, with an exchange order ID that could be queried. A
same-side reopening received a new position ID. BingX rejected the old ID with
code 109400 and the replacement position's size stayed unchanged. The test
contract was verified flat after cleanup.

Use the dedicated full-position endpoint for the protective stop. The earlier
Virtual test used the regular quantity-limited endpoint, which filled a
reduce-only order carrying the wrong position ID. The regular endpoint serves
manual partial closes on the current position.
[BingX close-by-position-ID API](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-trade/api-reference.md#15-close-position-by-positionid) ·
[Adapter notes](../external-apis.md#usdt-perpetual-exchanges)

## Entry and shared stop

- Start a new Position Cycle from a confirmed flat contract with no working
  orders. Reuse the two chart prices, Limit Cushion, and Auto, Perpetual long,
  and Perpetual short presets. Show the selected account, lots, base quantity,
  estimated loss, margin and leverage in the preview.
- Another same-side Chart Risk Entry adds to that Position Cycle when its stop
  is healthy, covers the verified position, and has yet to trigger. Give the
  add its own risk budget and quantity ceiling. Preview the current size, add
  size, total size and proposed shared stop. Allow one pending entry or add
  for the contract, including an unresolved IOC.
- Use the last trade to trigger a price-capped IOC limit entry. Keep the chart
  prices editable until dispatch. Reconcile by the original client order ID.
  Confirm the opening order's fills and BingX position ID before assigning
  protection. Base add coverage on confirmed fills. Retire a zero-fill add and
  retain coverage after a partial fill.
- Allow partial closes from the existing Order Ticket and reconcile manual
  reductions and other changes reported by BingX. Show the latest confirmed
  remaining position size on the shared stop. If the bound ID closes, retire
  its stop. A subsequent opening has its own Position Cycle.
- On a fresh mark-price crossing, cancel any waiting add. Reconcile any IOC
  already sent before finalizing coverage, and account for late fills of that
  linked order. Close the original position by its ID at market. Maintain
  protection while new entries are disarmed. Show pending or unresolved
  coverage and any residual exposure in Account.
- After a restart or a disconnect that pauses protection, require **Resume**
  after confirming the original position and receiving a fresh mark. Evaluate
  the stop immediately on resume.
- The existing Max Day Loss gate blocks BingX Live openings when enabled.
  Protective closes remain available. Max Day Loss removal is a separate task.
  [Current gates and workflow](../../README.md#usdt-perpetuals)

### Shared stop price for an add

The new stop may stay where it is or tighten. For a
long, the add's stop must be at or above the existing stop and below the add's
entry trigger. For a short, the rule mirrors this. Keep the current stop until
the first confirmed fill of the add. Apply the tighter price once and grow
coverage as subsequent fills arrive.
[Stock position adds specification](../../.scratch/chart-risk-position-adds/spec.md)

## Auto Leverage

On a flat entry, size from the risk budget using the worst allowed IOC limit,
the chart stop, and estimated taker fees on both legs. Round down to valid lots.
Check the minimum order value, collateral, opening limits, and BingX's
notional-based maintenance tiers and deductions. Auto chooses the largest
eligible quantity and then the highest leverage for that quantity within the
preset ceiling and the liquidation buffer. Honor saved numeric ceilings. The
signed capacity at a different leverage is available after that setting is
applied. Preview uses signed side leverage limits, maintenance tiers and
collateral, then arming verifies the chosen size at the resulting leverage.
Offer the exchange ceiling as an explicit option. A Fixed preset requires the
chosen leverage to match the verified exchange setting.

Require clearance beyond the stop of at least the greater of one tick and
0.10% of the IOC limit. Preview the current and selected leverage without
changing it. Verify the account is eligible and the contract is flat before
applying the selected leverage at arming. Read it back and recheck leverage,
capacity, collateral and clearance at activation. The one-way setting affects
both directions and persists if the entry expires or goes unfilled. Estimated
loss uses the stop price. A market close can fill away from it.
[BingX maintenance model](https://bingx.com/en/support/articles/11263342608015/) ·
[BingX leverage API](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-trade/api-reference.md#20-set-leverage)

### Leverage for an add

Size at the position's current verified leverage and
check the preset, collateral, position limit and liquidation clearance against
the combined position and shared stop. Use a smaller add if required. Reject
the add if the position terms cannot be verified or a valid minimum add cannot
fit. Compare the actual leverage, quantity and liquidation price after a fill.
An add leaves exchange leverage writes to the flat-entry workflow.

## Implementation outline

1. Reuse the existing perpetual entry gesture, side routing and chart price
   editing. Tie add intents to the active position and shared stop. Carry the
   original account and position ID through admission, fills, stop moves and
   Resume. Apply the WebSocket contract changes in Go and regenerate TypeScript.
   [Perpetual admission](../../engine/internal/exec/perpetual.go) ·
   [Chart Risk Entry UI](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) ·
   [Go WebSocket payloads](../../engine/internal/uihub/wsmsg/payloads.go)
2. Adapt the held protection state to follow the verified position quantity
   in both directions. Use one persistent whole-position close intent bound to
   that position. Before a stop close, resolve the outstanding add and protect
   any reconciled late fill belonging to it. Use the reviewed account's signed
   position data to verify coverage after every fill, close and reconnect.
   [Protection core](../../engine/internal/exec/perpetual_protection.go) ·
   [BingX positions](../../engine/internal/broker/bingx/account.go) ·
   [BingX orders](../../engine/internal/broker/bingx/orders.go)
3. Journal the full close before calling BingX. Save its exchange order ID and
   query the actual close order and fill quantity. After a lost response, resolve
   the original position and available order or fill history before any retry.
   Keep uncertain closes visible and block a new entry on the contract until
   reconciled. A flat snapshot can retire the old stop. Attribute executions only
   from matching order and fill evidence. Include whole-position ownership in the
   Live acknowledgement and update the user and engine guides with the release.
   [Perpetual order journal](../../engine/internal/broker/perps/adapter.go) ·
   [BingX order lookup](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-trade/api-reference.md#9-query-order-details)

## Verification and rollout

Use fixtures for both environments and a controlled chart session. Cover both
directions, price and lot rounding, flat-entry Auto leverage, add sizing,
first and partial fills, zero fills, liquidation limits, manual half-closes
and direct same-side additions. Cover a stop crossing during a waiting or
submitted add, partial close fills, lost replies, replay, restart and Resume,
disarmed protection, external close and same-side reopen. Verify the public
mark is suitable for each execution venue. Check that a Link Group change
leaves the stop bound to the original account. Run existing stock and MEXC
Chart Risk Entry regressions.

Verify the integrated flow on Virtual first. Check Live account reads and
review Live enablement after Virtual acceptance. Any real order test requires
separate authorization under repository rules. Disable new entries during a
rollback while retaining controls for existing positions and stops.
[Repository live-order rules](../../AGENTS.md#live-order-safety)

The Virtual adapter passed a BTC-USDT exchange cycle using its protected-entry
preflight, a same-ID add, a two-lot manual reduction, and a two-lot
whole-position close reconciled by broker order ID. The test left the account
flat and restored the original isolated leverage. The Live account supplied
signed one-way isolated opening terms. Live order submission is covered by
fixtures.
