# Chart axis flicker — 2026-10-23

The reported XXII 5-minute chart shifted sideways faster than the supplied
30 fps recording could consistently capture. The recording still showed the
visible low alternating between 0.2000 and 0.2021 as the price gutter changed.

## Diagnosis

A deterministic, isolated production ChartPanel with fixed sub-dollar bars,
Volume and a 554 × 436 panel reproduced a 58/64 px gutter oscillation on every
browser frame, even with no incoming data or pointer input. Native crosshair
label width estimation reserves a minus sign when autoscale extends below zero.
That wider gutter excludes an edge candle; the narrower price range then removes
the sign and shrinks the gutter, revealing the candle again.

Holding the native minimum width at 64 px stopped the oscillation. Disabling
crosshair price labels also stopped it; disabling the countdown badge did not.
The fix retains a wider measured gutter as the native minimum during the existing
imperative chart paint, allowing further growth without repeated shrink/expand
feedback. No market data or viewport-follow policy changes.

## Validation

- `cd ui; npm run e2e:chart-layout` failed before the fix with
  `light idle: the price axis must not alternate widths` (`2 !== 1`).
- The same command passed after the fix. It samples 100 consecutive browser
  frames per light/dark and idle/live combination, asserting fixed plot and
  gutter widths, and runs the existing order/risk overlay scenarios.
- `cd ui; npm exec vitest -- run --project chart-core --project chart-panel`:
  27 files, 586 tests passed. The first sandbox attempt could not rename the
  runner's temporary cache; rerunning with a workspace-local temporary directory
  passed without test changes.
- `cd ui; npm run lint`: passed.
- `cd ui; npm run build`: passed, including both type checks. The existing
  large-bundle warning remains.
- Markdown link validation and `git diff --check`: passed.
- Local engine/race/contract checks and the entire Playwright suite were not
  repeated: the change affects only chart layout; the focused browser suite
  exercises the actual production ChartPanel. Full hosted CI is the push gate.
- Verification in Earl's running live application remains manual; no live
  orders or running trading processes were touched.

Regression fixture and assertions live in
[the chart layout harness](../../ui/e2e/chart-order-layout.mjs). The rendering
rule is documented in [the renderer guide](../../ui/src/render/chart/README.md).
