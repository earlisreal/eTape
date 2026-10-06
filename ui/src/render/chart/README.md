# Chart Renderer

`ChartController` keeps `BarStore` and the engine's bars authoritative while
the chart derives its display series imperatively. High-frequency updates do
not flow through React state.

On the Daily chart, a dashed `Post` price line follows the latest live 1-minute
close during the chart's 16:00–20:00 ET post-market window. It disappears when that window
ends or the symbol/timeframe changes. The official Daily bar remains unchanged.

## 10-second display

The `10s` display contains real bars, explicit Volume-Only Bars, and completed No-Trade Bars, plus explicit
time-scale whitespace for confirmed Data Gaps. The current incomplete interval
is never fabricated. A No-Trade Bar is flat at the previous same-session real
close with zero volume; it is not carried across premarket, regular, postmarket,
overnight, or weekend boundaries, and a new
session needs a real bar before quiet intervals can be filled.

A Volume-Only Bar is a real eligible-volume bucket with no Price-Forming Print.
It is flat at the prior trusted last-eligible close, retains real volume, and
keeps ordinary candle styling. TypeScript does not infer this state from candle
shape or volume. No-Trade Bars remain zero-volume synthetic display fills.

When the OpenD link is down, provisional No-Trade Bars are suppressed. A real
bar marked `gap` confirms a Data Gap since the previous trustworthy real bar;
the interval remains visually empty and any provisional No-Trade Bars in it are
removed. A delayed real bar replaces a No-Trade Bar at the same timestamp.

## Volume Indicator

Each Chart Panel normalizes one canonical `VOLUME` instance with a stable
per-panel ID. The controller owns its local histogram and reads the same
`DisplayBar[]` as the candles, so it uses no engine indicator subscription,
`IndicatorStore` key, or chart-window hydration. Real and Volume-Only bars keep
their directional colors; synthetic No-Trade Bars and Data Gaps remain empty.

Visible Volume reserves the existing lower 25% of the price pane. Hiding its
instance or histogram output, or removing it, restores the ordinary candle
scale; showing or re-adding it reapplies both scales and the current display
bars immediately.

## Viewport behavior

For `10s`, an appended bar follows when the previous newest displayed slot is
at least partially visible. If it is completely outside the view, the current
range and zoom are preserved. Future Buffer space is consumed until four empty
bar widths remain; after that, the range shifts by the number of appended
slots while preserving its width. Corrections and gap repair preserve the
current viewport and zoom; a disappearing provisional tail keeps its logical
Future Buffer, while a generation replacement stays timestamp-anchored. While
a pointer or wheel gesture is active, bars keep painting but structural updates
preserve the gesture's current range; release reclassifies that final range
without replaying missed movement.
Symbol/timeframe changes start in Live View, and
Reset Chart View restores default spacing, four-bar right padding, and price
auto-scaling. The chart menu exposes Reset Chart View; it has no separate live
navigation command.

The visible 1-minute behavior remains unchanged: raw bars paint immediately
and its existing boundary-follow behavior is retained.

Sessions, drawings, and the legend consume the same display bars. The countdown
uses the newest eligible raw price from the active ET
trading day, so it remains useful through quiet or delayed buckets and ignores
far-future data. The market clock is estimated from OpenD's upstream server
timestamp and synchronized to the browser through WebSocket ping/pong; without
a valid sample, charts use browser time and retain the last valid offset across
a failed probe. A rate-limited `chart market clock boundary` trace records the
clock inputs used for diagnostics.

The merged price/countdown badge is the last React child in the chart host, so
React appends it after Lightweight Charts' native DOM. Its z-index 1 then paints
above the base axis canvas at the same level while the native crosshair canvas
at z-index 2 remains above it.

Chart drawings consume the Future Buffer as future chart positions. Their future
Drawing Anchors are not clamped to the newest loaded bar; incoming displayed bars
eventually align with those anchors.

A symbol open waits for the engine's `chart-ready` barrier, queries the prepared
archive/seed once, and calls `setData` once; pan and zoom do not request history.
Older provider backfill is archive-only and appears on the next symbol open.
Main-pane indicators autoscale against visible candles, while live bars continue
through imperative store/controller updates. Preserve chronological merge/dedupe
and controller disposal. Focused tests run with `npm exec vitest -- run
--project chart-core src/render/chart/ChartController.test.ts`; the full UI suite
runs with `npm test`.

## Visible high/low

The chart controller projects the eligible high/low anchors from its displayed
bars and current visible logical range. `VisibleExtremaPrimitive` renders the
projection imperatively on the main price series, so live updates and viewport
changes do not enter React state; synthetic No-Trade Bars and Data Gaps are
excluded.

## Crosshair Sync

The chart facade publishes native pointer time and exact main-pane stock price
to the UI-only cross-window coordinator. Receivers map that time to their own
display bars and use Lightweight Charts' imperative crosshair API; their view,
scales, and history remain unchanged. Indicator-pane pointers share time only.
The receiver legend selects its mapped candle, and clears back to latest when
the cursor leaves, its matching candle is absent, or the position is off-screen.
No-Trade Bars and Volume-Only Bars are eligible; Data Gaps and empty Future
Buffer positions are not. Cursor movement stays outside React state and legend
work remains frame-coalesced.

`orderCrosshair.ts` temporarily colors the native crosshair for chart order
selection and snaps native mouse Y to the order tick while preserving pointer
X, including future blank space. It restores normal formatting and theme on
release/cancellation; action colors are local and are not sent through Crosshair
Sync. Full-line order hit testing uses `nearestPriceLines` with an eight-pixel
tolerance and returns all exact nearest ties for explicit selection.
