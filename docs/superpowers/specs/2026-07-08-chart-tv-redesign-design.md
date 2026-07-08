# Chart redesign: TradingView-faithful chart panel

**Date:** 2026-07-08
**Status:** Approved
**Supersedes:** the chart-chrome portions of `2026-07-03-ui-design.md` (chart panel spec) and the
chart re-inking of `2026-07-07-ui-redesign-design.md`. Chart *behavior* (data flow, bar
architecture, sessions, fills-on-chart, cold-symbol honesty) is unchanged.

## Context

The current chart chrome is minimal: a `<select>`-based toolbar row (`ChartControls`), a
glyph-based floating drawing rail (`DrawingRail`), a two-item right-click popover, and no
on-chart legend — indicator editing happens in toolbar chips above the canvas. Earl wants the
chart to look and behave like full TradingView: its toolbars, its on-chart legend with
per-indicator toggle/edit, its right-click context menu, its drawing toolbars.

**Feasibility (verified 2026-07-08 against LWC v5 docs):** Lightweight Charts v5.2.0 is a pure
rendering engine — it provides panes, primitives/plugins, crosshair/click subscriptions, series
markers, price lines, and `takeScreenshot()`, but ships **zero UI chrome**. Every TradingView
widget must be (and can be) built as custom DOM overlays around the canvas. Nothing in the
library blocks any element of this design. eTape already has early versions of three of the four
chrome elements.

## Decisions

1. **Pixel-faithful TradingView clone** — not a Daylight-Ledger reinterpretation. The chart panel
   is a deliberate visual island; fidelity to TV's current UI *is* the design.
2. **Scope: all four chrome elements** — top toolbar, on-chart legend + in-chart indicator
   editing, right-click context menu, drawing toolbars (rail upgrade + floating selection
   toolbar).
3. **Canvas included** — TV's chart background, grid, crosshair, candle colors, and study
   colors. The Daylight Ledger chart palette is retired for this panel.
4. **Ledger header stays** — app-level chrome (link-group swatch, symbol type-to-load, close ✕,
   dockview drag) is untouched; the TV toolbar renders below it inside the panel body.
5. **Functional items only** — no button ships without a working backend. TV features with no
   eTape counterpart (alerts, replay, undo/redo, object tree, comparison, Pine) do not appear.

## Token system (TV's, verbatim)

| Role | Light | Dark |
|---|---|---|
| Chart bg | `#FFFFFF` | `#131722` |
| Toolbar / dialog bg | `#FFFFFF` | `#1E222D` |
| Border / separator | `#E0E3EB` | `#2A2E39` |
| Text | `#131722` | `#D1D4DC` |
| Muted text | `#787B86` | `#787B86` |
| Hover fill | `#F0F3FA` | `#2A2E39` |
| Accent (active TF, selection, links) | `#2962FF` | `#2962FF` |
| Candle up / down | `#089981` / `#F23645` | same |
| Grid | `rgba(42,46,57,.06)` | `rgba(240,243,250,.06)` |

- Candle colors are TV's **current** defaults; the pre-2022 `#26A69A`/`#EF5350` pair is the noted
  alternative if the modern pair reads wrong on the warm app surround.
- **Font:** TV's stack — `-apple-system, "Trebuchet MS", Roboto, Ubuntu, sans-serif`; 12px UI,
  11px axis, tabular numerals. IBM Plex does not appear inside the chart panel.
- **Geometry:** 38px toolbar height, 28×28 icon buttons, 6px radius, 1px separators.
- **Icons:** ~20 hand-rolled inline SVGs matching TV's iconography in one `tvIcons.tsx`. No icon
  library dependency.
- **Indicator defaults** switch to TV study colors: EMA `#2962FF`, SMA `#FF6D00`, VWAP
  `#7E57C2`, MACD line `#2962FF` / signal `#FF6D00` / hist tinted `#089981`/`#F23645`.
- Exact hover/press/shadow values are matched to TV screenshots during implementation; the table
  above is authoritative for the named roles.

## Layout

```
┌─ ledger-header (kept: ⬤ swatch · AAPL · Chart · ✕) ────────────────┐
├─ TV top toolbar (38px) ────────────────────────────────────────────┤
│ [AAPL 🔍] │ 10s 1m 5m 15m 30m 60m D W M │ [🕯▾] │ [ƒx Indicators] │ ⋯ [📷] [⚙] │
├────────────┬───────────────────────────────────────────────────────┤
│ ▦ cursor   │ AAPL · 1m · eTape  O 187.42 H 187.60 L 187.15 C 187.33 −0.42% │
│ ─ lines ▸  │ Vol 1.24M                                             │
│ ▭ rect     │ VWAP  187.51        ← hover: [👁] [⚙] [✕]             │
│ ⤢ measure  │ EMA 9 close 187.38                                    │
│ ──────     │              (candles, TV white/#131722)              │
│ 🧲 magnet  │                                                       │
│ 👁 hide    │                                                       │
│ 🗑 clear   ├───────────────────────────────────────────────────────┤
│            │ MACD 12 26 9  ▓hist ─line ─signal   [👁][⚙][✕]        │
└────────────┴──────────────────────── (MACD pane) ──────────────────┘
```

## 1 · Top toolbar (replaces `ChartControls`)

Left→right:

- **Symbol button** — bold symbol + search icon; click opens the existing type-to-load flow
  inline in the toolbar (same command path as the ledger-header edit; no new symbol-search
  backend).
- Separator.
- **Nine timeframe text buttons** — `10s 1m 5m 15m 30m 60m D W M`, all visible (no dropdown
  needed at 9 items); active one in `#2962FF`.
- Separator.
- **Chart-type picker** — dropdown: Candles / Bars / Line / Area (all native LWC series types).
  Controller swaps the main series in place; persisted per panel in workspace config.
- **ƒx Indicators** button — opens a TV-style picker dialog: searchable list of the catalog's
  5 indicators (VWAP, EMA, SMA, MACD, VOLUME); click adds an instance.
- Right-aligned: **camera** — PNG export via LWC `takeScreenshot()`, downloads
  `{symbol}-{timeframe}.png`; **settings gear** — chart settings dialog with functional toggles
  only: session shading, grid, volume visibility, symbol watermark; timezone displayed as fixed
  "ET" (read-only).

## 2 · On-chart legend + in-chart indicator editing (new)

Per-pane top-left DOM overlay; pane vertical offsets computed from `chart.panes()` heights
(recomputed on pane resize).

- **Symbol row (pane 0):** `AAPL · 1m · eTape` + O H L C values tinted up/down + change%.
  Tracks the crosshair bar via `subscribeCrosshairMove`; falls back to the last bar when the
  crosshair leaves the chart.
- **Volume row (pane 0):** `Vol` + value, same crosshair tracking.
- **One row per indicator instance** (in its own pane): title + params in muted text
  (`EMA 9 close`, `MACD 12 26 9`), live value(s) in the slot color(s). Hover reveals three
  controls, exactly TV's:
  - **👁 visibility toggle** — new `hidden` flag on the indicator instance (persisted), mapped to
    LWC's `visible` series option. Today's system is add/remove only; hide keeps the
    subscription and data flowing, just stops painting.
  - **⚙ settings** — opens the indicator settings dialog.
  - **✕ remove** — unchanged semantics (unsubscribes).
- **Indicator settings dialog** — TV's exact structure: draggable modal, tabs **Inputs | Style**,
  footer `Defaults · Cancel · Ok`. Inputs tab = the catalog params (same validation/min/max);
  Style tab = per-slot color, line width, line style. Param changes re-subscribe; style changes
  apply in place (existing `updateIndicator` split). This replaces the toolbar chip inputs
  entirely.
- **Hard constraint:** legend *values* update at crosshair/bar frequency and must never flow
  through React state. Values are written imperatively (`textContent` on refs) from the
  crosshair handler and the existing rAF scheduler paint. React owns only row existence and
  hover state (low-rate config).

## 3 · Right-click context menu (replaces the two-item popover)

TV-styled menu (hover fills, icon column, section separators), functional items only:

- Reset chart view (`resetZoom`)
- Jump to live (`jumpToLive` — exists on the controller, currently unwired)
- Copy price `187.33` (clipboard; price from the right-click Y coordinate)
- ─
- Remove all drawings (keeps confirm semantics)
- Hide/show all drawings (new global flag, see §4)
- ─
- Settings… (opens the chart settings dialog)

When invoked **on a drawing** (hit-test at the right-click point): Clone / Delete / a style
shortcut row are prepended. Menu closes on outside click or Escape (not `onMouseLeave`).

## 4 · Drawing toolbars

**Left rail, TV-style** (replaces `DrawingRail` glyphs with SVG icon buttons):

- Cursor (select), **lines flyout group** — trendline / ray / hline / hray in a TV-style flyout;
  the group button shows the last-used tool with TV's corner-arrow affordance — rect, measure.
- Separator, then: magnet toggle, **hide-all-drawings eye** (new — `DrawingsPrimitive` skips
  rendering; flag persisted per panel), trash (keeps the existing confirm popover).
- Same interaction contract as today: `data-drawing-rail` opt-out, `stopPropagation`, tool
  reverts to select after commit, Escape cancels.

**Floating toolbar on selection** (new): appears above the selected drawing (repositioned on
pan/zoom), TV-style pill with:

- Color swatch → palette popover
- Line width (1–4)
- Line style (solid / dashed / dotted)
- Clone, Delete

Requires per-drawing style fields on the model: `color?`, `width?`, `lineStyle?` with defaults in
`drawings/model.ts`, accepted by `validateDrawings`, rendered by `DrawingsPrimitive`, persisted
through the existing store/BroadcastChannel/KV path. Today all drawings share the accent color.

## 5 · Canvas theming + architecture

- **TV themes as `Palette` objects** — `TV_LIGHT` / `TV_DARK` satisfying the existing `Palette`
  interface (it already covers surfaces, candles, volume, grid, crosshair, sessions, indicator
  colors, fills). Selected only by the chart panel, following app light/dark mode.
  `chartTheme.ts`, `ChartController`, and all primitives (diamonds, session shading, drawings)
  re-theme untouched — this is the low-risk path the facade architecture was built for.
- **Session shading stays** (load-bearing for pre-market trading) but re-tinted to TV-subtle
  values inside `TV_LIGHT`/`TV_DARK`.
- **Fill diamonds stay**, tinted from the TV palette's buy/sell fills.
- New files: `ui/src/render/chart/tvTheme.ts` (TV palettes + font stack + geometry constants),
  `ui/src/chrome/panels/tv/tvIcons.tsx`, `TVToolbar.tsx`, `TVLegend.tsx`, `TVContextMenu.tsx`,
  `TVDrawingRail.tsx`, `TVFloatingToolbar.tsx`, `TVDialog.tsx` (shared draggable dialog shell),
  plus the indicator-picker / indicator-settings / chart-settings dialogs on that shell.
- Replaced/retired: `ChartControls.tsx`, `DrawingRail.tsx`, the inline context-menu popover in
  `ChartPanel.tsx`.
- **`ChartController` deltas:** chart-type swap (recreate main series via facade), indicator
  `hidden` mapping, per-drawing styles pass-through, hide-all-drawings flag, `jumpToLive` wiring.
- **Persistence deltas** (workspace config): chart type, indicator `hidden` + per-slot styles,
  hide-all-drawings, chart settings toggles; per-drawing styles ride the existing
  `drawings.<symbol>` KV entries.

## Out of scope

Alerts, bar replay, undo/redo, object tree, symbol comparison/multi-series, Pine Script, TV's
full symbol-search modal (type-to-load is the symbol entry), price-scale/time-scale context
menus, drawing lock, favorites/star system. Nothing fake gets a button.

## Testing

Mirror the existing structure: unit tests beside each new component
(`TVLegend.test.tsx`, `tvTheme.test.ts`, dialog tests with the fake facade), `ChartController`
deltas covered in `ChartController.test.ts` (chart-type swap, hidden mapping), drawing-style
model/geometry/primitive tests beside the drawings suite. Interaction checks (legend hover
controls, context-menu-on-drawing, floating toolbar reposition) via the existing
mocked-`lightweight-charts` panel-test pattern. Visual fidelity is checked manually against
TradingView screenshots in both themes at the end.
