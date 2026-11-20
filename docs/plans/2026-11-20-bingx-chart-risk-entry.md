# BingX Chart Risk Entry

Approved on 2026-11-20.

## Exchange verification

BingX Virtual accepted a sized reduce-only market close with the wrong
position ID on a one-way, isolated BTC-USDT position. The opening IOC's
position ID matched the signed position snapshot, and the simulated position
was closed after the test. The documented position-ID close acts on the whole
position. Enabling Chart Risk Entry on either venue requires an exchange route
that enforces both position ID and quantity.
[BingX adapter notes](../external-apis.md#usdt-perpetual-exchanges)

## Goal

Use Chart Risk Entry on a BingX perpetual chart with either BingX Virtual or
BingX Live selected in its Link Group. eTape holds the protective stop for both
venues. Address each entry and stop to its original Execution Venue and account.
Show VST for Virtual collateral and USDT for Live collateral. Both charts use
the BingX public feed. [Execution Venue guide](../../README.md#usdt-perpetuals) ·
[Link Group decision](../adr/0005-link-group-owned-execution-venue.md)

## Entry and protection

- Start from flat with no working orders on that contract in one-way, isolated
  mode. Reuse the two chart prices, risk budget, Limit Cushion, and Auto,
  Perpetual long, and Perpetual short presets. Preview lots, base quantity,
  estimated loss and margin, effective leverage, and the selected account.
- Let the last trade trigger a price-capped immediate-or-cancel limit entry.
  Keep both chart prices editable until dispatch. Reconcile the original client
  order ID after an ambiguous response. Show protection as pending until actual
  fills and Position Cycle ownership are confirmed. A terminal partial fill
  protects the filled size. A zero fill retires the pending stop.
- Trigger the fixed protective stop on BingX's fresh mark price and submit a
  position-reducing market close. Allow stop edits while the bound position and
  covered quantity remain valid and the edited stop clears liquidation. Cap
  coverage at the confirmed chart entry quantity. Shrink it if the position
  drops below that coverage. Show confirmed coverage and uncovered quantity in
  Account. Allow protection to trigger while new entries are disarmed.
- After a restart or a disconnect that pauses protection, require **Resume**
  after reconciling the original position and receiving a fresh mark. Evaluate
  the stop immediately on resume.
  [Current MEXC Live workflow](../../engine/internal/exec/README.md#mexc-live-chart-risk-entry)
- Treat Max Day Loss as a separate task. Under the current gate, an active cap
  blocks BingX Live openings and protective reductions remain available.
  [Current loss gate](../../README.md#usdt-perpetuals)

## BingX Auto Leverage

Size from the risk budget using the worst allowed IOC entry price, the chart
stop, and estimated taker fees on both legs. For each eligible leverage, round
down to valid BingX lots and check minimum order value, available collateral,
account limits, and the contract's notional tier. Rank candidates by largest
eligible quantity first and highest passing leverage at that quantity second.
The preview shows estimated loss at the stop. A market close can fill away from
that price.

Require margin to cover losses, maintenance, and closing fees at a mark beyond
the stop. Start with eTape's existing minimum clearance of the larger of one tick
or 0.10% of the IOC limit, using verified BingX maintenance tiers and deductions.
Use the same calculation for preview, admission, and dispatch. A Fixed preset
requires its chosen leverage to match the verified exchange setting and pass the
liquidation check. [BingX maintenance model](https://bingx.com/en/support/articles/11263342608015/) ·
[BingX liquidation threshold](https://bingx.com/en/support/articles/18418923135631)

Honor saved numeric caps and offer an explicit option to use the exchange's
maximum as the ceiling on BingX. Show the current and selected leverage in the
read-only preview. In one-way mode, a leverage change applies to the account's
contract in both directions and affects subsequent orders. Apply the selected
setting at arming if it differs from the current one. Verify it and the opening
capacity before accepting the arm. Recheck both at activation. The exchange
setting persists if the entry expires or goes unfilled.
[BingX Set Leverage](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-trade/api-reference.md#20-set-leverage)

## Implementation outline

1. Extend the existing perpetual Chart Risk Entry preview and admission for the
   selected BingX account. Reuse its signed margin, leverage, allowed size, and
   account reads. Verify how to obtain the leverage and notional limits for a
   proposed size, along with maintenance tiers and fees, in each environment.
   The account's available opening volume read is for its current setting.
   Check flat position, working orders, and the opening gates before any
   leverage write. Reserve the account and contract against competing eTape
   openings while arming. Reconcile an ambiguous write with an account read
   before proceeding. Pause if fresh terms cannot support the reviewed entry.
   Require a new review if an amendment needs different leverage. Compare the
   resulting leverage and liquidation price against the broker's position
   snapshot after a fill.
   [Entry core](../../engine/internal/exec/perpetual.go) ·
   [BingX orders](../../engine/internal/broker/bingx/orders.go) ·
   [BingX trade API](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-trade/api-reference.md#39-position-and-maintenance-margin-ratio)
2. Extend BingX reconciliation to prove the link between the IOC fills and the
   exchange position ID. Verify whether an exact match in order detail or order
   history supplies the same position ID as the signed position snapshot. Retain
   pending ownership across restart if the ID arrives after terminal entry status.
   Keep the Virtual account identity separate from Live custody acknowledgement.
   Persist ownership before arming a stop. An external close and same-side reopen
   must retire the original stop. If the original position cannot be proven, show
   the exposure and require manual resolution.
   [BingX account reads](../../engine/internal/broker/bingx/account.go) ·
   [BingX order history](https://bingx-api.github.io/docs-v3/#/en/Swap/Trades%20Endpoints/Query%20Order%20history) ·
   [Protection core](../../engine/internal/exec/perpetual_protection.go)
3. Verify which one-way close request can enforce the original position ID at
   BingX. A contract-only reduce-only order can hit a later same-side position
   in the gap between the last account read and submission. BingX documents a
   market close by position ID for the whole position, with no client order ID.
   Prove that the selected close route enforces both the covered quantity and
   the position ID, including rejection of a stale ID when a same-side position
   has reopened. Preserve the close intent across restart, reconcile ambiguous
   outcomes, and show remaining exposure after a terminal close.
   [BingX close by position ID](https://github.com/BingX-API/api-ai-skills/blob/main/skills/swap-trade/api-reference.md#15-close-position-by-positionid)
4. Reuse the chart prices and Account states with the selected venue's VST or
   LIVE badge. Include stop custody and automatic leverage changes in the
   account-scoped Live review. Protect the original account if the Link Group
   changes. Update the user and execution guides when shipping the feature.
   [Chart Risk Entry UI](../../ui/src/chrome/panels/tv/ChartRiskEntry.tsx) ·
   [Adapter and environment guide](../external-apis.md#usdt-perpetual-exchanges)

## Verification and rollout

Use fixtures for both environments and a controlled chart session. Cover long
and short, price and lot rounding, stale prices, changed account terms, partial
and zero fills, lost entry and close responses, manual reductions, external
close and reopen, restart and Resume, disarmed protection, and a close that
leaves exposure. Verify that the public mark used by each chart is suitable for
its Execution Venue. Check Link Group switches between VST and Live for account,
orders, and collateral labels. Run the existing MEXC Chart Risk Entry tests.

Check a contract where the liquidation buffer limits leverage and one where
higher leverage constrains quantity. Verify the preview leaves settings alone,
only the reviewed setting is applied, ambiguous writes reconcile before dispatch,
and a changed setting or insufficient available size pauses the entry.

Prove the workflow in Virtual first. Require confirmed position ownership,
quantity-limited closes, and leverage readback before enabling each venue. Verify
Live account reads and review Live enablement after Virtual acceptance. Any real
order test needs separate authorization under the repository's live-order rules.
If enabling entries is rolled back while a position remains open, keep its
protection visible and manageable until it is closed or the trader explicitly
cancels the stop. [Repository validation and live-order rules](../../AGENTS.md#live-order-safety)
