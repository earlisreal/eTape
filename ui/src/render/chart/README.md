# Chart Renderer

`ChartController` keeps `BarStore` and the engine's bars authoritative while
the chart derives its display series imperatively. High-frequency updates do
not flow through React state.

## 10-second display

The `10s` display contains real bars, explicit Volume-Only Bars, and completed No-Trade Bars, plus explicit
time-scale whitespace for confirmed Data Gaps. The current incomplete interval
is never fabricated. A No-Trade Bar is flat at the previous same-session real
close with zero volume; it is not carried across premarket, regular, postmarket,
overnight, or weekend boundaries, and a new
session needs a real bar before quiet intervals can be filled.

A Volume-Only Bar is a real eligible-volume bucket with no Price-Forming Print.
It is flat at the prior trusted last-eligible close, retains real volume, and
keeps ordinary candle styling; the legend labels it `volume only`. TypeScript
does not infer this state from candle shape or volume. No-Trade Bars remain
zero-volume synthetic display fills and are labeled separately.

When the OpenD link is down, provisional No-Trade Bars are suppressed. A real
bar marked `gap` confirms a Data Gap since the previous trustworthy real bar;
the interval remains visually empty and any provisional No-Trade Bars in it are
removed. A delayed real bar replaces a No-Trade Bar at the same timestamp.

## Viewport behavior

For `10s`, an appended bar follows when the previous newest displayed slot is
at least partially visible. If it is completely outside the view, the current
range and zoom are preserved. Future Buffer space is consumed until four empty
bar widths remain; after that, the range shifts by the number of appended
slots while preserving its width. Corrections and gap repair preserve the
existing timestamp view. Symbol/timeframe changes start in Live View, and
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
