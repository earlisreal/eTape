# Chart Crosshair Sync

Status: ready-for-agent
Approval: Earl approved implementation on 2026-10-02 by requesting implementation of this spec.

## Requested Behavior

- Each Chart Panel has its own Crosshair Sync toggle.
- Crosshair sharing requires both the source and receiving Chart Panels to have sync enabled.
- Sharing is limited to charts with the same color selection.
- The existing color selection is a non-null Link Group; pinned charts have no shared color group.

## Domain Language

- The existing color selection is a Link Group, distinct from a Panel Group.
- Crosshair Sync is the feature name, distinct from Scanner Sync and symbol focus; its definition is recorded in `CONTEXT.md`.

## Agreed Behavior

Earl accepted all Round 1 and Round 2 recommendations on 2026-10-02:

1. Share across all open workspace windows, consistent with Link Group scope.
2. Share time and stock price between eligible Chart Panels displaying the same symbol.
3. Provide a direct Chart Panel Header icon toggle with a Crosshair Sync tooltip and an explicit on/off state.
4. Default off for new and existing charts without a saved preference; persist each Chart Panel's choice across restart.
5. Preserve each receiving chart's viewport and hide the synchronized crosshair when its position is outside that view.
6. Clear synchronized crosshairs when the source pointer leaves the chart.
7. Match a finer source candle to the destination candle containing its start time. From a coarser source, use the first available finer destination candle starting within the source candle's period, including D/W/M periods.
8. Share the exact pointer price rather than the selected candle's close.
9. Hovering an indicator pane shares time only; indicator values are not stock prices and are not shared as horizontal price lines.
10. While a synchronized crosshair is visible, receiving legends show their own mapped candle's OHLC, volume, and indicator values. Clearing or hiding that crosshair returns the legend to latest values.
11. Share positions on loaded displayed candles, including No-Trade Bars and Volume-Only Bars. Data Gaps and empty Future Buffer positions clear the shared position. If a destination has no eligible matching candle, hide its synchronized crosshair rather than substitute an unrelated nearby candle.
12. The currently hovered eligible chart drives sharing. Source symbol/timeframe/Link Group changes, disabling sync, closing, or hiding the source clear its shared position. Changes to a receiving chart clear or re-evaluate that chart's eligibility without clearing other receivers.
13. Continue passive crosshair sharing while drawing, measuring, or dragging order lines. Receiving charts do not perform drawing, order, or pointer interactions as a result.
14. A pinned chart's toggle stays visible but disabled while it has no Link Group. Preserve its saved preference and resume participation when it is linked again.

## Examples

| Source | Receiving Chart | Result |
| --- | --- | --- |
| Green AAPL, sync on, pointer at 09:32 on 1m | Green AAPL, sync on, 5m | Show the 09:30 candle and the source's exact stock price if visible. |
| Green AAPL, sync on, a Daily candle | Green AAPL, sync on, 1m | Show the first available minute candle in that ET trading date if visible. |
| Green AAPL, sync on, pointer over MACD | Green AAPL, sync on | Share the mapped time and receiving candle's legend values, with no shared horizontal price line. |
| Green AAPL, sync on | Green AAPL, sync off | Receiving chart remains independent. |
| Green AAPL, sync on | Blue AAPL, sync on | No sharing, because Link Groups differ. |
| Green AAPL, sync on | Green chart temporarily displaying another symbol | Ignore the update until symbols match. |
| Any eligible source | Receiver's matching candle is absent, a Data Gap, or outside its view | Hide the synchronized crosshair and return its legend to latest; preserve its viewport. |
| Source pointer leaves, source opts out, or source context changes | Eligible receivers | Clear that source's shared position; preserve any newer source's ownership. |

## Acceptance Criteria

- Both source and receiver must be enabled, have the same non-null Link Group, and display the same symbol. Same-symbol charts with different colors do not share.
- Cross-window behavior matches same-window behavior across open eTape workspaces sharing the application's origin/browser context, consistent with existing Link Groups.
- New and previously saved charts with no preference begin off. Toggling one chart changes only that chart's preference; ordinary save/reload and layout export/import preserve the choice.
- The header toggle exposes an accessible name and pressed state, shows a clear active state and tooltip, remains usable in narrow chart headers, and is disabled for pinned charts.
- Intraday, D/W/M, and coarse-to-fine mappings use exchange-calendar candle periods and actual destination timestamps. A missing candle never becomes an unrelated nearest match or viewport-edge match.
- Price-enabled receivers display the same stock price as the source pointer; they do not snap it to the receiving candle's close. Indicator-pane sources share time without converting indicator units to stock prices.
- Receivers keep their own pan, zoom, price scale, and Live View/Historical View state. Crosshair display never pans, changes autoscaling, or loads history.
- Receiving OHLC, volume, and indicator legends follow the receiving timeframe's mapped candle while the shared crosshair is visible and return to latest values on clear/hide.
- No-Trade Bars and Volume-Only Bars are eligible displayed candles. Data Gaps, absent destination candles, and empty future space do not fabricate a position.
- Source leave, hiding, closure, context changes, and opt-out clear its position promptly. Crosshair coordinates are transient and are not restored after restart.
- Receiving charts never rebroadcast synchronized positions. A former source's delayed clear cannot remove a newer source's position, and stale symbol/group updates cannot place a crosshair on an unrelated chart.
- Real drawing, measuring, and order-line interactions continue normally. Shared crosshairs and legends are display-only and never produce remote clicks, drawing edits, order submissions, replacements, or cancellations.
- High-frequency cursor data stays outside React state, uses the existing imperative chart flow, and coalesces rendering to animation frames. All subscriptions and scheduled work are disposed when a chart is removed.

## Implementation Boundaries

- Reuse saved panel settings, existing header control patterns, and the installed chart library's imperative crosshair APIs. No new dependency or engine/WebSocket contract is needed.
- Keep cursor delivery separate from symbol/venue focus notifications so pointer movement does not trigger low-frequency panel subscriptions or persistence writes.
- Native programmatic crosshair changes do not emit the move subscription used by legends; update receiving legend selection explicitly.
- Cross-window cursor ownership and messages remain UI-only. Handle normal source disposal/hiding and unexpected source loss so receivers do not retain a dead cursor indefinitely.
- Extend the owning UI/chart READMEs when implementing the feature; the current guides continue to describe shipped behavior.
- No separate global sync switch, extra Link Groups, independent sync grouping, indicator-value synchronization, viewport synchronization, sticky cursor, or future-time projection is part of this feature.

## Existing Code Facts

- Link Groups share focused symbols and execution venues across windows through a BroadcastChannel (`ui/src/chrome/linkGroups.ts`).
- Per-panel preferences already live in saved `PanelConfig.settings` (`ui/src/chrome/workspace.ts`).
- Chart header controls already contain timeframes, indicators, drawings, screenshot, and settings (`ui/src/chrome/panels/tv/ChartHeaderControls.tsx`).
- The chart facade exposes a crosshair-move subscription for local legend tracking (`ui/src/render/chart/ChartApiFacade.ts`).
- The installed chart library supports setting and clearing a crosshair programmatically; those operations do not notify the existing crosshair-move subscription, so synchronized legend behavior needs an explicit decision.
- The local crosshair follows the pointer freely rather than snapping its price to a candle or indicator (`ui/src/render/chart/chartTheme.ts`).
- Existing legends display the selected candle's OHLC, volume, and indicator values; clearing the selected crosshair returns them to the latest candle (`ui/src/chrome/panels/tv/legendView.ts`).
- High-frequency chart interaction must remain imperative, outside React state.

## Design Tree

Round 1 and Round 2 are settled. The design frontier is empty. No ADR is needed for these reversible UI choices.

## Draft Policy And Validation

The spec and glossary changes are included with the implementation commit, per `AGENTS.md`.
Implementation validation and any skipped CI checks are recorded at handoff.

For implementation, add focused runnable checks for candle mapping, eligibility/ownership/clearing, receiving legend behavior, and header preference behavior using existing test infrastructure. Verify actual crosshair rendering and passive interactions in the browser across two charts, two windows, and narrow layouts. Run the CI-equivalent Windows checklist required by `README.md` before committing and delivering the resulting feature.
