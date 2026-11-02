# Volume Overlap

Status: approved; Earl confirmed shared understanding on 2026-11-03.
Implemented on 2026-11-03.

## Goal

Add a settings option to let price use the space currently reserved for the
Volume Indicator, matching the overlapping layout in Earl's reference image.
Keep this the existing Volume Indicator, distinct from Volume Profile. See the
[glossary](../../CONTEXT.md#chart-viewport).

## Design decisions

- The application-wide Volume Overlap default starts off. Each Chart Panel can
  follow that default or explicitly override it on or off.
- Keep the histogram within the bottom 25% of the main price pane. Price may
  extend through that area when overlap is enabled, with 8% bottom padding below
  price to keep the lowest wick clear of the floor; price paints above volume.
- Use fixed translucency in overlap mode. Add no height or opacity controls.
- Cap rendered histogram opacity at 50% in overlap mode, including custom
  colors. Preserve already lower opacity and saved colors; disabling overlap
  restores the original rendering. Legend text retains its current styling.
- Support candlesticks, OHLC bars, line and area charts with the same setting.
- Changes to the application-wide default immediately update charts following
  it across open workspaces. Explicit chart overrides retain their on/off
  choice. Use the existing cross-workspace configuration sync mechanism.
- Layout export/import preserves inheritance and explicit overrides. Charts
  following the app default use the destination application's default after
  import; layout exports do not include the global default.

## Current-code evidence

- [ChartController](../../ui/src/render/chart/ChartController.ts) already creates
  the local Volume histogram in price pane 0 on its own invisible scale.
  `setVolumeGeometry` is the shared seam for the price and volume margins.
- [chartTheme](../../ui/src/render/chart/chartTheme.ts) sets visible-volume price
  margins to `{ top: 0.08, bottom: 0.25 }`, ordinary price margins to
  `{ top: 0.08, bottom: 0 }`, and volume margins to
  `{ top: 0.75, bottom: 0 }`. `liftCandleToTop` already puts price above volume.
- [Chart Settings](../../ui/src/chrome/panels/tv/ChartSettingsDialog.tsx) contains
  per-panel boolean preferences and applies drafts only on Ok.
  `normalizeChartSettings` handles older settings; the rollback projection
  preserves the legacy `volume: false` field.
- [ChartPanel](../../ui/src/chrome/panels/ChartPanel.tsx) applies settings at
  controller creation and through `applyChartSettings`, then persists them.
- [TV palettes](../../ui/src/render/chart/tvTheme.ts) use 50% opacity for default
  up/down Volume colors in both themes. Custom histogram colors are returned
  unchanged by [volumeColorFor](../../ui/src/render/chart/indicatorSeries.ts)
  and can be opaque.
- [Layout export](../../ui/src/chrome/backup.ts) retains panel settings except
  symbol, so chart overrides can travel through the existing export format.
  Its envelope contains layout and/or hotkeys, excluding global preferences.
- Supported stored price styles are candle, bar, line and area. The controller
  already reapplies volume geometry after changing the main series.
- [ThemeProvider](../../ui/src/chrome/ThemeProvider.tsx) and
  [SoundConfigProvider](../../ui/src/sound/SoundConfigProvider.tsx) load/save
  global values with generic GetConfig/SetConfig commands.
  [Config sync](../../ui/src/configSync.ts) notifies other same-origin workspace
  windows to re-read accepted writes using BroadcastChannel.
- [General settings](../../ui/src/chrome/GeneralSection.tsx) groups global
  controls that save immediately. No general chart-preferences group or existing
  per-panel inheritance selector is present.
- Chart Settings rollback projection spreads raw settings before normalized
  settings. Resetting to inheritance must explicitly remove the old raw
  `volumeOverlap` override, rather than merely omit it from the new object.
- Installed Lightweight Charts 5.2.0 exposes series order and scale margins but
  no public histogram-opacity option or color-alpha utility. Custom colors
  currently accept nonempty CSS color strings rather than just hex swatches.

## Controls and behavior

- Settings -> General -> Charts -> Overlap volume: application-wide checkbox,
  off by default, saved immediately like neighboring General controls.
- Chart settings -> Overlap volume: Follow app default (showing its current
  On/Off value), On or Off. New and existing charts start with Follow app
  default. Chart settings apply on Ok; cancel discards the draft.
- Turning overlap on lets price use the lower plot area with 8% bottom padding
  while volume retains its bottom-quarter scale and paints behind price. Turning it off restores
  the existing reserved-volume layout.
- Volume visibility remains controlled by the existing indicator controls.
  Hiding or removing Volume reclaims the lower area in either mode and keeps
  the saved preference. Showing or re-adding it uses that preference.
- Returning a chart to Follow app default removes its override. Subsequent
  global changes affect that chart immediately, including after reload.
- Applying the preference preserves the time viewport and existing autoscale
  choice. The price axis, crosshair, drawings and trading overlays continue to
  use stock-price coordinates throughout the shared plot area.

No new price-style selector, height control or opacity control is included.

## Implementation

1. Add a narrow `ChartPreferencesProvider` with config key `chartPreferences`
   and `{ volumeOverlap: false }`, following existing GetConfig/SetConfig and
   config-sync patterns. Mount it in [App](../../ui/src/App.tsx) and shared
   [test providers](../../ui/src/test/providers.tsx), plus isolated chart test
   harnesses as needed. Add a Charts group in General settings using the same
   immediate-save behavior as its neighbors. No generic preference framework
   or reuse of unrelated theme/sound fields is needed.
2. Add optional per-chart `volumeOverlap?: boolean` and a three-choice control
   separate from the existing boolean-only toggle list. Explicit true/false
   overrides; absence follows the global choice. Normalize malformed overrides
   to inheritance. Preserve Chart Settings Ok/cancel behavior and its legacy
   rollback projection. Returning to Follow app default must remove a previous
   raw override. Do not populate false overrides in presets or legacy charts.
3. Resolve the effective preference and pass it to the controller during
   initialization and settings application. Preserve inheritance versus an
   explicit choice through persistence and export/import; do not eagerly save
   the resolved global value into every chart. Update mounted inheriting
   controllers when the global value loads or changes without
   recreating charts, fetching bars or saving panel overrides.
4. Extend the controller's central volume-geometry logic: visible Volume with
   overlap off retains the current reserved band; overlap on uses 8% top and
   bottom price margins with the existing volume margins. Hidden or removed
   Volume uses ordinary price margins regardless of the saved overlap preference.
5. Keep the preference when Volume is hidden or removed; toggling the layout
   option does not add, show or remove an indicator. Showing or re-adding Volume
   uses the saved layout choice. Reuse the same series, bars and legend values.
6. Add a small browser-normalized render-color resolver to cap overlap alpha at
   `min(originalAlpha, 0.5)`, preserving saved CSS colors and hue. Resolve/cache
   on settings/style/palette changes rather than parsing through the DOM per
   tick or per bar; avoid implementing a general CSS color parser. Preserve
   normal rendering when overlap is off and keep legend text styling unchanged.
   Reseed displayed volume when its effective color changes so existing and
   incoming bars agree. Cover opaque hex, rgb/rgba, transparent and named colors
   supported by the chart library, including already lower alpha.
7. Retain the preference through palette changes, chart-type changes,
   symbol/timeframe changes and reload. Applying the option must not reset the
   time viewport or force price autoscale on. Existing overlays must reproject
   to the resulting price coordinates through the normal render path.
8. Update the [renderer guide](../../ui/src/render/chart/README.md#volume-indicator)
   and [chart UI guide](../../ui/src/chrome/panels/tv/README.md) when implemented;
   update [UI guidance](../../ui/README.md) for global settings, chart overrides,
   propagation and layout portability. Add the plan to the implemented index
   when the feature is completed.

No engine, WebSocket contract, market-data subscription, new dependency,
indicator-model migration, Volume Profile or order-execution change is expected.
The reference image's other studies are outside this feature.

## Validation plan

- Settings tests: global off default, explicit on/off overrides versus
  inheritance, strict normalization, Ok applies, cancel discards,
  persistence/reload and existing rollback compatibility. Assert that returning
  to inheritance actually removes the persisted raw override.
- Provider/General tests: missing/invalid global values, initial load, save,
  accepted-write notification, cross-workspace re-read and cleanup. Global
  updates immediately affect inheriting charts without rewriting panel settings
  or changing explicit overrides.
- Controller tests: reserved versus overlapping margins; hidden/removed and
  shown/re-added Volume; preference across theme/type/reload paths; no new
  subscription or viewport reset; effective colors after toggles and updates.
- Panel tests: initialize and apply the effective preference, keep explicit
  overrides independent, and preserve inheritance/overrides through the existing
  layout format. Extend the structural-export/round-trip cases in
  [backup tests](../../ui/src/chrome/backup.test.ts) for absent/true/false chart
  overrides. No export version bump is expected.
- Run focused affected UI tests, typecheck, applicable lint and build. Add a
  browser regression for real chart geometry and persistence if existing
  controller/component coverage cannot verify the behavior.
- Visually verify both themes with large volume spikes and low candles;
  existing crosshair, drawing, order, alert and Risk Entry overlays; all supported
  price styles; hidden Volume; and symbol/timeframe/reload transitions.
  Use simulated data without live broker operations.
- Validate Markdown links and run `git diff --check` for planning documents.
  Engine/UI checks and hosted CI are not required for prose-only plan changes.
- When implemented, follow [AGENTS.md](../../AGENTS.md#validation): focused local
  checks, scoped commit, main integration/push, and successful full hosted CI on
  the pushed main commit before handoff.

Focused test commands will use the actual Vitest project routing in
[vitest.config.ts](../../ui/vitest.config.ts), for example from `ui`:

```powershell
npx vitest run --project chart-core src/render/chart/ChartController.test.ts src/render/chart/chartTheme.test.ts
npx vitest run --project chart-panel src/chrome/panels/ChartPanel.test.tsx src/chrome/panels/tv/ChartSettingsDialog.test.tsx
npx vitest run --project chrome-regressions src/chrome/GeneralSection.test.tsx src/chrome/backup.test.ts
npx vitest run src/chrome/ChartPreferencesProvider.test.tsx
npm run typecheck
npm run lint
npm run build
```

Include new color-resolver tests in their configured project. `npm test` runs
the full UI suite; its custom runner does not focus on forwarded filename args.

## Rollout, rollback and risks

A missing global preference keeps the current layout; a missing chart override
follows the global preference. Reverting the UI ignores the new preferences
and retains the existing Volume Indicator and saved color.
No runtime-data rewrite is needed.

The main risks are opaque custom colors, losing the preference on controller
recreation, changing view state while applying it, and confusing overlap with
Volume visibility. Central geometry/color handling, lifecycle tests and real
chart inspection address those risks. Cross-window refresh uses the existing
same-origin BroadcastChannel mechanism; when unavailable, another open window
sees the saved default on reload. This reversible presentation choice does not
warrant a new ADR.
