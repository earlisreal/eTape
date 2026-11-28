# Chart trade marker visibility

Approved and implemented on 2026-11-28.

## Goal and scope

Add a **Show trade markers** checkbox in **Chart settings** so a chart can show
or hide every executed-fill diamond. The checkbox covers buys, sells, short
sales, and buy-to-covers for stocks and crypto. It controls the drawing of the
diamonds on that chart.

## Chart behavior

- Open **Chart settings** from the gear or from **Settings…** in the chart’s
  right-click menu. **OK** applies and saves the choice. Closing the dialog
  without applying discards the draft.
- Each chart saves its choice with the workspace. It follows the chart through
  symbol, timeframe, and chart-style changes, workspace reopen, and layout
  export and import. New charts and older layouts with a missing setting show
  markers by default.
- Applying the checkbox redraws the chart even when the market is quiet.
  Turning it on shows historical markers and any fills that arrived while the
  diamonds were hidden.

## Current behavior and implementation

1. Add `showTradeMarkers` to the per-chart settings and checkbox list. Default
   to visible for a missing or malformed setting, retain an explicit `false`,
   and use the existing settings save path. Layout export and import already
   carry these panel settings. [Chart settings](../../ui/src/chrome/panels/tv/ChartSettingsDialog.tsx),
   [layout backup](../../ui/src/chrome/backup.ts)
2. Give the chart’s fill-diamond primitive a visibility flag. Apply the saved
   value when creating the chart and apply changes through the settings
   handler, following the visible high/low control. Gate drawing in the
   primitive and request a canvas redraw on visibility changes. The chart
   already feeds the same primitive with aggregated history and live fills.
   [Chart Panel](../../ui/src/chrome/panels/ChartPanel.tsx),
   [fill diamonds](../../ui/src/render/chart/diamondPrimitive.ts),
   [visible high/low](../../ui/src/render/chart/visibleExtremaPrimitive.ts)
3. Document the checkbox and its per-chart behavior in the [UI guide](../../ui/README.md).

## Verification

- Extend focused settings and chart checks for default-on behavior, explicit
  off, **OK**, and closing without applying. Verify that layout export and
  import preserve the choice and that an older layout still restores its other
  panels and settings. [Chart settings tests](../../ui/src/chrome/panels/tv/ChartSettingsDialog.test.tsx),
  [Chart Panel tests](../../ui/src/chrome/panels/ChartPanel.test.tsx),
  [backup tests](../../ui/src/chrome/backup.test.ts)
- Use fixed bars and synthetic fills in the demo browser harness. Put all four
  fill sides at distinct positions on two charts of the same symbol. Verify
  that toggling one chart clears and restores its diamonds without waiting for
  fresh bars and that the other chart keeps drawing them. Send a fill while
  hidden and verify that it appears when enabled. Reopen the workspace and
  switch timeframe and chart style to check that the saved choice still applies.
  [Fixed-bar browser fixture](../../ui/e2e/volume-overlap.spec.ts)
- Run the focused UI tests, browser scenario, lint, typecheck, build, and
  `git diff --check`. Verify hosted CI for the implementation on `main`.

## Rollout and rollback

The default preserves visible markers across upgrades. The main regression
risk is a stale canvas on quiet charts, so exercise both directions with the
bar feed paused. An application rollback restores the prior behavior of always
drawing the diamonds.
