# Visible High/Low Callouts in Chart Panels

Status: ready-for-agent

## Problem Statement

When a trader pans or zooms a Chart Panel, they must visually scan every displayed price bar to find the highest and lowest price in the current Chart Viewport. The price scale and current-price treatment do not identify the two extrema, and ordinary Chart Drawings are the wrong tool because they are trader-authored, persistent annotations rather than transient facts about the current view.

This makes it slower to assess the range of a Historical View, compare a compressed intraday move, or orient after changing a symbol or timeframe.

## Solution

Add a default-on, per-Chart-Panel setting named **Show visible high/low**. When enabled, the price pane shows passive high and low point callouts for eligible price bars intersecting the current Chart Viewport.

Each callout is a compact, theme-muted short leader anchored to the actual displayed point and a formatted price label. The high label sits above its point and the low label below it. Labels remain inside the price pane, flip horizontally near its right edge, and separate when their prices are close together. They are not full-width price levels, price-scale labels, Chart Drawings, or interactive controls.

Candle and bar charts use visible High and Low values. Line and area charts use visible Close values, so each label remains on the rendered line or area. Extended-hours bars participate when displayed. Data Gaps and No-Trade Bars do not participate. The annotations update with the viewport and live chart data without routing high-frequency data through React state.

## User Stories

1. As an active trader, I want to see the highest eligible price in my current Chart Viewport, so that I can assess the visible range without manually scanning every bar.
2. As an active trader, I want to see the lowest eligible price in my current Chart Viewport, so that I can quickly identify the visible downside of a move.
3. As an active trader, I want the extrema callouts to update when I pan, so that they describe the Historical View I am actually examining.
4. As an active trader, I want the extrema callouts to update when I zoom, so that they reflect the newly visible price bars.
5. As an active trader, I want a partially visible bar to count, so that an extrema does not jump merely because the bar crosses a viewport edge.
6. As an active trader, I want candle and bar charts to use wick High and Low values, so that the annotations identify the true visible trading range.
7. As an active trader, I want line and area charts to use their rendered Close values, so that a callout always points to a value I can see on the chart.
8. As an active trader, I want displayed extended-hours price bars included when they are visible, so that the current Chart Viewport is treated consistently across sessions.
9. As an active trader, I want Data Gaps excluded, so that missing or untrustworthy market data cannot be presented as a price extreme.
10. As an active trader, I want No-Trade Bars excluded, so that a carried-forward price cannot steal a callout from a real price event.
11. As an active trader, I want no extrema callout when the viewport has no eligible price bar, so that eTape never borrows a price from outside my view.
12. As an active trader, I want a repeated high or low marked at its rightmost visible occurrence, so that the annotation is closest to the latest matching price action.
13. As an active trader, I want one combined \`H/L <price>\` callout when the visible high and low are identical, so that duplicate labels do not obscure a flat range.
14. As an active trader, I want nearby high and low labels separated without hiding either value, so that a tight range remains readable.
15. As an active trader, I want ordinary callouts to show the chart-formatted price without redundant text, so that the chart remains compact.
16. As an active trader, I want the high label above its point and the low label below its point, so that the leader does not obscure the corresponding wick or line point.
17. As an active trader, I want a label to flip left when there is insufficient room on its right, so that it stays inside the price pane rather than overlapping the price scale.
18. As an active trader, I want the callouts to follow the active light or dark chart theme, so that they remain legible without introducing bullish or bearish meaning.
19. As an active trader, I want the callouts to be passive, so that they do not interfere with my crosshair, panning, zooming, or Chart Drawing interactions.
20. As an active trader, I want a separate **Show visible high/low** Chart Panel setting, so that I can turn these annotations off when I prefer an uncluttered chart.
21. As an active trader, I want that setting enabled by default, so that visible extrema are immediately available in a new Chart Panel.
22. As an active trader, I want the setting retained for each Chart Panel across restarts, so that one panel can remain focused while another keeps the annotations.
23. As an active trader, I want older saved Chart Panel settings to gain this feature enabled, so that an upgrade does not silently change my normal chart presentation.
24. As an active trader, I want turning the setting off to remove both callouts immediately, so that the control has an obvious result.
25. As an active trader, I want turning the setting back on to calculate the current extrema immediately, so that I do not need to pan or wait for a new bar.
26. As an active trader, I want the annotations to refresh after a symbol or timeframe change, so that no prior chart's extrema linger.
27. As an active trader, I want the annotations to refresh as a live bar changes, so that Live View continues to describe the current visible range.
28. As an active trader, I want extrema callouts to leave Live View, Historical View, Future Buffer behavior, and price autoscaling unchanged, so that this display aid never moves my chart.
29. As a Chart Panel user, I want the annotations to remain distinct from my saved Chart Drawings, so that changing the viewport does not create, alter, or delete my own work.
30. As a maintainer, I want extrema calculation to use the already displayed chart bars and visible logical range, so that the feature does not request extra history or duplicate market-data state.
31. As a maintainer, I want viewport-driven updates coalesced with the existing chart paint work, so that panning remains responsive at native input-event rates.
32. As a maintainer, I want no engine, market-data, or WebSocket-contract change, so that a presentation feature cannot alter bars, marks, or order behavior.
33. As a maintainer, I want behavior tested through visible extrema and rendered callout outcomes, so that refactoring the internal scan or canvas implementation does not make tests brittle.

## Implementation Decisions

- Extend the per-Chart-Panel presentation settings with a persisted Boolean named \`visibleExtrema\`, surfaced as **Show visible high/low**. It defaults to enabled, including when normalizing saved settings created before this feature existed.
- Keep the feature entirely in the chart UI. It consumes the Chart Controller's already displayed bars and current visible logical range; it does not request backfill, alter bar construction, change engine state, or add a WebSocket contract.
- Add one public visible-extrema projection at the Chart Controller boundary. It returns the two transient callout anchors needed by the renderer, rather than exposing or duplicating the controller's internal displayed-bar scan in the Chart Panel.
- Select every eligible rendered bar that intersects the Chart Viewport, including bars clipped at either horizontal edge. Do not use bars outside the range.
- For candlestick and bar chart types, choose the maximum High and minimum Low. For line and area chart types, choose the maximum and minimum Close because Close is the plotted point.
- Exclude Data Gaps and No-Trade Bars from extrema selection. If no eligible rendered price point remains, expose no callout.
- Break equal-price ties by retaining the rightmost visible matching bar. If high and low resolve to the same price, expose one combined \`H/L\` callout instead of two labels.
- Render extrema in a dedicated, imperative, main-price-series canvas overlay. It is transient display state, not a Chart Drawing, and must not be coupled to the trader-authored drawing primitive or persisted drawing model.
- Use the active chart palette, chart font, and existing price formatter. Ordinary callouts contain only the formatted price; the combined flat-range callout prefixes the same formatted price with \`H/L\`.
- Draw a compact muted leader from each anchor to its label. Position the high label above its anchor and the low label below it. Keep labels within the price pane by flipping horizontally at the right edge and by minimally separating close labels; preserve both labels whenever their values differ.
- Do not render full-width horizontal lines, price-axis chips, filled badges, gain/loss color coding, hover surfaces, hit targets, alerts, or selectable controls.
- Refresh the overlay after every coalesced chart paint and after visible-range changes. The visible-range callback may run at native input rate, so it may schedule work but must not trigger React state updates or duplicate a calculation within the same animation frame.
- Refresh, hide, or detach the overlay correctly as the main series changes, the symbol or timeframe changes, the setting changes, chart data becomes unavailable, the palette changes, or the Chart Panel unmounts.
- Keep pointer handling unchanged. The overlay is display-only and cannot intercept crosshair, pan, zoom, or Chart Drawing input.
- No new dependency, API endpoint, configuration migration, ADR, or documentation-page redesign is required. The existing chart primitive and settings conventions are sufficient.

## Testing Decisions

- A good test observes selected extrema, callout anchors, formatted labels, settings behavior, or recorded canvas output. It must not assert private loop structure, request-animation-frame implementation details, React render counts, or helper-call order.
- Use the existing Chart Controller fake-facade test seam as the primary behavioral seam. Give it displayed bars and a visible logical range, then observe the public extrema projection. This keeps the selection contract at the highest existing chart-data boundary.
- At the primary seam, prove candle/bar High-Low selection; line/area Close selection; partially visible edge bars; extended-hours inclusion; rightmost equal-value ties; a flat range producing one combined callout; Data Gap exclusion; No-Trade Bar exclusion; and no result when no eligible point is visible.
- Reuse the existing chart primitive recording-context test style for the small visual contract: leaders anchor at the selected point, ordinary labels contain only formatted price, combined labels contain \`H/L\`, placement flips/clamps inside the pane, and close values separate rather than disappear.
- Reuse the existing Chart Panel integration test harness to prove that the persisted setting defaults on, older saved settings normalize to on, disabling removes the overlay, re-enabling immediately restores it, and lifecycle changes do not leave stale annotations attached to a replacement series.
- Reuse the existing Chart Settings dialog tests to prove the checkbox's accessible label and saved value. Do not add browser end-to-end coverage for a local canvas presentation feature unless the existing focused seams expose a gap.
- Run focused UI tests and type checking during implementation. This is a UI-only change without generated contracts, engine changes, or live-order effects; broader validation remains proportional to the final diff.

## Out of Scope

- Full-width high/low levels, price-axis labels, current-price-line changes, or any price-alert behavior.
- Persistent extrema annotations, a new Chart Drawing type, drawing-tool controls, or saving extrema per symbol.
- Hover cards, click targets, context-menu actions, keyboard shortcuts, notifications, or sound.
- Extrema from volume, indicators, order data, account data, or a different Chart Panel.
- Including Data Gaps or No-Trade Bars as price events, or borrowing an extrema from outside the Chart Viewport.
- Changes to Live View, Historical View, Future Buffer, reset behavior, autoscaling, bar aggregation, market-data eligibility, or execution marks.
- Engine changes, WebSocket contract changes, generated TypeScript changes, new backend queries, or changes to live-order behavior.
- User-configurable colors, line styles, label templates, or a general annotation framework.

## Further Notes

- The supplied reference image distinguishes compact extrema point callouts from the separate full-width current-price line. This feature intentionally follows the point-callout treatment.
- **Chart Panel**, **Chart Viewport**, **Live View**, **Historical View**, **Future Buffer**, **No-Trade Bar**, **Data Gap**, and **Chart Drawing** use the repository glossary's meanings.
- The existing rendering invariant remains decisive: high-frequency chart data and viewport changes stay imperative and animation-frame-coalesced, never routed through React state.
- The existing workspace-layout ADR does not conflict with this per-Chart-Panel presentation setting; it requires no layout version change.
