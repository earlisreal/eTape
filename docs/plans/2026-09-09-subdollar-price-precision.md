# Sub-dollar Price Precision in DOM, T&S, and Chart Axis

Status: implemented

## Goal

For a symbol whose current/latest valid price is below `$1.00`, display prices
with exactly four decimal places in the DOM ladder, Time & Sales (T&S), and the
chart's native right price scale. At `$1.00` and above, retain a fixed
three-decimal display. This is a display-only UI change: source prices,
market-data eligibility, chart bars, orders, and WebSocket contracts remain
unchanged.

## Agreed behavior

| Current/latest valid price | Display precision | Notes |
|---|---:|---|
| `0 < price < 1` | 4 | Use ordinary `toFixed(4)` display rounding. |
| `price >= 1` | 3 | `$1.00` is deliberately in this branch. |
| missing, zero, negative, `NaN`, or non-finite | 3 | Safe initial-load/reconnect fallback. |

The selected precision is uniform within each surface. It is not derived from
each individual ladder level, tape row, historical bar, or visible axis tick.
This keeps columns and the chart scale stable as values vary around the
threshold.

### Price source by surface

- **DOM ladder:** its existing newest `LastTrade` from the tape ring.
- **T&S:** the symbol's O(1) `TapeRing.lastTick(symbol)` value, even while the
  tape is paused, scrolled, or filtered. A historical top row must not change
  a sub-dollar symbol back to three decimals.
- **Chart:** the latest valid raw bar close read by `ChartController.sync()`.
  Synthetic no-trade display bars must not become an independent precision
  source.

The selection recalculates on the surface's next normal update. There is no
new timer, subscription, React state path, setting, or persisted preference.

### Included and excluded UI

Included:

- DOM bid/ask rows, spread text, LULD boundary/fallback rows, and the ladder
  canvas's LULD accessibility text.
- All visible T&S row prices.
- The chart right-axis labels and the Lightweight Charts crosshair/last-value
  labels that use the main series' native price formatter.

Excluded:

- Chart legend, bar-close/countdown badge, context-menu Copy Price text, and
  drawing measurement labels.
- Order ticket, account, watchlist, stock-info, execution, and all other price
  displays.
- Engine aggregation, tick-size validation, trade-report eligibility, order
  validation, generated wire sources, and all provider data.

## Non-goals

- Do not replace the global `formatPrice()` signature or make its callers infer
  precision. It is intentionally used by unrelated panels.
- Do not repurpose `priceDecimals(prices)`: it scans a value set and has
  different semantics from the agreed current-price policy.
- Do not add symbol tick-size metadata, a market-data field, a configuration
  option, a feature flag, a dependency, or a new formatter class.
- Do not change any underlying numeric value, price rounding used for trading,
  or chart data. JavaScript display rounding only affects text.
- Do not extend the chart scope to non-axis text merely because it displays a
  price.

## Current-code evidence

- [`ui/src/render/format.ts`](../../ui/src/render/format.ts) owns the shared
  canvas formatter. `formatPrice()` is `toFixed(decimals)` and
  `QUOTE_DECIMALS` is the current fixed-three baseline. `priceDecimals()` is
  unused by production paths and is the wrong value-set policy for this work.
- [`ui/src/render/ladder/ladderState.ts`](../../ui/src/render/ladder/ladderState.ts)
  receives `last: LastTrade | null`, but hard-codes
  `decimals: QUOTE_DECIMALS`. Its painter already formats rows, fallback rows,
  and spread from `state.decimals`. `luldAccessibleText()` separately uses
  `toFixed(2)` and therefore needs the selected precision passed explicitly.
- [`ui/src/chrome/panels/LadderPanel.tsx`](../../ui/src/chrome/panels/LadderPanel.tsx)
  maintains the current `last` trade from the symbol-scoped tape ring, builds
  the ladder state, and owns the canvas `aria-label` update.
- [`ui/src/render/tape/tapeState.ts`](../../ui/src/render/tape/tapeState.ts)
  currently formats each row with `QUOTE_DECIMALS`. Its returned rows can be a
  paused historical window, so the renderer cannot use `rows[0]` as the
  current-symbol source.
- [`ui/src/chrome/panels/TapePanel.tsx`](../../ui/src/chrome/panels/TapePanel.tsx)
  already owns both paint and hover calls to `buildTapeRows()` and has access to
  `stores.tape.lastTick(symbol)` without a new data path.
- [`ui/src/render/chart/ChartController.ts`](../../ui/src/render/chart/ChartController.ts)
  reads raw bars in `sync()` and can apply options to its main `LwcSeries`.
  The facade already exposes `series.applyOptions()`.
- [`ui/src/render/chart/chartTheme.ts`](../../ui/src/render/chart/chartTheme.ts)
  configures visual series options but does not set a main-series `priceFormat`,
  leaving the chart axis to Lightweight Charts defaults today.

## Minimal design

### 1. Add one shared selector, not a global formatting change

In `ui/src/render/format.ts`, add a small exported helper such as
`quoteDecimals(price: number | null | undefined): number`:

```ts
return Number.isFinite(price) && price > 0 && price < 1 ? 4 : QUOTE_DECIMALS;
```

Keep `formatPrice(price, decimals)` unchanged. The helper is the single source
for the threshold, exact `$1.00` boundary, and invalid-data fallback. It does
not inspect tick size or a list of prices.

### 2. Wire the DOM ladder through its existing state

In `ui/src/render/ladder/ladderState.ts`:

1. Derive `decimals` once from `args.last?.price` when building
   `LadderPaintState`.
2. Keep `paintLadder.ts` unchanged: its ordinary rows, LULD fallback rows, and
   spread already consume `state.decimals` and will therefore update together.
3. Let `luldAccessibleText()` accept the selected precision while preserving
   its existing average-entry argument compatibility (for example, append an
   optional `decimals = QUOTE_DECIMALS` parameter). Format the LULD range via
   `formatPrice()` rather than a separate `toFixed(2)` call.

In `ui/src/chrome/panels/LadderPanel.tsx`, pass `paintState.decimals` into the
accessible-text call. Do not put live price data in React state or change the
imperative scheduler.

### 3. Keep T&S precision tied to the live symbol while paused

Extend `buildTapeRows()`'s display options with an optional `latestPrice` (or
an equivalently narrow explicit argument). In `TapePanel`, obtain it once from
`stores.tape.lastTick(symbolRef.current)?.price` and pass it to both:

- the scheduled paint call; and
- the hover row lookup.

`buildTapeRows()` selects its decimal count once from that value and uses it
for every returned row. Missing input resolves to the three-decimal fallback.
Do not scan the ring again, derive the policy from `view.anchorSeq`, or let the
minimum-size filter alter the selected precision.

### 4. Apply the chart-native price format from the controller

Keep chart precision inside `ChartController`; do not add a ChartPanel prop or
touch its separately scoped badge/legend formatter.

1. At mount, initialize the main series with the safe three-decimal price
   format.
2. During `sync()`, derive precision from the latest valid **raw** bar close,
   before synthetic display bars are considered. Apply a Lightweight Charts
   main-series option only when the selected precision changes:

   ```ts
   priceFormat: {
     type: "price",
     precision: decimals,
     minMove: decimals === 4 ? 0.0001 : 0.001,
   }
   ```

3. Cache the currently-applied decimal count so high-frequency chart syncs do
   not repeatedly call `applyOptions()` with the same value.
4. Reset the cache/series to three decimals on symbol/timeframe reload when
   data is absent, and apply the cached format when `setChartType()` recreates
   the main series. Theme updates should keep the existing price-format option.

This lets Lightweight Charts update only the right scale and its native
price-scale labels. It neither changes bars nor reaches the overlay timer,
legend, Copy Price command, or drawings.

## File-level implementation checklist

1. `ui/src/render/format.ts`
   - Add the current-price decimal selector using the existing
     `QUOTE_DECIMALS` fallback.
   - Leave `formatPrice`, `priceDecimals`, and unrelated callers intact.

2. `ui/src/render/format.test.ts`
   - Add table-driven coverage for sub-dollar, exactly `$1.00`, above-dollar,
     zero, negative, `NaN`, and absent inputs.

3. `ui/src/render/ladder/ladderState.ts`
   - Use the selector for the state decimal count.
   - Route LULD accessibility range text through that count without changing
     LULD projection, book handling, or order marks.

4. `ui/src/chrome/panels/LadderPanel.tsx`
   - Supply the state decimal count to `luldAccessibleText()` only.

5. `ui/src/render/ladder/ladderState.test.ts`
   - Prove sub-dollar state has four decimals and `$1.00`/no trade has three.
   - Capture canvas text for ordinary rows, spread, and LULD fallback rows.
   - Assert sub-dollar LULD accessibility text has four places, while existing
     above-dollar text remains three places after the policy change.

6. `ui/src/render/tape/tapeState.ts`
   - Accept the explicit live latest price and select one precision per result
     set.

7. `ui/src/chrome/panels/TapePanel.tsx`
   - Pass `lastTick(symbol)?.price` consistently to paint and hover row builds.
   - Preserve the current bounded ring, pause-anchor, filter, and imperative
     paint behavior.

8. `ui/src/render/tape/tapeState.test.ts` (and `TapePanel.test.tsx` only if
   its store fake needs the existing `lastTick()` method exposed)
   - Verify four fixed places for a sub-dollar live latest price.
   - Verify all rows use that one choice, including an older/above-dollar row.
   - Verify an anchored paused window still follows the newest live price,
     independent of the visible row and min-size filter.
   - Verify exactly `$1.00` and unavailable latest price produce three places.

9. `ui/src/render/chart/ChartController.ts`
   - Add the cached series-precision application described above.
   - Preserve it across mount, sync, no-data reset, and chart-type recreation.

10. `ui/src/render/chart/ChartController.test.ts`
    - Assert mount/no-data uses `{ precision: 3, minMove: 0.001 }`.
    - Assert a latest raw close below `$1` applies four decimal precision and
      `0.0001` min move.
    - Assert exact `$1.00`, a return above the threshold, and a symbol reset
      return to three decimals.
    - Assert unchanged syncs do not add redundant `applyOptions()` calls and a
      chart-type change recreates the main series with the active format.

No engine or generated-source file should change. No README change is expected
because this alters no flow, interface, dependency, invariant, or operational
procedure; reassess only if implementation exposes a user-facing precision
policy already documented elsewhere.

## Validation

Run focused tests while implementing:

```powershell
Set-Location ui
npx vitest run src/render/format.test.ts src/render/ladder/ladderState.test.ts src/render/tape/tapeState.test.ts src/render/chart/ChartController.test.ts
npm run typecheck
```

Because this is an approved plan, complete the repository's CI-equivalent
Windows checklist before handoff (the workflow remains authoritative): engine
tests, race tests, vet, pinned `golangci-lint`, `mingw32-make -C engine
gen-ts-check`, then `npm ci`, `npm run lint`, `npm test`, `npm run build`, and
`git diff --check`. Run `npm run e2e` as the proportional UI check if its local
browser prerequisites are available. Record every skipped check and why.

Manual smoke checks:

1. A live sub-dollar symbol shows four fixed digits in the ladder, T&S, axis,
   crosshair label, and native last-value label.
2. A transition through `$1.00` switches the named surfaces to three digits on
   their next normal update; no source price or bar value changes.
3. Initial load/reconnect with no valid latest price is stable at three digits
   and does not throw.
4. Pause T&S on older rows, then confirm its fixed column remains governed by
   the current live price rather than the paused row.
5. Confirm the chart legend, timer badge, Copy Price command, and non-scoped
   panels retain their existing formatting.

## Rollout, rollback, and risks

This is a UI-only, reversible change with no migration or data compatibility
work. Roll back with a scoped revert of the UI formatter wiring.

Primary risks are visual rather than data-related: four-digit axis labels can
widen the native price-scale gutter, and applying the wrong price source could
make paused T&S or synthetic chart gaps flicker. The one-source-per-surface
policy, cached chart option update, and focused transition/paused/no-data tests
are the safeguards. Underlying numeric values and order paths remain untouched.
