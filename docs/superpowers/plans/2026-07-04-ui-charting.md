# eTape UI — Plan 2 of 6: Charting

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Chart panel to both workspaces — TradingView Lightweight Charts v5 (unforked) rendering candles + volume + a MACD sub-pane, per-chart indicator instances streamed as series, a custom diamond fill-marker plugin, ET session shading, cold-symbol / in-progress-bar handling, and wickplot interaction conventions — all driven from the existing `BarStore` through the rAF scheduler, following link groups, with light-default / dark theming from a new single-source-of-truth palette.

**Architecture:** Lightweight Charts v5 owns *all* candlestick viewport math (pan, zoom, price/time axes, crosshair, auto-follow) — we do **not** port wickplot's `BarWindow`/`ChartViewport`/`priceGrid`/`niceAxisStep` (YAGNI: LWC already does that job). The chart's wiring logic lives in a pure, dependency-injected `ChartController` (render layer) that talks to LWC through a minimal `ChartApiFacade` interface, so it is unit-tested with a fake facade — no headless chart rendering. `ChartPanel` (chrome) creates the real LWC chart, adapts it to the facade, hands it to the controller, registers a `Surface` with the scheduler (which calls `controller.sync()` once per dirty frame), and wires ResizeObserver + link-group symbol following + timeframe/indicator config. A new `palette.ts` (render layer) is the single color source of truth: the LWC theme, every custom painter/primitive, and all chrome derive from it; painters receive the palette in their paint state so it is never read from a global.

**Tech Stack:** Adds `lightweight-charts@^5.2.0` (runtime). TypeScript 5, React 18, Vite 5, Vitest (unit + jsdom). No new test tooling — golden-image (node-canvas) + Playwright still arrive in Plans 3 and 6; Plan 2 verifies via pure unit tests, fake-facade controller tests, and a documented dev-app checklist.

## Global Constraints

Inherited verbatim from Plan 1 (`docs/superpowers/plans/2026-07-04-ui-foundation-data-plane.md` §Global Constraints) — every task implicitly includes these. Restated here are the ones Plan 2 touches most, plus Plan-2-specific additions.

- **Hard rule:** high-frequency data (chart bars/indicators) never flows through React state. The chart is an LWC instance mounted once via a ref and driven imperatively from a `Surface.paint()` the scheduler calls once per dirty frame. React renders chrome only (panel header controls, theme toggle). (stack-decision, ui-design §Architecture)
- **Dependency direction:** `chrome → render → data → wire`, never backwards. `render/chart/*` may import `data/*` and the external `lightweight-charts` lib and `render/palette.ts`; it must never import from `chrome/*`. `ChartController` depends on the `ChartApiFacade` interface, not on `chrome`. (ui-design §Architecture)
- **Honesty policy:** never render stale as live; never render in-progress as done. The in-progress bar is the last bar updating in place — never drawn as a "closed" bar. A quiet symbol holding a partial bar past its wall-clock end is **not** staleness; staleness is judged from health topics only. Cold sub-minute symbols show bars growing from subscribe time with an explanatory hint, not an empty error. (ui-design §Error handling, §Chart panel spec)
- **Wire format:** WebSocket + JSON; each topic delivers a full snapshot on subscribe, then deltas. The UI requests logical topics and never reasons about moomoo quota. Indicator instances are requested via a **command** (`SubscribeIndicator`, correlation-ID'd), and their series arrive on the `md.indicator` topic keyed by `instanceId`. (ui-design §Engine↔UI contract, portfolio-orders-design §Commands)
- **Type source of truth:** `ui/src/wire/contract.ts` is the interim hand-authored contract; keep every field name identical to the specs so the future tygo `ui/src/gen/*` is a drop-in. Plan 2 adds no breaking contract change — it consumes `md.bars` / `md.indicator` as already declared. (go-engine-design §uihub)
- **Charting scope (verbatim from the Plan-1 roadmap, item 2):** LWC v5 unforked (≥5.2.0) owns all viewport math; do not port wickplot viewport/axis classes; anything drawn on the chart plugs in as an LWC v5 primitive and asks LWC for pixels (`priceToCoordinate`/`timeToCoordinate`). Scope: candles + volume + MACD in a native v5 sub-pane; custom diamond fill-marker plugin ported from `~/Projects/earlisreal-lightweight-charts` commit `069fa855` (`drawDiamond`/`hitTestDiamond`, Manhattan hit test, 0.8 size factor) + the v3.7.1 `borderWidth` pattern (`a25e7dc0`); a bar-bucketing test-mirror of the engine; indicator instances (VWAP/EMA/SMA/MACD/volume/buy-sell delta) as streamed series; ET session shading; wickplot interaction *conventions* (crosshair snap, cursor-anchored wheel zoom, drag pan, jump-to-live) mapped onto LWC options; cold-symbol / in-progress-bar states.
- **Palette (cross-cutting, decided 2026-07-04):** a **new eTape palette**, explicitly *not* seeded from wickplot's `ChartColors`. Two variants; **light is the app default**, dark selectable via a settings toggle persisted in the config store (config key `theme`; per-workspace theming is out of v1 scope). It lands as `ui/src/render/palette.ts`, the single color source of truth. The LWC chart theme derives from it, every custom painter/primitive consumes it (received in paint state, never read from a global), and Plan 1's inline hex placeholders are swept onto the light-default palette as this plan's final step. Plans 3–5 take their colors exclusively from this module.
- **ET timezone:** all session math is US Eastern (pre-market 04:00, RTH 09:30–16:00, post 16:00–20:00 ET); intraday aggregation is anchored 09:30 ET. US stocks only. (CLAUDE.md scope, ui-design §Chart panel spec)

---

## File Structure (Plan 2)

```
ui/
  package.json                          MODIFY — add "lightweight-charts": "^5.2.0"
  src/
    render/
      palette.ts                        NEW    — Palette interface + LIGHT/DARK + getPalette(mode)
      palette.test.ts                   NEW
    render/chart/
      barBucket.ts                      NEW    — pure engine-mirror bucketing (session-anchored)
      barBucket.test.ts                 NEW
      sessions.ts                       NEW    — pure ET session bands over a time range
      sessions.test.ts                  NEW
      diamondMarker.ts                  NEW    — pure: drawDiamondPath, hitTestDiamond, FillMarker
      diamondMarker.test.ts             NEW
      diamondPrimitive.ts               NEW    — LWC ISeriesPrimitive drawing diamond fills
      sessionPrimitive.ts               NEW    — LWC IPanePrimitive drawing session bands
      chartTheme.ts                     NEW    — palette → LWC ChartOptions / series options
      chartTheme.test.ts                NEW
      indicatorSeries.ts                NEW    — pure: IndicatorInstance → series descriptor
      indicatorSeries.test.ts           NEW
      ChartApiFacade.ts                 NEW    — minimal interface over the LWC surface we use
      ChartController.ts                NEW    — pure controller; drives the facade from stores
      ChartController.test.ts           NEW
    data/
      IndicatorStore.ts                 NEW    — series values per indicator instanceId
      IndicatorStore.test.ts            NEW
      registry.ts                       MODIFY — add indicators store + route md.indicator
      registry.test.ts                  MODIFY — assert md.indicator routes to IndicatorStore
    chrome/
      ThemeProvider.tsx                 NEW    — palette context; loads/persists theme mode
      ThemeProvider.test.tsx            NEW
      WorkspaceHeader.tsx               NEW    — symbol-focus box per group + theme toggle
      WorkspaceHeader.test.tsx          NEW
      PanelFrame.tsx                    MODIFY — swatch/header colors from palette; extend PanelProps threading
      AppShell.tsx                      MODIFY — render WorkspaceHeader; dockview theme class from mode
      ReconnectOverlay.tsx              MODIFY — colors from palette
      panels/
        ChartPanel.tsx                  NEW    — LWC mount + controller + Surface + link-following + config persistence
        ChartControls.tsx               NEW    — per-chart timeframe + indicator manager (add/remove/params/colors)
        ChartPanel.test.tsx             NEW
        SmokePainterPanel.tsx           MODIFY — colors from palette (sweep)
        registry.tsx                    MODIFY — register "chart"; extend PanelProps with linkGroups + commands
    App.tsx                             MODIFY — pass linkGroups + commands into the shell; wrap in ThemeProvider
  fixtures/
    chart-session.json                  NEW    — md.bars backfill + in-progress + finalize + gap; md.indicator VWAP
  mock-engine/
    run.ts                              MODIFY — allow selecting the chart fixture for the dev app
```

**Design note — why a `ChartController` + `ChartApiFacade`:** the codebase's house style is dependency injection for everything hard to test (`WsClient` takes `socketFactory`, `Scheduler` takes `RafLike`, `LinkGroups` takes `LinkBus`). LWC cannot be meaningfully rendered in jsdom without brittle canvas shims, and its correctness is TradingView's job, not ours. So all *our* logic — when to `setData` vs `update`, in-progress bar handling, indicator add/remove, symbol/timeframe switching, auto-follow — lives in `ChartController`, tested against a `FakeChartApi`. `ChartPanel` is the thin, mostly-untested adapter that builds a real LWC chart, wraps it in the facade, and forwards resize/link/config events.

---

## Task 1: eTape palette module (light default + dark)

**Files:**
- Create: `ui/src/render/palette.ts`
- Test: `ui/src/render/palette.test.ts`

**Interfaces:**
- Produces: `interface Palette` (the exact keys below — every later task references them), `type ThemeMode = "light" | "dark"`, `const LIGHT: Palette`, `const DARK: Palette`, `function getPalette(mode: ThemeMode): Palette`.

> **Approved 2026-07-04 (Earl, via the palette preview).** The "precision instrument" direction: a teal/rose semantic pair (colorblind separation + long-session comfort), a brass accent (financial heritage; never collides with green/red), and a session temperature arc (cool pre-market → clear RTH → warm post). The type layer was captured alongside the colors — IBM Plex Mono for all data surfaces, IBM Plex Sans for chrome (`FONTS` below). Values are final for v1 — implement verbatim.

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/render/palette.test.ts
import { describe, it, expect } from "vitest";
import { LIGHT, DARK, getPalette, type Palette } from "./palette";

const KEYS: (keyof Palette)[] = [
  "bg", "surface", "border", "text", "textMuted", "grid", "crosshair",
  "up", "down", "volUp", "volDown",
  "buyFill", "sellFill", "fillOutline",
  "sessionPre", "sessionRth", "sessionPost", "sessionClosed",
  "indVwap", "indEma", "indSma", "indMacdLine", "indMacdSignal", "indMacdHist",
  "linkRed", "linkGreen", "linkBlue", "linkYellow",
  "accent", "ok", "warn", "danger",
];

describe("palette", () => {
  it("both variants define every key with a non-empty string", () => {
    for (const p of [LIGHT, DARK]) {
      for (const k of KEYS) {
        expect(typeof p[k], `${k}`).toBe("string");
        expect(p[k].length, `${k}`).toBeGreaterThan(0);
      }
    }
  });

  it("light and dark differ on the core surfaces", () => {
    expect(LIGHT.bg).not.toBe(DARK.bg);
    expect(LIGHT.text).not.toBe(DARK.text);
  });

  it("getPalette selects by mode", () => {
    expect(getPalette("light")).toBe(LIGHT);
    expect(getPalette("dark")).toBe(DARK);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/palette.test.ts`
Expected: FAIL — `Cannot find module './palette'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/palette.ts
// The single color source of truth for eTape. The LWC chart theme, every custom
// painter/primitive, and all chrome derive from this. Painters receive a Palette
// in their paint state — never read one from a global. Light is the app default.
export type ThemeMode = "light" | "dark";

export interface Palette {
  // surfaces / structure
  bg: string;          // panel + chart background
  surface: string;     // headers, controls
  border: string;      // hairlines
  text: string;        // primary text
  textMuted: string;   // secondary text, axis labels
  grid: string;        // chart grid lines
  crosshair: string;   // crosshair line
  // candles + volume
  up: string;          // bullish candle body/wick/border
  down: string;        // bearish candle body/wick/border
  volUp: string;       // up-bar volume (rgba, semi-transparent)
  volDown: string;     // down-bar volume (rgba, semi-transparent)
  // fills-on-chart (diamond markers)
  buyFill: string;     // buy diamond fill (soft green)
  sellFill: string;    // sell diamond fill (soft pink)
  fillOutline: string; // thin dark outline on both
  // ET session shading (rgba, low alpha — drawn behind bars)
  sessionPre: string;
  sessionRth: string;  // usually transparent
  sessionPost: string;
  sessionClosed: string;
  // indicator default colors
  indVwap: string;
  indEma: string;
  indSma: string;
  indMacdLine: string;
  indMacdSignal: string;
  indMacdHist: string;
  // link-group swatches
  linkRed: string;
  linkGreen: string;
  linkBlue: string;
  linkYellow: string;
  // status
  accent: string;
  ok: string;
  warn: string;
  danger: string;
}

export const LIGHT: Palette = {
  bg: "#FBFCFD",
  surface: "#EEF1F4",
  border: "#DCE1E7",
  text: "#10151C",
  textMuted: "#5A6672",
  grid: "#E8ECF0",
  crosshair: "#9AA6B2",
  up: "#17A67C",
  down: "#E0526E",
  volUp: "rgba(23,166,124,0.38)",
  volDown: "rgba(224,82,110,0.38)",
  buyFill: "#4CC79E",
  sellFill: "#F58DA1",
  fillOutline: "#10151C",
  sessionPre: "rgba(92,120,160,0.07)",
  sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(198,150,64,0.08)",
  sessionClosed: "rgba(40,50,65,0.05)",
  indVwap: "#6E56CF",
  indEma: "#C0872E",
  indSma: "#3E7CB1",
  indMacdLine: "#3E7CB1",
  indMacdSignal: "#E0526E",
  indMacdHist: "#8A97A6",
  linkRed: "#DB4C56",
  linkGreen: "#1FA97F",
  linkBlue: "#3E7CB1",
  linkYellow: "#CF9A2B",
  accent: "#C0872E",
  ok: "#17A67C",
  warn: "#C0872E",
  danger: "#D93A49",
};

export const DARK: Palette = {
  bg: "#0E1116",
  surface: "#161B22",
  border: "#262D38",
  text: "#DCE3EC",
  textMuted: "#7A8794",
  grid: "#1C222C",
  crosshair: "#55616F",
  up: "#2BB894",
  down: "#F0647E",
  volUp: "rgba(43,184,148,0.34)",
  volDown: "rgba(240,100,126,0.34)",
  buyFill: "#35C79E",
  sellFill: "#F98AA3",
  fillOutline: "#05070A",
  sessionPre: "rgba(120,150,190,0.12)",
  sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(200,155,70,0.10)",
  sessionClosed: "rgba(255,255,255,0.03)",
  indVwap: "#9A86FF",
  indEma: "#E0A64B",
  indSma: "#6BA8D8",
  indMacdLine: "#6BA8D8",
  indMacdSignal: "#F0647E",
  indMacdHist: "#55616F",
  linkRed: "#F0555F",
  linkGreen: "#2BB894",
  linkBlue: "#5AA0D8",
  linkYellow: "#E0B23E",
  accent: "#E0A64B",
  ok: "#2BB894",
  warn: "#E0A64B",
  danger: "#F0555F",
};

export function getPalette(mode: ThemeMode): Palette {
  return mode === "dark" ? DARK : LIGHT;
}

// Type layer — part of the visual identity, kept in the single source of truth.
// Canvas painters put FONTS.mono in their ctx.font strings; chrome uses the CSS
// vars set from these in Task 10. Both faces are the IBM Plex super-family.
export const FONTS = {
  mono: '"IBM Plex Mono", ui-monospace, monospace', // data surfaces: tape, ladder, prices, axes
  sans: '"IBM Plex Sans", system-ui, sans-serif',   // chrome: labels, menus, buttons
} as const;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/palette.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/palette.ts ui/src/render/palette.test.ts
git commit -m "feat(ui/render): eTape palette (light default + dark), single color source of truth"
```

---

## Task 2: Chart theme mapping (palette → LWC options)

**Files:**
- Create: `ui/src/render/chart/chartTheme.ts`
- Test: `ui/src/render/chart/chartTheme.test.ts`

**Interfaces:**
- Consumes: `Palette` (Task 1).
- Produces: `chartOptions(p: Palette): DeepChartOptions`, `candleOptions(p: Palette): CandleOpts`, `volumeOptions(p: Palette): HistogramOpts`. These are plain objects (typed loosely so the module does not hard-depend on LWC's exported option types, keeping it unit-testable and drop-in against v5.2's actual `ChartOptions`); `ChartPanel` passes them straight to `createChart` / `addSeries`.

> **LWC v5 API note:** v5 creates series via `chart.addSeries(CandlestickSeries, options, paneIndex?)` (not the v4 `addCandlestickSeries`). Crosshair modes come from `CrosshairMode`. Confirm exact export names against `node_modules/lightweight-charts` types after Task 9's install; the option *shapes* below are stable across v5.x.

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/render/chart/chartTheme.test.ts
import { describe, it, expect } from "vitest";
import { LIGHT, DARK } from "../palette";
import { chartOptions, candleOptions, volumeOptions } from "./chartTheme";

describe("chartTheme", () => {
  it("maps palette surfaces onto chart layout + grid", () => {
    const o = chartOptions(LIGHT);
    expect(o.layout?.background).toEqual({ type: "solid", color: LIGHT.bg });
    expect(o.layout?.textColor).toBe(LIGHT.text);
    expect(o.grid?.vertLines?.color).toBe(LIGHT.grid);
    expect(o.grid?.horzLines?.color).toBe(LIGHT.grid);
    expect(o.crosshair?.vertLine?.color).toBe(LIGHT.crosshair);
  });

  it("candles use up/down palette colors for body, wick and border", () => {
    const c = candleOptions(DARK);
    expect(c.upColor).toBe(DARK.up);
    expect(c.downColor).toBe(DARK.down);
    expect(c.wickUpColor).toBe(DARK.up);
    expect(c.wickDownColor).toBe(DARK.down);
    expect(c.borderUpColor).toBe(DARK.up);
    expect(c.borderDownColor).toBe(DARK.down);
  });

  it("volume histogram is priceScaleId '' (overlay) and colored per palette", () => {
    const v = volumeOptions(LIGHT);
    expect(v.priceScaleId).toBe("");
    expect(v.priceFormat?.type).toBe("volume");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/chartTheme.test.ts`
Expected: FAIL — `Cannot find module './chartTheme'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/chart/chartTheme.ts
import type { Palette } from "../palette";

// Loose structural types — these mirror the subset of LWC v5 ChartOptions /
// series options we set, without importing LWC's option types (keeps the module
// pure + trivially testable). ChartPanel passes them to createChart/addSeries as-is.
export interface DeepChartOptions {
  layout?: { background?: { type: "solid"; color: string }; textColor?: string };
  grid?: { vertLines?: { color: string }; horzLines?: { color: string } };
  crosshair?: { mode?: number; vertLine?: { color: string }; horzLine?: { color: string } };
  rightPriceScale?: { borderColor: string };
  timeScale?: { borderColor: string; rightOffset: number; secondsVisible: boolean; timeVisible: boolean };
  autoSize?: boolean;
}
export interface CandleOpts {
  upColor: string; downColor: string;
  wickUpColor: string; wickDownColor: string;
  borderUpColor: string; borderDownColor: string;
  borderVisible: boolean;
}
export interface HistogramOpts {
  priceScaleId: string;
  priceFormat: { type: "volume" };
  color?: string;
}

// CrosshairMode.Magnet === 1 in LWC — crosshair snaps to the nearest bar
// vertically while floating horizontally (the wickplot convention).
const CROSSHAIR_MAGNET = 1;

export function chartOptions(p: Palette): DeepChartOptions {
  return {
    layout: { background: { type: "solid", color: p.bg }, textColor: p.text },
    grid: { vertLines: { color: p.grid }, horzLines: { color: p.grid } },
    crosshair: {
      mode: CROSSHAIR_MAGNET,
      vertLine: { color: p.crosshair },
      horzLine: { color: p.crosshair },
    },
    rightPriceScale: { borderColor: p.border },
    timeScale: { borderColor: p.border, rightOffset: 5, secondsVisible: true, timeVisible: true },
    autoSize: false, // we drive resize via ResizeObserver → controller.resize()
  };
}

export function candleOptions(p: Palette): CandleOpts {
  return {
    upColor: p.up, downColor: p.down,
    wickUpColor: p.up, wickDownColor: p.down,
    borderUpColor: p.up, borderDownColor: p.down,
    borderVisible: true,
  };
}

export function volumeOptions(p: Palette): HistogramOpts {
  // Overlaid on the main pane, its own invisible scale; per-bar color is set on
  // each data point (up/down) at setData/update time by the controller.
  return { priceScaleId: "", priceFormat: { type: "volume" } };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/chartTheme.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/chartTheme.ts ui/src/render/chart/chartTheme.test.ts
git commit -m "feat(ui/chart): LWC theme mapping derived from the palette"
```

---

## Task 3: Bar bucketing test-mirror (pure)

**Files:**
- Create: `ui/src/render/chart/barBucket.ts`
- Test: `ui/src/render/chart/barBucket.test.ts`

**Interfaces:**
- Produces: `type Timeframe = "10s"|"1m"|"5m"|"15m"|"30m"|"60m"|"D"|"W"|"M"`, `function bucketStartMs(tsMs: number, tf: Timeframe): number`, `function etParts(tsMs: number): { y:number; mo:number; d:number; h:number; mi:number; s:number; wday:number }`. This mirrors the engine's session-anchored bucketing so fixtures can be *validated* against our understanding and the controller can decide in-progress-vs-new independently of message order.
- Consumed by: Task 8 (`ChartController`), Task 11 (fixture-validation test).

**Why:** the engine buckets intraday bars anchored to 09:30 ET (TradingView-style), not to midnight — a 5m bar covers 09:30–09:35, 09:35–09:40, …. `BarStore` already upserts by the `bucketStart` string the engine sends; this mirror lets us assert the fixtures follow that rule and gives the controller a pure "does this tick's time fall in the last bar's bucket?" check. All ET conversion goes through `Intl.DateTimeFormat` with `America/New_York` (handles DST correctly).

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/render/chart/barBucket.test.ts
import { describe, it, expect } from "vitest";
import { bucketStartMs, etParts } from "./barBucket";

// 2026-07-06 is a Monday; ET is EDT (UTC-4) in July.
const at = (iso: string) => Date.parse(iso);

describe("etParts", () => {
  it("converts a UTC instant to ET wall-clock (EDT in July)", () => {
    const p = etParts(at("2026-07-06T13:30:00Z")); // 09:30 ET
    expect([p.h, p.mi]).toEqual([9, 30]);
  });
  it("handles EST in January (UTC-5)", () => {
    const p = etParts(at("2026-01-06T14:30:00Z")); // 09:30 ET
    expect([p.h, p.mi]).toEqual([9, 30]);
  });
});

describe("bucketStartMs — session-anchored at 09:30 ET", () => {
  it("5m buckets align to :30/:35/:40 past the anchor", () => {
    expect(bucketStartMs(at("2026-07-06T13:31:00Z"), "5m")).toBe(at("2026-07-06T13:30:00Z"));
    expect(bucketStartMs(at("2026-07-06T13:34:59Z"), "5m")).toBe(at("2026-07-06T13:30:00Z"));
    expect(bucketStartMs(at("2026-07-06T13:35:00Z"), "5m")).toBe(at("2026-07-06T13:35:00Z"));
  });
  it("10s buckets align within the minute", () => {
    expect(bucketStartMs(at("2026-07-06T13:30:07Z"), "10s")).toBe(at("2026-07-06T13:30:00Z"));
    expect(bucketStartMs(at("2026-07-06T13:30:12Z"), "10s")).toBe(at("2026-07-06T13:30:10Z"));
  });
  it("1m buckets align to the minute", () => {
    expect(bucketStartMs(at("2026-07-06T13:30:45Z"), "1m")).toBe(at("2026-07-06T13:30:00Z"));
  });
  it("D buckets start at 00:00 ET (04:00Z in EDT)", () => {
    expect(bucketStartMs(at("2026-07-06T18:00:00Z"), "D")).toBe(at("2026-07-06T04:00:00Z"));
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/barBucket.test.ts`
Expected: FAIL — `Cannot find module './barBucket'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/chart/barBucket.ts
// Pure mirror of the engine's session-anchored bar bucketing. Intraday buckets
// are anchored to 09:30 ET (TradingView-style), NOT to midnight. Used to validate
// fixtures and to let the chart controller reason about in-progress vs new buckets
// without depending on message arrival order. ET conversion via Intl (DST-correct).
export type Timeframe = "10s" | "1m" | "5m" | "15m" | "30m" | "60m" | "D" | "W" | "M";

const ET = new Intl.DateTimeFormat("en-US", {
  timeZone: "America/New_York", hour12: false,
  year: "numeric", month: "numeric", day: "numeric",
  hour: "2-digit", minute: "2-digit", second: "2-digit", weekday: "short",
});
const WDAY: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

export interface EtParts { y: number; mo: number; d: number; h: number; mi: number; s: number; wday: number }

export function etParts(tsMs: number): EtParts {
  const parts = ET.formatToParts(new Date(tsMs));
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "0";
  let h = Number(get("hour"));
  if (h === 24) h = 0; // hour12:false can yield "24" at midnight in some engines
  return {
    y: Number(get("year")), mo: Number(get("month")), d: Number(get("day")),
    h, mi: Number(get("minute")), s: Number(get("second")),
    wday: WDAY[get("weekday")] ?? 0,
  };
}

// ET midnight (00:00 ET) for the ET calendar day containing tsMs, as an epoch ms.
function etMidnightMs(tsMs: number): number {
  const p = etParts(tsMs);
  const secsSinceEtMidnight = p.h * 3600 + p.mi * 60 + p.s;
  return tsMs - secsSinceEtMidnight * 1000 - (new Date(tsMs).getUTCMilliseconds());
}

const ANCHOR_SECS = 9 * 3600 + 30 * 60; // 09:30 ET session anchor

export function bucketStartMs(tsMs: number, tf: Timeframe): number {
  const midnight = etMidnightMs(tsMs);
  const secsIntoDay = Math.floor((tsMs - midnight) / 1000);

  const floorTo = (spanSecs: number, anchorSecs: number): number => {
    const rel = secsIntoDay - anchorSecs;
    const bucketRel = Math.floor(rel / spanSecs) * spanSecs;
    return midnight + (anchorSecs + bucketRel) * 1000;
  };

  switch (tf) {
    case "10s": return floorTo(10, 0);        // aligned to the minute grid
    case "1m":  return floorTo(60, 0);
    case "5m":  return floorTo(5 * 60, ANCHOR_SECS);
    case "15m": return floorTo(15 * 60, ANCHOR_SECS);
    case "30m": return floorTo(30 * 60, ANCHOR_SECS);
    case "60m": return floorTo(60 * 60, ANCHOR_SECS);
    case "D":   return midnight;
    case "W": {
      // Week starts Monday 00:00 ET.
      const p = etParts(tsMs);
      const daysFromMonday = (p.wday + 6) % 7;
      return midnight - daysFromMonday * 86400 * 1000;
    }
    case "M": {
      const p = etParts(tsMs);
      // First of the ET month at 00:00 ET: subtract (day-1) days from ET midnight.
      return midnight - (p.d - 1) * 86400 * 1000;
    }
  }
}
```

> **Implementation caveat to verify in Step 4:** `etMidnightMs` assumes ET offset is constant across the seconds it subtracts, which holds except across a DST transition instant — acceptable for bar bucketing (no US market session straddles the 02:00 transition). Keep the DST test (`etParts` January case) green as the guard.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/barBucket.test.ts`
Expected: PASS (all cases, both DST regimes).

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/barBucket.ts ui/src/render/chart/barBucket.test.ts
git commit -m "feat(ui/chart): session-anchored bar-bucketing test-mirror of the engine"
```

---

## Task 4: ET session bands (pure)

**Files:**
- Create: `ui/src/render/chart/sessions.ts`
- Test: `ui/src/render/chart/sessions.test.ts`

**Interfaces:**
- Consumes: `etParts` (Task 3).
- Produces: `type Session = "pre"|"rth"|"post"|"closed"`, `interface Band { startMs: number; endMs: number; session: Session }`, `function sessionBands(fromMs: number, toMs: number): Band[]`.
- Consumed by: Task 6b (`sessionPrimitive.ts`), Task 8 (controller passes bands to the primitive).

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/render/chart/sessions.test.ts
import { describe, it, expect } from "vitest";
import { sessionBands } from "./sessions";

const at = (iso: string) => Date.parse(iso);

describe("sessionBands", () => {
  it("segments one ET trading day into pre / rth / post / closed", () => {
    // 2026-07-06 (EDT): pre 04:00–09:30 ET = 08:00–13:30Z; rth 09:30–16:00 = 13:30–20:00Z;
    // post 16:00–20:00 = 20:00–24:00Z.
    const bands = sessionBands(at("2026-07-06T04:00:00Z"), at("2026-07-07T04:00:00Z"));
    const rth = bands.find((b) => b.session === "rth")!;
    expect(rth.startMs).toBe(at("2026-07-06T13:30:00Z"));
    expect(rth.endMs).toBe(at("2026-07-06T20:00:00Z"));
    const pre = bands.find((b) => b.session === "pre")!;
    expect(pre.endMs).toBe(at("2026-07-06T13:30:00Z"));
    const post = bands.find((b) => b.session === "post")!;
    expect(post.startMs).toBe(at("2026-07-06T20:00:00Z"));
  });

  it("bands are contiguous and cover the whole range", () => {
    const from = at("2026-07-06T10:00:00Z"), to = at("2026-07-06T22:00:00Z");
    const bands = sessionBands(from, to);
    expect(bands[0].startMs).toBe(from);
    expect(bands[bands.length - 1].endMs).toBe(to);
    for (let i = 1; i < bands.length; i++) expect(bands[i].startMs).toBe(bands[i - 1].endMs);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/sessions.test.ts`
Expected: FAIL — `Cannot find module './sessions'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/chart/sessions.ts
import { etParts } from "./barBucket";

export type Session = "pre" | "rth" | "post" | "closed";
export interface Band { startMs: number; endMs: number; session: Session }

const PRE = 4 * 60, RTH = 9 * 60 + 30, POST = 16 * 60, CLOSE = 20 * 60; // minutes into ET day

function sessionAt(tsMs: number): Session {
  const p = etParts(tsMs);
  const m = p.h * 60 + p.mi;
  if (p.wday === 0 || p.wday === 6) return "closed"; // weekend (holidays: engine may override later)
  if (m < PRE) return "closed";
  if (m < RTH) return "pre";
  if (m < POST) return "rth";
  if (m < CLOSE) return "post";
  return "closed";
}

// Contiguous session bands covering [fromMs, toMs). Steps at each ET boundary by
// walking forward and snapping to the next transition; bounded, no unbounded loop.
export function sessionBands(fromMs: number, toMs: number): Band[] {
  const bands: Band[] = [];
  let cursor = fromMs;
  let guard = 0;
  while (cursor < toMs && guard++ < 10_000) {
    const session = sessionAt(cursor);
    const next = Math.min(nextBoundaryMs(cursor), toMs);
    bands.push({ startMs: cursor, endMs: next, session });
    cursor = next;
  }
  return bands;
}

// The next ET session-boundary instant strictly after tsMs.
function nextBoundaryMs(tsMs: number): number {
  const p = etParts(tsMs);
  const m = p.h * 60 + p.mi;
  const secOffset = p.s * 1000 + (tsMs % 1000);
  const minutesToBoundary = (() => {
    for (const b of [PRE, RTH, POST, CLOSE, 24 * 60]) if (b > m) return b - m;
    return 24 * 60 - m;
  })();
  // align to the boundary minute exactly
  return tsMs - secOffset + minutesToBoundary * 60_000 - (secOffset > 0 ? 0 : 0);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/sessions.test.ts`
Expected: PASS. If the "contiguous" test reveals off-by-one at boundaries, fix `nextBoundaryMs` alignment until both tests are green.

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/sessions.ts ui/src/render/chart/sessions.test.ts
git commit -m "feat(ui/chart): pure ET session-band computation (pre/rth/post/closed)"
```

---

## Task 5: Diamond fill-marker geometry (pure port)

**Files:**
- Create: `ui/src/render/chart/diamondMarker.ts`
- Test: `ui/src/render/chart/diamondMarker.test.ts`

**Interfaces:**
- Produces: `interface FillMarker { timeMs: number; price: number; side: "buy"|"sell" }`, `function diamondHalfSize(size: number): number`, `function drawDiamondPath(ctx: PathCtx, x: number, y: number, halfSize: number): void`, `function hitTestDiamond(cx:number, cy:number, size:number, x:number, y:number): boolean`, `function fillColor(side: "buy"|"sell", p: Palette): string`.
- Consumed by: Task 6a (`diamondPrimitive.ts`).

**Port source (reference, not a dependency):** `~/Projects/earlisreal-lightweight-charts/src/renderers/series-markers-diamond.ts` @ `069fa855` — `drawDiamond` (path) + `hitTestDiamond` (Manhattan). The 0.8 size factor comes from `shapeSize('diamond', size)` in `series-markers-utils.ts` (`size(originalSize, 0.8)`). The border comes from the v3.7.1 `borderWidth` pattern (`a25e7dc0`): stroke the same path after fill.

> **Verified against `lightweight-charts@5.2.0` typings (2026-07-04):** upstream v5 **cannot** draw a diamond natively — `SeriesMarkerShape = "circle" | "square" | "arrowUp" | "arrowDown"` (no diamond), and the native `SeriesMarker` has a single `color` field with **no border/outline** property. So the diamond-with-border in the UI spec is unreachable via `createSeriesMarkers`. What *is* native is price anchoring (`SeriesMarkerPricePosition = "atPriceTop" | "atPriceBottom" | "atPriceMiddle"` + a `price` field) — the `atPrice*` culling the roadmap credited. The supported way to draw a custom shape in v5 is the **primitive API** (`series.attachPrimitive(ISeriesPrimitive)`), which is the same mechanism `createSeriesMarkers` itself is built on. Therefore: we do **not** depend on the fork — the diamond geometry is trivial generic canvas math (4 `lineTo`s + a Manhattan hit test) reimplemented in this pure module and drawn via a v5 series primitive (Task 9). The fork is cited only for provenance of the 0.8 factor and the border pattern Earl already tuned.

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/render/chart/diamondMarker.test.ts
import { describe, it, expect } from "vitest";
import { diamondHalfSize, drawDiamondPath, hitTestDiamond, fillColor } from "./diamondMarker";
import { LIGHT } from "../palette";

describe("diamond geometry (ported from earlisreal-lightweight-charts 069fa855)", () => {
  it("applies the 0.8 size factor", () => {
    // shapeSize('diamond', s) = round(s * 0.8) made odd; halfSize = (that - 1)/2.
    expect(diamondHalfSize(20)).toBeCloseTo((oddish(20 * 0.8) - 1) / 2, 5);
  });

  it("hit test is Manhattan-distance (rotated square)", () => {
    // size 20 → halfSize ≈ 7; a point 3 right + 3 up (sum 6 ≤ 7) hits; 5+5=10 misses.
    expect(hitTestDiamond(100, 100, 20, 103, 97)).toBe(true);
    expect(hitTestDiamond(100, 100, 20, 105, 95)).toBe(false);
  });

  it("draws a 4-point closed diamond path centered at (x,y)", () => {
    const ops: string[] = [];
    const ctx = {
      beginPath: () => ops.push("begin"),
      moveTo: (x: number, y: number) => ops.push(`move ${x} ${y}`),
      lineTo: (x: number, y: number) => ops.push(`line ${x} ${y}`),
      closePath: () => ops.push("close"),
    };
    drawDiamondPath(ctx, 50, 50, 10);
    expect(ops).toEqual([
      "begin", "move 50 40", "line 40 50", "line 50 60", "line 60 50", "close",
    ]);
  });

  it("maps side to the palette fill color", () => {
    expect(fillColor("buy", LIGHT)).toBe(LIGHT.buyFill);
    expect(fillColor("sell", LIGHT)).toBe(LIGHT.sellFill);
  });
});

function oddish(n: number): number { const r = Math.round(n); return r % 2 === 0 ? r + 1 : r; }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/diamondMarker.test.ts`
Expected: FAIL — `Cannot find module './diamondMarker'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/chart/diamondMarker.ts
// Ported from ~/Projects/earlisreal-lightweight-charts @ 069fa855
// (drawDiamond / hitTestDiamond, Manhattan hit test, 0.8 size factor) + the
// v3.7.1 borderWidth pattern (a25e7dc0: stroke the path after fill). Kept pure so
// the primitive (diamondPrimitive.ts) stays a thin LWC adapter.
import type { Palette } from "../palette";

export interface FillMarker { timeMs: number; price: number; side: "buy" | "sell" }

// Minimal 2D-context surface the path routine needs (so it is testable with a fake).
export interface PathCtx {
  beginPath(): void;
  moveTo(x: number, y: number): void;
  lineTo(x: number, y: number): void;
  closePath(): void;
}

// shapeSize('diamond', size) === size * 0.8, rounded to an odd integer (LWC keeps
// marker shapes odd-sized so they center on a pixel). halfSize = (shapeSize - 1)/2.
function shapeSize(size: number): number {
  const r = Math.round(size * 0.8);
  return r % 2 === 0 ? r + 1 : r;
}
export function diamondHalfSize(size: number): number {
  return (shapeSize(size) - 1) / 2;
}

export function drawDiamondPath(ctx: PathCtx, x: number, y: number, halfSize: number): void {
  ctx.beginPath();
  ctx.moveTo(x, y - halfSize);
  ctx.lineTo(x - halfSize, y);
  ctx.lineTo(x, y + halfSize);
  ctx.lineTo(x + halfSize, y);
  ctx.closePath();
}

export function hitTestDiamond(cx: number, cy: number, size: number, x: number, y: number): boolean {
  const halfSize = diamondHalfSize(size);
  return Math.abs(cx - x) + Math.abs(cy - y) <= halfSize; // Manhattan ball = rotated square
}

export function fillColor(side: "buy" | "sell", p: Palette): string {
  return side === "buy" ? p.buyFill : p.sellFill;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/diamondMarker.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/diamondMarker.ts ui/src/render/chart/diamondMarker.test.ts
git commit -m "feat(ui/chart): port diamond fill-marker geometry (drawDiamond/hitTestDiamond)"
```

---

## Task 6: IndicatorStore + md.indicator routing

**Files:**
- Create: `ui/src/data/IndicatorStore.ts`
- Test: `ui/src/data/IndicatorStore.test.ts`
- Modify: `ui/src/data/registry.ts`
- Modify: `ui/src/data/registry.test.ts`

**Interfaces:**
- Consumes: `PaintStore` (Plan 1 `data/store.ts`), `SnapshotMsg`/`DeltaMsg` (contract).
- Produces: `interface IndicatorPoint { timeMs: number; value: number }`, `class IndicatorStore extends PaintStore` with `apply(m)` (keyed by `m.key` = instanceId; snapshot replaces that instance's series, delta appends/upserts the last point by time) and `series(instanceId: string): IndicatorPoint[]`. Adds `indicators: IndicatorStore` to the `Stores` interface and `makeStores()`; routes `md.indicator` to it (replacing the Plan-1 no-op at `registry.ts:41`).
- Consumed by: Task 8 (`ChartController`).

> **Contract note:** the interim `md.indicator` payload for a snapshot is `IndicatorPoint[]`; for a delta it is a single `IndicatorPoint`. Both carry the instance via the message `key` (the `instanceId`). This matches the spec's "engine streams backfill + live series like any other topic" and keeps field names ready for tygo.

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/data/IndicatorStore.test.ts
import { describe, it, expect } from "vitest";
import { IndicatorStore } from "./IndicatorStore";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

const snap = (key: string, payload: unknown): SnapshotMsg => ({ kind: "snapshot", topic: "md.indicator", key, payload });
const delta = (key: string, payload: unknown): DeltaMsg => ({ kind: "delta", topic: "md.indicator", key, payload });

describe("IndicatorStore", () => {
  it("snapshot loads a series, delta appends a new point", () => {
    const s = new IndicatorStore();
    s.apply(snap("vwap-1", [{ timeMs: 1000, value: 10 }, { timeMs: 2000, value: 11 }]));
    s.apply(delta("vwap-1", { timeMs: 3000, value: 12 }));
    expect(s.series("vwap-1")).toEqual([
      { timeMs: 1000, value: 10 }, { timeMs: 2000, value: 11 }, { timeMs: 3000, value: 12 },
    ]);
  });

  it("delta with same timeMs upserts the last point in place (in-progress value)", () => {
    const s = new IndicatorStore();
    s.apply(snap("vwap-1", [{ timeMs: 1000, value: 10 }]));
    s.apply(delta("vwap-1", { timeMs: 1000, value: 10.5 }));
    expect(s.series("vwap-1")).toEqual([{ timeMs: 1000, value: 10.5 }]);
  });

  it("keeps instances independent and marks dirty on apply", () => {
    const s = new IndicatorStore();
    s.apply(snap("ema-9", [{ timeMs: 1000, value: 5 }]));
    s.apply(snap("sma-20", [{ timeMs: 1000, value: 6 }]));
    expect(s.series("ema-9")).toHaveLength(1);
    expect(s.series("sma-20")[0].value).toBe(6);
    expect(s.series("missing")).toEqual([]);
    expect(s.consumeDirty()).toBe(true);
    expect(s.consumeDirty()).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/data/IndicatorStore.test.ts`
Expected: FAIL — `Cannot find module './IndicatorStore'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/data/IndicatorStore.ts
import { PaintStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

export interface IndicatorPoint { timeMs: number; value: number }

// One series per indicator instanceId (delivered as the message `key`). Snapshot
// replaces the whole series (backfill); delta appends a new point, or upserts the
// last point in place when timeMs matches (the current in-progress bar's value).
export class IndicatorStore extends PaintStore {
  private readonly byInstance = new Map<string, IndicatorPoint[]>();

  apply(m: SnapshotMsg | DeltaMsg): void {
    const id = m.key ?? "";
    if (m.kind === "snapshot") {
      const pts = (m.payload as IndicatorPoint[]).slice().sort((a, b) => a.timeMs - b.timeMs);
      this.byInstance.set(id, pts);
      this.markDirty();
      return;
    }
    const pt = m.payload as IndicatorPoint;
    const arr = this.byInstance.get(id) ?? [];
    const last = arr[arr.length - 1];
    if (last && last.timeMs === pt.timeMs) arr[arr.length - 1] = pt;
    else arr.push(pt);
    this.byInstance.set(id, arr);
    this.markDirty();
  }

  series(instanceId: string): IndicatorPoint[] {
    return this.byInstance.get(instanceId) ?? [];
  }
}
```

- [ ] **Step 4: Wire it into the registry**

In `ui/src/data/registry.ts`:

```ts
// add to imports
import { IndicatorStore } from "./IndicatorStore";

// add to the Stores interface (after `bars: BarStore;`)
  indicators: IndicatorStore;

// add to makeStores() return object (after `bars: new BarStore(),`)
    indicators: new IndicatorStore(),

// replace the Plan-1 no-op line
//   case "md.indicator": return; // Plan 2 adds an IndicatorStore
// with:
    case "md.indicator": stores.indicators.apply(m); return;
```

- [ ] **Step 5: Extend the registry test**

In `ui/src/data/registry.test.ts`, add a case asserting `md.indicator` routes to the indicator store:

```ts
it("routes md.indicator to the IndicatorStore keyed by instanceId", () => {
  const stores = makeStores();
  routeToStore(stores, { kind: "snapshot", topic: "md.indicator", key: "vwap-1",
    payload: [{ timeMs: 1000, value: 10 }] });
  expect(stores.indicators.series("vwap-1")).toHaveLength(1);
});
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/data/IndicatorStore.test.ts src/data/registry.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add ui/src/data/IndicatorStore.ts ui/src/data/IndicatorStore.test.ts ui/src/data/registry.ts ui/src/data/registry.test.ts
git commit -m "feat(ui/data): IndicatorStore (per-instance series) + md.indicator routing"
```

---

## Task 7: Indicator catalog + instance → series descriptor (pure)

**Files:**
- Create: `ui/src/render/chart/indicatorSeries.ts`
- Test: `ui/src/render/chart/indicatorSeries.test.ts`

**What this delivers (spec: "customizable per chart — add/remove instances, periods, colors"):** a data-driven **indicator catalog** describing, for every type, its customizable **parameters** (with defaults + bounds) and its drawable **slots** (each with the palette color it defaults to). Both the per-chart management UI (Task 9) and `describeIndicator` are generated from this catalog, so:
- **Parameters are customizable** — every param (EMA/SMA period, MACD fast/slow/signal) is editable per instance and flows to the engine in the `SubscribeIndicator` command (Task 8), which recomputes the series.
- **Colors are customizable per drawable series** — a "slot" is one line/histogram within an instance. Single-series indicators have one slot (`line`); MACD has three (`macd`/`signal`/`hist`). Color overrides are keyed by slot, so **every series of every indicator (including each MACD line) is individually themeable**; unset slots fall back to the palette default and re-derive on theme switch.

**Interfaces:**
- Consumes: `Palette` (Task 1).
- Produces: `type IndicatorType`, `interface IndicatorInstance { instanceId; type; params: Record<string,number>; colors?: Record<string,string> }`, `interface ParamSpec`, `interface SlotSpec`, `interface CatalogEntry`, `const INDICATOR_CATALOG: Record<IndicatorType, CatalogEntry>`, `function withDefaultParams(type, params?)`, `interface SeriesDescriptor { key; slot; kind; paneIndex; color }`, `function describeIndicator(inst, p): SeriesDescriptor[]`.
- Consumed by: Task 8 (`ChartController`), Task 9 (management UI renders param inputs + color pickers from the catalog).

- [ ] **Step 1: Write the failing test**

```ts
// ui/src/render/chart/indicatorSeries.test.ts
import { describe, it, expect } from "vitest";
import { describeIndicator, withDefaultParams, INDICATOR_CATALOG } from "./indicatorSeries";
import { LIGHT } from "../palette";

describe("indicator catalog", () => {
  it("exposes editable params with defaults + bounds for parameterized types", () => {
    expect(INDICATOR_CATALOG.EMA.params).toEqual([{ key: "period", label: "Period", default: 9, min: 1, max: 400 }]);
    expect(INDICATOR_CATALOG.MACD.params.map((p) => p.key)).toEqual(["fast", "slow", "signal"]);
    expect(INDICATOR_CATALOG.VWAP.params).toEqual([]); // VWAP has no params
  });

  it("withDefaultParams fills missing params from the catalog, keeping user overrides", () => {
    expect(withDefaultParams("EMA")).toEqual({ period: 9 });
    expect(withDefaultParams("EMA", { period: 21 })).toEqual({ period: 21 });
    expect(withDefaultParams("MACD", { fast: 8 })).toEqual({ fast: 8, slow: 26, signal: 9 });
  });
});

describe("describeIndicator", () => {
  it("overlays VWAP/EMA/SMA as single-slot lines on the main pane (0), palette-defaulted", () => {
    for (const [type, color] of [["VWAP", LIGHT.indVwap], ["EMA", LIGHT.indEma], ["SMA", LIGHT.indSma]] as const) {
      const [d] = describeIndicator({ instanceId: `${type}-1`, type, params: {} }, LIGHT);
      expect(d).toMatchObject({ kind: "line", paneIndex: 0, slot: "line", color, key: `${type}-1` });
    }
  });

  it("routes VOLUME/DELTA to histograms on the main pane", () => {
    expect(describeIndicator({ instanceId: "vol", type: "VOLUME", params: {} }, LIGHT)[0].kind).toBe("histogram");
    expect(describeIndicator({ instanceId: "d", type: "DELTA", params: {} }, LIGHT)[0].kind).toBe("histogram");
  });

  it("MACD yields three slots in the sub-pane (1), each palette-defaulted and #slot-keyed", () => {
    const ds = describeIndicator({ instanceId: "macd", type: "MACD", params: {} }, LIGHT);
    expect(ds.map((d) => d.slot)).toEqual(["macd", "signal", "hist"]);
    expect(ds.map((d) => d.key)).toEqual(["macd#macd", "macd#signal", "macd#hist"]);
    expect(ds.every((d) => d.paneIndex === 1)).toBe(true);
    expect([ds[0].color, ds[1].color, ds[2].color]).toEqual([LIGHT.indMacdLine, LIGHT.indMacdSignal, LIGHT.indMacdHist]);
  });

  it("honors a per-slot color override, even for one of MACD's three series", () => {
    const [d] = describeIndicator({ instanceId: "ema", type: "EMA", params: {}, colors: { line: "#123456" } }, LIGHT);
    expect(d.color).toBe("#123456");
    const macd = describeIndicator({ instanceId: "m", type: "MACD", params: {}, colors: { signal: "#abcdef" } }, LIGHT);
    expect(macd.find((d) => d.slot === "signal")!.color).toBe("#abcdef");
    expect(macd.find((d) => d.slot === "macd")!.color).toBe(LIGHT.indMacdLine); // others stay palette-default
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/indicatorSeries.test.ts`
Expected: FAIL — `Cannot find module './indicatorSeries'`.

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/chart/indicatorSeries.ts
import type { Palette } from "../palette";

export type IndicatorType = "VWAP" | "EMA" | "SMA" | "MACD" | "VOLUME" | "DELTA";

// A per-chart indicator instance. `params` and `colors` are the customizable state,
// persisted with the workspace (Task 9). `colors` is keyed by slot; unset slots use
// the palette default (so they re-theme automatically on light/dark switch).
export interface IndicatorInstance {
  instanceId: string;
  type: IndicatorType;
  params: Record<string, number>;
  colors?: Record<string, string>;
}

export interface ParamSpec { key: string; label: string; default: number; min: number; max: number }
export interface SlotSpec { slot: string; kind: "line" | "histogram"; paneIndex: number; paletteKey: keyof Palette }
export interface CatalogEntry { type: IndicatorType; label: string; params: ParamSpec[]; slots: SlotSpec[] }

export interface SeriesDescriptor {
  key: string;         // unique LWC series id: instanceId (single-slot) or `${instanceId}#${slot}`
  slot: string;        // stable slot name — the persistable color key
  kind: "line" | "histogram";
  paneIndex: number;   // 0 = main pane, 1 = MACD sub-pane
  color: string;       // resolved: inst.colors?.[slot] ?? palette[slot's default key]
}

const MAIN = 0, SUBPANE = 1;

// The v1 indicator catalog: every type's editable params (defaults + bounds) and
// drawable slots (with the palette key each defaults to). The management UI (Task 9)
// renders inputs from `params` and color pickers from `slots`.
export const INDICATOR_CATALOG: Record<IndicatorType, CatalogEntry> = {
  VWAP:   { type: "VWAP",   label: "VWAP",       params: [], slots: [{ slot: "line", kind: "line", paneIndex: MAIN, paletteKey: "indVwap" }] },
  EMA:    { type: "EMA",    label: "EMA",        params: [{ key: "period", label: "Period", default: 9,  min: 1, max: 400 }], slots: [{ slot: "line", kind: "line", paneIndex: MAIN, paletteKey: "indEma" }] },
  SMA:    { type: "SMA",    label: "SMA",        params: [{ key: "period", label: "Period", default: 20, min: 1, max: 400 }], slots: [{ slot: "line", kind: "line", paneIndex: MAIN, paletteKey: "indSma" }] },
  VOLUME: { type: "VOLUME", label: "Volume",     params: [], slots: [{ slot: "hist", kind: "histogram", paneIndex: MAIN, paletteKey: "indMacdHist" }] },
  DELTA:  { type: "DELTA",  label: "Buy/Sell Δ", params: [], slots: [{ slot: "hist", kind: "histogram", paneIndex: MAIN, paletteKey: "indMacdHist" }] },
  MACD:   { type: "MACD",   label: "MACD",
            params: [
              { key: "fast",   label: "Fast",   default: 12, min: 1, max: 200 },
              { key: "slow",   label: "Slow",   default: 26, min: 1, max: 400 },
              { key: "signal", label: "Signal", default: 9,  min: 1, max: 200 },
            ],
            slots: [
              { slot: "macd",   kind: "line",      paneIndex: SUBPANE, paletteKey: "indMacdLine" },
              { slot: "signal", kind: "line",      paneIndex: SUBPANE, paletteKey: "indMacdSignal" },
              { slot: "hist",   kind: "histogram", paneIndex: SUBPANE, paletteKey: "indMacdHist" },
            ] },
};

// Fill any params the user hasn't set with the catalog defaults.
export function withDefaultParams(type: IndicatorType, params: Record<string, number> = {}): Record<string, number> {
  const out = { ...params };
  for (const p of INDICATOR_CATALOG[type].params) if (out[p.key] === undefined) out[p.key] = p.default;
  return out;
}

export function describeIndicator(inst: IndicatorInstance, p: Palette): SeriesDescriptor[] {
  const entry = INDICATOR_CATALOG[inst.type];
  const single = entry.slots.length === 1;
  return entry.slots.map((s) => ({
    key: single ? inst.instanceId : `${inst.instanceId}#${s.slot}`,
    slot: s.slot,
    kind: s.kind,
    paneIndex: s.paneIndex,
    color: inst.colors?.[s.slot] ?? p[s.paletteKey],
  }));
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/indicatorSeries.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/indicatorSeries.ts ui/src/render/chart/indicatorSeries.test.ts
git commit -m "feat(ui/chart): indicator catalog + per-slot color model (customizable params + colors)"
```

---

## Task 8: ChartApiFacade + ChartController (the heart, fake-facade tested)

**Files:**
- Create: `ui/src/render/chart/ChartApiFacade.ts`
- Create: `ui/src/render/chart/ChartController.ts`
- Test: `ui/src/render/chart/ChartController.test.ts`

**Interfaces:**
- Consumes: `Palette`, `chartTheme` (Task 2), `sessions.Band` (Task 4), `FillMarker` (Task 5), `describeIndicator`/`IndicatorInstance`/`SeriesDescriptor` (Task 7), `Timeframe` (Task 3), the `Bar` type (contract), and reader interfaces for bars/indicators.
- Produces:
  - `ChartApiFacade` — the minimal LWC surface: `interface LwcSeries { setData(d: unknown[]): void; update(d: unknown): void; applyOptions(o: unknown): void; }`, and `interface ChartApiFacade { addSeries(kind: "candle"|"line"|"histogram", options: unknown, paneIndex: number): LwcSeries; removeSeries(s: LwcSeries): void; setSessionBands(bands: Band[]): void; setFillMarkers(m: FillMarker[]): void; timeToCoordinate(timeMs: number): number | null; priceToCoordinate(price: number): number | null; scrollToRealTime(): void; isAtRightEdge(): boolean; resize(w: number, h: number): void; applyOptions(o: unknown): void; remove(): void; }`
  - `interface BarReader { series(symbol: string, timeframe: string): Bar[] }` and `interface IndicatorReader { series(instanceId: string): { timeMs: number; value: number }[] }` (satisfied by `BarStore`/`IndicatorStore`).
  - `interface CommandSender { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }`
  - `class ChartController` with `mount()`, `sync()`, `setSymbol(s)`, `setTimeframe(tf)`, `setPalette(p)`, `setFills(m: FillMarker[])`, `addIndicator(inst)`, `updateIndicator(inst)`, `removeIndicator(instanceId)`, `resize(w,h)`, `jumpToLive()`, `dispose()`.
- Consumed by: Task 9 (`ChartPanel`).

**Behavior contract (what the tests below lock in):**
1. `mount()` creates a candle series (pane 0) and a volume histogram (pane 0, overlay scale) and applies the theme.
2. First `sync()` for a (symbol, timeframe) with backfill bars calls candle `setData` with the full array + volume `setData`; it never calls `update` on the first paint.
3. A later `sync()` where only the last (in-progress) bar changed calls candle `update`/volume `update` **once** with that bar — not `setData`.
4. A `sync()` where a new bucket appeared also calls `update` (LWC treats a later time as append).
5. Auto-follow: after applying a new/updated last bar, if `isAtRightEdge()` is true the controller lets LWC keep following (no explicit scroll); it does **not** force-scroll when the user has scrolled back.
6. `jumpToLive()` calls `scrollToRealTime()`.
7. `setSymbol`/`setTimeframe` reset the applied-state so the next `sync()` re-runs `setData` for the new slice, re-issues indicator subscribe commands for the new (symbol, timeframe), and re-`setData`s indicator series.
8. `addIndicator(inst)` sends `SubscribeIndicator` (with instanceId, symbol, timeframe, type, params), creates the descriptor series; `removeIndicator` sends `UnsubscribeIndicator` and removes the series; `sync()` feeds each indicator series from `IndicatorReader`.
9. `setPalette(p)` re-applies chart + series options and forwards the palette to session/fill via facade (`setSessionBands` uses colors the primitive reads from the palette passed at construction/`setPalette`).
10. `sync()` recomputes session bands spanning the visible bars and calls `setSessionBands`.
11. Cold symbol: when `bars.series()` is empty, `sync()` is a no-op on data (no throw) — the panel shows the cold hint (Task 9), not the controller.

- [ ] **Step 1: Write the ChartApiFacade interface (no test — it is a pure type surface)**

```ts
// ui/src/render/chart/ChartApiFacade.ts
import type { Band } from "./sessions";
import type { FillMarker } from "./diamondMarker";

export interface LwcSeries {
  setData(data: unknown[]): void;
  update(bar: unknown): void;
  applyOptions(options: unknown): void;
}

// The minimal slice of Lightweight Charts v5 the controller drives. ChartPanel
// implements this over a real IChartApi; ChartController.test.ts implements a fake.
export interface ChartApiFacade {
  addSeries(kind: "candle" | "line" | "histogram", options: unknown, paneIndex: number): LwcSeries;
  removeSeries(series: LwcSeries): void;
  setSessionBands(bands: Band[]): void;  // forwarded to the session pane-primitive
  setFillMarkers(markers: FillMarker[]): void; // forwarded to the diamond series-primitive
  timeToCoordinate(timeMs: number): number | null;
  priceToCoordinate(price: number): number | null;
  scrollToRealTime(): void;
  isAtRightEdge(): boolean;
  resize(width: number, height: number): void;
  applyOptions(options: unknown): void;
  remove(): void;
}
```

- [ ] **Step 2: Write the failing controller test**

```ts
// ui/src/render/chart/ChartController.test.ts
import { describe, it, expect, vi } from "vitest";
import { ChartController, type BarReader, type IndicatorReader, type CommandSender } from "./ChartController";
import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import { LIGHT } from "../palette";
import type { Bar } from "../../wire/contract";

function fakeSeries(): LwcSeries & { calls: string[] } {
  const calls: string[] = [];
  return { calls, setData: () => calls.push("setData"), update: () => calls.push("update"), applyOptions: () => calls.push("applyOptions") };
}

function fakeFacade() {
  const created: Array<{ kind: string; pane: number; series: LwcSeries & { calls: string[] } }> = [];
  let atRightEdge = true;
  const facade: ChartApiFacade & { created: typeof created; setRightEdge: (v: boolean) => void; scrolls: number; bands: number } = {
    created, scrolls: 0, bands: 0,
    setRightEdge: (v) => { atRightEdge = v; },
    addSeries: (kind, _o, pane) => { const s = fakeSeries(); created.push({ kind, pane, series: s }); return s; },
    removeSeries: () => {},
    setSessionBands: () => { facade.bands++; },
    setFillMarkers: () => {},
    timeToCoordinate: () => 0,
    priceToCoordinate: () => 0,
    scrollToRealTime: () => { facade.scrolls++; },
    isAtRightEdge: () => atRightEdge,
    resize: () => {},
    applyOptions: () => {},
    remove: () => {},
  };
  return facade;
}

const bar = (bucketStart: string, c: number, inProgress = false): Bar =>
  ({ symbol: "US.AAPL", timeframe: "1m", bucketStart, o: c, h: c, l: c, c, v: 100, inProgress });

function barReaderOf(bars: Bar[]): BarReader { return { series: () => bars }; }
const emptyIndicators: IndicatorReader = { series: () => [] };
function commandSpy(): CommandSender & { names: string[] } {
  const names: string[] = [];
  return { names, sendCommand: (n) => { names.push(n); return Promise.resolve({ status: "accepted" }); } };
}

const make = (reader: BarReader, cmd = commandSpy(), ind: IndicatorReader = emptyIndicators) => {
  const facade = fakeFacade();
  const ctrl = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" }, { bars: reader, indicators: ind, commands: cmd });
  ctrl.mount();
  return { facade, ctrl, cmd };
};

describe("ChartController", () => {
  it("mount creates a candle + volume series", () => {
    const { facade } = make(barReaderOf([]));
    expect(facade.created.map((c) => c.kind)).toEqual(["candle", "histogram"]);
  });

  it("first sync with backfill calls setData, not update", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10), bar("2026-07-06T13:31:00Z", 11)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    const candle = facade.created[0].series;
    expect(candle.calls).toContain("setData");
    expect(candle.calls).not.toContain("update");
  });

  it("second sync with only the last bar changed calls update once", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10, false), bar("2026-07-06T13:31:00Z", 11, true)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();                       // backfill → setData
    bars[1] = bar("2026-07-06T13:31:00Z", 11.5, true); // in-progress tick
    const candle = facade.created[0].series;
    const before = candle.calls.filter((c) => c === "update").length;
    ctrl.sync();
    const after = candle.calls.filter((c) => c === "update").length;
    expect(after - before).toBe(1);
    expect(candle.calls.filter((c) => c === "setData")).toHaveLength(1);
  });

  it("does not force-scroll when the user has scrolled back", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10, true)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    facade.setRightEdge(false);
    bars[0] = bar("2026-07-06T13:30:00Z", 10.5, true);
    ctrl.sync();
    expect(facade.scrolls).toBe(0);
    ctrl.jumpToLive();
    expect(facade.scrolls).toBe(1);
  });

  it("addIndicator subscribes and creates the descriptor series; remove unsubscribes", () => {
    const { facade, ctrl, cmd } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    expect(cmd.names).toContain("SubscribeIndicator");
    expect(facade.created.some((c) => c.kind === "line")).toBe(true);
    ctrl.removeIndicator("vwap-1");
    expect(cmd.names).toContain("UnsubscribeIndicator");
  });

  it("updateIndicator: param edit re-subscribes; color-only edit does not", () => {
    const { ctrl, cmd } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 9 } });
    cmd.names.length = 0;
    ctrl.updateIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 21 } }); // param change
    expect(cmd.names).toEqual(["UnsubscribeIndicator", "SubscribeIndicator"]);
    cmd.names.length = 0;
    ctrl.updateIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 21 }, colors: { line: "#123456" } });
    expect(cmd.names).toEqual([]); // color-only → applied in place, no re-subscribe
  });

  it("setSymbol re-backfills (next sync calls setData again) and re-subscribes indicators", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl, cmd } = make(barReaderOf(bars));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    ctrl.sync();
    const candle = facade.created[0].series;
    const setDataBefore = candle.calls.filter((c) => c === "setData").length;
    cmd.names.length = 0;
    ctrl.setSymbol("US.NVDA");
    ctrl.sync();
    expect(candle.calls.filter((c) => c === "setData").length).toBe(setDataBefore + 1);
    expect(cmd.names).toContain("SubscribeIndicator"); // re-subscribed for the new symbol
  });

  it("sync recomputes and sets session bands", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    expect(facade.bands).toBeGreaterThan(0);
  });

  it("sync on a cold (empty) series does not throw or setData", () => {
    const { facade, ctrl } = make(barReaderOf([]));
    expect(() => ctrl.sync()).not.toThrow();
    expect(facade.created[0].series.calls).not.toContain("setData");
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: FAIL — `Cannot find module './ChartController'`.

- [ ] **Step 4: Write the implementation**

```ts
// ui/src/render/chart/ChartController.ts
import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import type { Palette } from "../palette";
import type { Bar } from "../../wire/contract";
import { chartOptions, candleOptions, volumeOptions } from "./chartTheme";
import { sessionBands } from "./sessions";
import { describeIndicator, withDefaultParams, type IndicatorInstance, type SeriesDescriptor } from "./indicatorSeries";
import type { FillMarker } from "./diamondMarker";
import type { Timeframe } from "./barBucket";

export interface BarReader { series(symbol: string, timeframe: string): Bar[] }
export interface IndicatorReader { series(instanceId: string): { timeMs: number; value: number }[] }
export interface CommandSender { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }

export interface ChartConfig { symbol: string; timeframe: string }
interface Deps { bars: BarReader; indicators: IndicatorReader; commands: CommandSender }

// LWC wants seconds (UTCTimestamp); our bucketStart is an ISO string.
const toLwcTime = (bucketStart: string): number => Math.floor(Date.parse(bucketStart) / 1000);
const toLwcTimeMs = (ms: number): number => Math.floor(ms / 1000);

export class ChartController {
  private candle!: LwcSeries;
  private volume!: LwcSeries;
  private lastAppliedCount = 0;             // bars applied via setData/update
  private lastAppliedKey = "";              // last bar's bucketStart|close, to detect in-progress change
  private backfilled = false;
  private readonly indicators = new Map<string, { inst: IndicatorInstance; series: Map<string, LwcSeries> }>();

  constructor(
    private facade: ChartApiFacade,
    private palette: Palette,
    private config: ChartConfig,
    private readonly deps: Deps,
  ) {}

  mount(): void {
    this.facade.applyOptions(chartOptions(this.palette));
    this.candle = this.facade.addSeries("candle", candleOptions(this.palette), 0);
    this.volume = this.facade.addSeries("histogram", volumeOptions(this.palette), 0);
  }

  sync(): void {
    const bars = this.deps.bars.series(this.config.symbol, this.config.timeframe);
    this.applyBars(bars);
    this.applyIndicators();
    this.applySessions(bars);
  }

  private applyBars(bars: Bar[]): void {
    if (bars.length === 0) return; // cold symbol — panel shows the hint, not an error
    if (!this.backfilled) {
      this.candle.setData(bars.map(toCandle));
      this.volume.setData(bars.map((b) => toVolume(b, this.palette)));
      this.backfilled = true;
      this.lastAppliedCount = bars.length;
      this.lastAppliedKey = keyOf(bars[bars.length - 1]);
      return;
    }
    const last = bars[bars.length - 1];
    const grew = bars.length > this.lastAppliedCount;
    const lastChanged = keyOf(last) !== this.lastAppliedKey;
    if (grew || lastChanged) {
      this.candle.update(toCandle(last));
      this.volume.update(toVolume(last, this.palette));
      this.lastAppliedCount = bars.length;
      this.lastAppliedKey = keyOf(last);
      // Auto-follow is LWC's default when already at the right edge; never force it
      // when the user has scrolled back (honesty: don't yank their view).
    }
  }

  private applyIndicators(): void {
    for (const { inst, series } of this.indicators.values()) {
      const descriptors = describeIndicator(inst, this.palette);
      for (const d of descriptors) {
        const s = series.get(d.key);
        if (!s) continue;
        // For MACD's multi-series the engine streams each sub-series under its own
        // instanceId suffix; single-series indicators use the base instanceId.
        const points = this.deps.indicators.series(d.key);
        s.setData(points.map((p) => ({ time: toLwcTimeMs(p.timeMs), value: p.value })));
      }
    }
  }

  private applySessions(bars: Bar[]): void {
    if (bars.length === 0) { this.facade.setSessionBands([]); return; }
    const from = Date.parse(bars[0].bucketStart);
    const to = Date.parse(bars[bars.length - 1].bucketStart) + 1;
    this.facade.setSessionBands(sessionBands(from, to));
  }

  addIndicator(inst: IndicatorInstance): void {
    // Resolve any unset params to catalog defaults so the engine always gets a
    // complete param set (and the stored instance matches what's rendered).
    const resolved: IndicatorInstance = { ...inst, params: withDefaultParams(inst.type, inst.params) };
    const series = new Map<string, LwcSeries>();
    for (const d of describeIndicator(resolved, this.palette)) {
      series.set(d.key, this.facade.addSeries(d.kind === "histogram" ? "histogram" : "line",
        { color: d.color, priceScaleId: d.paneIndex === 0 && d.kind === "histogram" ? "" : undefined }, d.paneIndex));
    }
    this.indicators.set(resolved.instanceId, { inst: resolved, series });
    void this.deps.commands.sendCommand("SubscribeIndicator", {
      instanceId: resolved.instanceId, symbol: this.config.symbol, timeframe: this.config.timeframe,
      type: resolved.type, params: resolved.params,
    });
  }

  removeIndicator(instanceId: string): void {
    const entry = this.indicators.get(instanceId);
    if (!entry) return;
    for (const s of entry.series.values()) this.facade.removeSeries(s);
    this.indicators.delete(instanceId);
    void this.deps.commands.sendCommand("UnsubscribeIndicator", { instanceId });
  }

  // Apply an edited instance. A param change re-subscribes (the engine recomputes
  // the series); a color-only change just re-applies each slot's color in place —
  // no re-subscribe, so the line doesn't blink.
  updateIndicator(inst: IndicatorInstance): void {
    const existing = this.indicators.get(inst.instanceId);
    if (!existing) { this.addIndicator(inst); return; }
    const next: IndicatorInstance = { ...inst, params: withDefaultParams(inst.type, inst.params) };
    if (JSON.stringify(existing.inst.params) !== JSON.stringify(next.params)) {
      this.removeIndicator(inst.instanceId);
      this.addIndicator(next);
      return;
    }
    existing.inst = next; // colors only
    for (const d of describeIndicator(next, this.palette)) existing.series.get(d.key)?.applyOptions({ color: d.color });
  }

  setSymbol(symbol: string): void { this.config = { ...this.config, symbol }; this.resetForReload(); }
  setTimeframe(timeframe: string): void { this.config = { ...this.config, timeframe }; this.resetForReload(); }

  private resetForReload(): void {
    this.backfilled = false;
    this.lastAppliedCount = 0;
    this.lastAppliedKey = "";
    // Re-subscribe every live indicator for the new (symbol, timeframe).
    for (const { inst } of this.indicators.values()) {
      void this.deps.commands.sendCommand("SubscribeIndicator", {
        instanceId: inst.instanceId, symbol: this.config.symbol, timeframe: this.config.timeframe,
        type: inst.type, params: inst.params,
      });
    }
  }

  setPalette(p: Palette): void {
    this.palette = p;
    this.facade.applyOptions(chartOptions(p));
    this.candle.applyOptions(candleOptions(p));
    this.volume.applyOptions(volumeOptions(p));
    for (const { inst, series } of this.indicators.values())
      for (const d of describeIndicator(inst, p)) series.get(d.key)?.applyOptions({ color: d.color });
  }

  setFills(markers: FillMarker[]): void { this.facade.setFillMarkers(markers); }
  resize(w: number, h: number): void { this.facade.resize(w, h); }
  jumpToLive(): void { this.facade.scrollToRealTime(); }
  dispose(): void {
    for (const id of [...this.indicators.keys()]) this.removeIndicator(id);
    this.facade.remove();
  }
}

function keyOf(b: Bar): string { return `${b.bucketStart}|${b.c}|${b.h}|${b.l}|${b.v}|${b.inProgress}`; }
function toCandle(b: Bar) { return { time: toLwcTime(b.bucketStart), open: b.o, high: b.h, low: b.l, close: b.c }; }
function toVolume(b: Bar, p: Palette) {
  return { time: toLwcTime(b.bucketStart), value: b.v, color: b.c >= b.o ? p.volUp : p.volDown };
}
```

> **Note on the `describeIndicator` key vs stream key:** MACD produces three descriptor keys (`id#macd`, `id#signal`, `id#hist`); `applyIndicators` reads each from `IndicatorReader.series(d.key)`, so the engine streams each MACD sub-series under those exact suffixed instanceIds. Single-series indicators use the base `instanceId`. The subscribe command carries the base `instanceId` + `type`; the engine is responsible for emitting the suffixed sub-series for MACD. Document this in the fixture (Task 11) so it round-trips.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/ChartController.test.ts`
Expected: PASS (all cases). Fix any mismatch between the behavior contract and the fake until green.

- [ ] **Step 6: Commit**

```bash
git add ui/src/render/chart/ChartApiFacade.ts ui/src/render/chart/ChartController.ts ui/src/render/chart/ChartController.test.ts
git commit -m "feat(ui/chart): ChartController — pure, fake-facade-tested chart-drive logic"
```

---

## Task 9: LWC primitives + ChartPanel (real chart) + registry/PanelProps threading

**Files:**
- Create: `ui/src/render/chart/diamondPrimitive.ts`
- Create: `ui/src/render/chart/sessionPrimitive.ts`
- Create: `ui/src/chrome/panels/ChartPanel.tsx`
- Create: `ui/src/chrome/panels/ChartControls.tsx`
- Create: `ui/src/chrome/panels/ChartPanel.test.tsx`
- Modify: `ui/package.json` (add `lightweight-charts`)
- Modify: `ui/src/chrome/panels/registry.tsx` (register `chart`; extend `PanelProps`)
- Modify: `ui/src/chrome/PanelFrame.tsx` (thread new props)
- Modify: `ui/src/App.tsx` (pass `linkGroups` + `commands`)

**Interfaces:**
- Consumes: everything above; `Scheduler.register` (Plan 1), `LinkGroups` (Plan 1), `CommandSender` shape from `WsClient.sendCommand`.
- Produces: `ChartPanel` React component; extended `PanelProps` (`+ linkGroups: LinkGroups; + commands: CommandSender`); the `"chart"` entry in `PANELS`.

- [ ] **Step 1: Install Lightweight Charts**

Run: `cd ui && npm install lightweight-charts@^5.2.0`
Then verify the v5 exports we rely on exist:
Run: `cd ui && node -e "const l=require('lightweight-charts'); console.log(['createChart','CandlestickSeries','HistogramSeries','LineSeries','CrosshairMode'].map(k=>k+':'+(k in l)))"`
Expected: all `true`. If any export name differs in the installed 5.2.x, adjust the imports in Steps 3–4 accordingly (the option *shapes* from Task 2 are unaffected).

- [ ] **Step 2: Write the diamond + session primitives**

These are thin LWC v5 primitive adapters over the pure geometry from Tasks 4–5. They import from `lightweight-charts` (external dep — allowed at the render layer). The diamond primitive is a **series primitive** (`ISeriesPrimitive`); the session primitive is a **pane primitive** drawn behind bars.

```ts
// ui/src/render/chart/diamondPrimitive.ts
import type { ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, Time } from "lightweight-charts";
import { drawDiamondPath, diamondHalfSize, fillColor, type FillMarker } from "./diamondMarker";
import type { Palette } from "../palette";

// Draws buy/sell diamond fills anchored to (time, price), with a thin dark outline
// (v3.7.1 borderWidth pattern: stroke after fill). Culling is implicit — LWC returns
// null coordinates for off-screen times/prices and we skip them.
export class DiamondFillPrimitive implements ISeriesPrimitive<Time> {
  private markers: FillMarker[] = [];
  private series: ISeriesApi<"Candlestick"> | null = null;
  private chartApi: SeriesAttachedParameter<Time>["chart"] | null = null;
  private readonly size = 16;

  constructor(private palette: Palette) {}
  attached(p: SeriesAttachedParameter<Time>): void { this.series = p.series as ISeriesApi<"Candlestick">; this.chartApi = p.chart; }
  detached(): void { this.series = null; this.chartApi = null; }
  setMarkers(m: FillMarker[]): void { this.markers = m; }
  setPalette(p: Palette): void { this.palette = p; }

  paneViews() {
    const draw = (target: { useBitmapCoordinateSpace: (cb: (scope: { context: CanvasRenderingContext2D; horizontalPixelRatio: number; verticalPixelRatio: number }) => void) => void }) => {
      const series = this.series, chartApi = this.chartApi;
      if (!series || !chartApi) return;
      target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, verticalPixelRatio: vr }) => {
        const half = diamondHalfSize(this.size);
        for (const m of this.markers) {
          const x = chartApi.timeScale().timeToCoordinate((Math.floor(m.timeMs / 1000)) as unknown as Time);
          const y = series.priceToCoordinate(m.price);
          if (x === null || y === null) continue; // off-screen → skip (culling)
          const px = x * hr, py = y * vr, ph = half * Math.max(hr, vr);
          ctx.fillStyle = fillColor(m.side, this.palette);
          drawDiamondPath(ctx, px, py, ph);
          ctx.fill();
          ctx.lineWidth = Math.max(1, hr);       // borderWidth pattern
          ctx.strokeStyle = this.palette.fillOutline;
          ctx.stroke();
        }
      });
    };
    return [{ renderer: () => ({ draw }) }];
  }
}
```

```ts
// ui/src/render/chart/sessionPrimitive.ts
import type { IPanePrimitive, PaneAttachedParameter, Time } from "lightweight-charts";
import type { Band, Session } from "./sessions";
import type { Palette } from "../palette";

// Fills vertical session bands behind the bars (pre/post/closed tinted; rth clear).
export class SessionShadingPrimitive implements IPanePrimitive<Time> {
  private bands: Band[] = [];
  private chartApi: PaneAttachedParameter<Time>["chart"] | null = null;
  constructor(private palette: Palette) {}
  attached(p: PaneAttachedParameter<Time>): void { this.chartApi = p.chart; }
  detached(): void { this.chartApi = null; }
  setBands(b: Band[]): void { this.bands = b; }
  setPalette(p: Palette): void { this.palette = p; }

  private color(s: Session): string {
    return s === "pre" ? this.palette.sessionPre
      : s === "post" ? this.palette.sessionPost
      : s === "closed" ? this.palette.sessionClosed
      : this.palette.sessionRth;
  }

  paneViews() {
    const draw = (target: { useBitmapCoordinateSpace: (cb: (scope: { context: CanvasRenderingContext2D; horizontalPixelRatio: number; bitmapSize: { height: number } }) => void) => void }) => {
      const chartApi = this.chartApi;
      if (!chartApi) return;
      const ts = chartApi.timeScale();
      target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, bitmapSize }) => {
        for (const b of this.bands) {
          const x0 = ts.timeToCoordinate((Math.floor(b.startMs / 1000)) as unknown as Time);
          const x1 = ts.timeToCoordinate((Math.floor(b.endMs / 1000)) as unknown as Time);
          if (x0 === null || x1 === null) continue;
          ctx.fillStyle = this.color(b.session);
          ctx.fillRect(x0 * hr, 0, (x1 - x0) * hr, bitmapSize.height);
        }
      });
    };
    // zOrder 'bottom' → behind the series.
    return [{ renderer: () => ({ draw }), zOrder: () => "bottom" as const }];
  }
}
```

> **v5 primitive API — verified present in `lightweight-charts@5.2.0`:** both extension points exist in the typings — `series.attachPrimitive(ISeriesPrimitive)` (`ISeriesPrimitiveBase.paneViews()` → `IPrimitivePaneView.renderer()` → `IPrimitivePaneRenderer`) for the diamond fills, and `pane.attachPrimitive(IPanePrimitive)` (`IPanePrimitiveBase.paneViews()`) for the session background. `createSeriesMarkers` is itself an `ISeriesPrimitiveWrapper`, confirming this is the blessed extension path. Confirm the exact `renderer.draw`/`useBitmapCoordinateSpace` scope field names against the installed d.ts during Step 1's check and adjust the thin adapter if 5.2.x differs; the pure geometry (Tasks 4–5) does not change. Fallback if `pane.attachPrimitive` proves awkward: attach `SessionShadingPrimitive` as a **series** primitive with `zOrder: "bottom"` instead — both draw behind the bars.

- [ ] **Step 3: Extend `PanelProps` and register the chart panel**

In `ui/src/chrome/panels/registry.tsx`:

```tsx
// add imports
import type { LinkGroups } from "../linkGroups";
import { ChartPanel } from "./ChartPanel";

// extend PanelProps
export interface PanelProps {
  config: PanelConfig;
  stores: Stores;
  scheduler: Scheduler;
  width: number;
  height: number;
  linkGroups: LinkGroups;
  commands: { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> };
  // Persist a panel's own settings (timeframe, indicators, …). AppShell updates the
  // workspace doc's matching panel entry and debounce-saves via WorkspaceStore.
  onConfigChange: (settings: Record<string, unknown>) => void;
}

// add to PANELS
  "chart": {
    component: ChartPanel,
    topics: ["md.bars", "md.indicator"],
  },
```

In `ui/src/chrome/PanelFrame.tsx` — thread the new props through (the frame gains `linkGroups`, `commands`, and `onConfigChange` params and forwards them):

```tsx
export function PanelFrame(
  { config, stores, scheduler, linkGroups, commands, onConfigChange }:
  { config: PanelConfig; stores: Stores; scheduler: Scheduler;
    linkGroups: LinkGroups; commands: PanelProps["commands"];
    onConfigChange: (settings: Record<string, unknown>) => void },
): JSX.Element {
  // ...existing size logic unchanged...
  const props: PanelProps = { config, stores, scheduler, width: size.width, height: size.height,
    linkGroups, commands, onConfigChange };
  // ...existing render unchanged...
}
```

In `ui/src/chrome/AppShell.tsx` — thread the props into each `PanelFrame`, and own the per-panel config persistence. `AppShell` holds the loaded `ws`, so it updates the matching panel's `settings` and debounce-saves via `WorkspaceStore` (the same store the layout auto-save already uses):

```tsx
// Props gains: linkGroups: LinkGroups; commands: PanelProps["commands"];
// A stable per-panel onConfigChange updates ws.panels[i].settings then saves.
const onConfigChange = (panelId: string, settings: Record<string, unknown>) => {
  const next = { ...ws, panels: ws.panels.map((p) => (p.id === panelId ? { ...p, settings } : p)) };
  setWs(next);                 // keep local state authoritative for subsequent edits
  workspaceStore.save(next);   // debounced persist (config key workspace.<name>)
};
// components mapping becomes:
      () => <PanelFrame config={p} stores={stores} scheduler={scheduler}
        linkGroups={linkGroups} commands={commands}
        onConfigChange={(settings) => onConfigChange(p.id, settings)} />,
```

In `ui/src/App.tsx` — pass them from the already-constructed `client` + `linkGroups`:

```tsx
// replace the `void linkGroups;` line and the <AppShell .../> usage:
  const commands = { sendCommand: (name: string, args: unknown) => client.sendCommand(name, args) };
  return (
    <ReconnectOverlay state={state}>
      <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
        workspaceStore={workspaceStore} linkGroups={linkGroups} commands={commands} />
    </ReconnectOverlay>
  );
```

> **Confirm `WsClient.sendCommand` signature** matches `(name, args) => Promise<{status; value?}>` (it is used the same way in `workspace.ts`); if the resolved shape differs, adapt the `commands` adapter here only.

- [ ] **Step 4: Write the ChartPanel**

```tsx
// ui/src/chrome/panels/ChartPanel.tsx
import { useEffect, useRef, useState } from "react";
import { createChart, CandlestickSeries, HistogramSeries, LineSeries, type IChartApi, type ISeriesApi, type Time } from "lightweight-charts";
import type { PanelProps } from "./registry";
import { ChartController } from "../../render/chart/ChartController";
import type { ChartApiFacade, LwcSeries } from "../../render/chart/ChartApiFacade";
import { DiamondFillPrimitive } from "../../render/chart/diamondPrimitive";
import { SessionShadingPrimitive } from "../../render/chart/sessionPrimitive";
import { withDefaultParams, type IndicatorInstance, type IndicatorType } from "../../render/chart/indicatorSeries";
import { ChartControls } from "./ChartControls";
import { useTheme } from "../ThemeProvider";

// Adapts a real LWC v5 IChartApi to the controller's minimal ChartApiFacade.
function makeFacade(chart: IChartApi, palette: Parameters<typeof DiamondFillPrimitive.prototype.setPalette>[0]): {
  facade: ChartApiFacade; setPalette: (p: typeof palette) => void;
} {
  let candle: ISeriesApi<"Candlestick"> | null = null;
  const session = new SessionShadingPrimitive(palette);
  const diamonds = new DiamondFillPrimitive(palette);

  const facade: ChartApiFacade = {
    addSeries: (kind, options, paneIndex) => {
      const s = kind === "candle" ? chart.addSeries(CandlestickSeries, options as object, paneIndex)
        : kind === "line" ? chart.addSeries(LineSeries, options as object, paneIndex)
        : chart.addSeries(HistogramSeries, options as object, paneIndex);
      if (kind === "candle") {
        candle = s as ISeriesApi<"Candlestick">;
        candle.attachPrimitive(diamonds);
        // Pane primitive on the main pane (index 0) for session shading:
        chart.panes()[0]?.attachPrimitive?.(session);
      }
      return s as unknown as LwcSeries;
    },
    removeSeries: (s) => chart.removeSeries(s as unknown as ISeriesApi<"Line">),
    setSessionBands: (bands) => session.setBands(bands),
    setFillMarkers: (m) => diamonds.setMarkers(m),
    timeToCoordinate: (ms) => chart.timeScale().timeToCoordinate((Math.floor(ms / 1000)) as unknown as Time),
    priceToCoordinate: (price) => candle?.priceToCoordinate(price) ?? null,
    scrollToRealTime: () => chart.timeScale().scrollToRealTime(),
    isAtRightEdge: () => {
      const r = chart.timeScale().scrollPosition();
      return r >= -1; // at/near the right edge (LWC scrollPosition 0 = latest bar at right)
    },
    resize: (w, h) => chart.resize(w, h),
    applyOptions: (o) => chart.applyOptions(o as object),
    remove: () => chart.remove(),
  };
  return { facade, setPalette: (p) => { session.setPalette(p); diamonds.setPalette(p); } };
}

export function ChartPanel({ config, stores, scheduler, width, height, linkGroups, commands, onConfigChange }: PanelProps): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const controllerRef = useRef<ChartController | null>(null);
  const setFacadePaletteRef = useRef<((p: typeof palette) => void) | null>(null);
  const idSeq = useRef(0);
  const { palette } = useTheme();
  const symbol = (config.settings.symbol as string) ?? "US.AAPL";
  const timeframe0 = (config.settings.timeframe as string) ?? "1m";

  // Config surfaces (timeframe + indicators) ARE low-rate chrome, so React state is
  // fine here (the hard rule is about market data, not per-chart config).
  const [timeframe, setTf] = useState(timeframe0);
  const [instances, setInstances] = useState<IndicatorInstance[]>(
    (config.settings.indicators as IndicatorInstance[]) ?? [],
  );

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const chart = createChart(host, { width, height });
    const { facade, setPalette } = makeFacade(chart, palette);
    setFacadePaletteRef.current = setPalette;
    const controller = new ChartController(facade, palette, { symbol, timeframe: timeframe0 },
      { bars: stores.bars, indicators: stores.indicators, commands });
    controller.mount();
    controllerRef.current = controller;

    // Restore persisted indicator instances (colors + params) saved with the workspace.
    for (const inst of instances) controller.addIndicator(inst);

    const applySymbol = () => controller.setSymbol(linkGroups.symbolFor(config.group) ?? symbol);
    applySymbol();
    const offLink = linkGroups.subscribe(applySymbol);

    // One Surface per chart: dirty if bars OR indicators changed (consume BOTH —
    // never short-circuit, or one store's flag would be left stuck).
    const off = scheduler.register({
      id: `chart:${config.id}`,
      isDirty: () => { const b = stores.bars.consumeDirty(); const i = stores.indicators.consumeDirty(); return b || i; },
      paint: () => controller.sync(),
    });

    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      controller.resize(Math.floor(r.width), Math.floor(r.height));
    });
    ro.observe(host);

    return () => { off(); offLink(); ro.disconnect(); controller.dispose(); controllerRef.current = null; };
    // symbol/timeframe/indicator changes are handled imperatively below — the chart
    // must never remount (canvas keeps its context).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [config.id]);

  // Theme switch: re-apply palette to chart, series and the custom primitives.
  useEffect(() => {
    controllerRef.current?.setPalette(palette);
    setFacadePaletteRef.current?.(palette);
  }, [palette]);

  // ---- config mutations: drive the controller imperatively, then persist ----
  const persist = (patch: Record<string, unknown>) => onConfigChange({ ...config.settings, timeframe, indicators: instances, ...patch });

  const changeTimeframe = (tf: string) => { setTf(tf); controllerRef.current?.setTimeframe(tf); persist({ timeframe: tf }); };
  const addIndicator = (type: IndicatorType) => {
    const inst: IndicatorInstance = { instanceId: `${type}-${idSeq.current++}`, type, params: withDefaultParams(type) };
    const next = [...instances, inst];
    setInstances(next); controllerRef.current?.addIndicator(inst); persist({ indicators: next });
  };
  const updateIndicator = (inst: IndicatorInstance) => {
    const next = instances.map((i) => (i.instanceId === inst.instanceId ? inst : i));
    setInstances(next); controllerRef.current?.updateIndicator(inst); persist({ indicators: next });
  };
  const removeIndicator = (id: string) => {
    const next = instances.filter((i) => i.instanceId !== id);
    setInstances(next); controllerRef.current?.removeIndicator(id); persist({ indicators: next });
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <ChartControls timeframe={timeframe} instances={instances} palette={palette}
        onTimeframe={changeTimeframe} onAdd={addIndicator} onUpdate={updateIndicator} onRemove={removeIndicator} />
      <div ref={hostRef} style={{ flex: 1, minHeight: 0, position: "relative" }} />
    </div>
  );
}
```

The controls are a real per-chart manager (timeframe + full indicator add/remove/edit), generated from `INDICATOR_CATALOG` so every param and every slot color is editable:

```tsx
// ui/src/chrome/panels/ChartControls.tsx  (or colocate in ChartPanel.tsx)
import { INDICATOR_CATALOG, describeIndicator, type IndicatorInstance, type IndicatorType } from "../../render/chart/indicatorSeries";
import type { Palette } from "../../render/palette";

const TFS: string[] = ["10s", "1m", "5m", "15m", "30m", "60m", "D", "W", "M"];
const TYPES = Object.keys(INDICATOR_CATALOG) as IndicatorType[];

export function ChartControls({ timeframe, instances, palette, onTimeframe, onAdd, onUpdate, onRemove }: {
  timeframe: string; instances: IndicatorInstance[]; palette: Palette;
  onTimeframe: (tf: string) => void; onAdd: (t: IndicatorType) => void;
  onUpdate: (i: IndicatorInstance) => void; onRemove: (id: string) => void;
}): JSX.Element {
  return (
    <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 6, padding: "3px 6px",
      background: palette.surface, borderBottom: `1px solid ${palette.border}`, color: palette.text, fontSize: 12 }}>
      <select aria-label="timeframe" value={timeframe} onChange={(e) => onTimeframe(e.target.value)}>
        {TFS.map((tf) => <option key={tf} value={tf}>{tf}</option>)}
      </select>
      <select aria-label="add indicator" value="" onChange={(e) => { if (e.target.value) onAdd(e.target.value as IndicatorType); }}>
        <option value="">+ indicator</option>
        {TYPES.map((t) => <option key={t} value={t}>{INDICATOR_CATALOG[t].label}</option>)}
      </select>
      {instances.map((inst) => <InstanceChip key={inst.instanceId} inst={inst} palette={palette} onUpdate={onUpdate} onRemove={onRemove} />)}
    </div>
  );
}

function InstanceChip({ inst, palette, onUpdate, onRemove }: {
  inst: IndicatorInstance; palette: Palette; onUpdate: (i: IndicatorInstance) => void; onRemove: (id: string) => void;
}): JSX.Element {
  const entry = INDICATOR_CATALOG[inst.type];
  const setParam = (k: string, v: number) => onUpdate({ ...inst, params: { ...inst.params, [k]: v } });
  const setColor = (slot: string, c: string) => onUpdate({ ...inst, colors: { ...inst.colors, [slot]: c } });
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "1px 5px",
      border: `1px solid ${palette.border}`, borderRadius: 3 }}>
      <b style={{ fontWeight: 600 }}>{entry.label}</b>
      {entry.params.map((p) => (
        <label key={p.key} style={{ color: palette.textMuted }}>{p.label[0]}
          <input type="number" min={p.min} max={p.max} value={inst.params[p.key] ?? p.default}
            onChange={(e) => setParam(p.key, Number(e.target.value))} style={{ width: 42, marginLeft: 2 }} />
        </label>
      ))}
      {describeIndicator(inst, palette).map((d) => (
        <input key={d.slot} aria-label={`${inst.instanceId} ${d.slot} color`} type="color" value={d.color}
          onChange={(e) => setColor(d.slot, e.target.value)} style={{ width: 18, height: 16, padding: 0, border: "none", background: "none" }} />
      ))}
      <button aria-label={`remove ${inst.instanceId}`} onClick={() => onRemove(inst.instanceId)}
        style={{ color: palette.textMuted, border: "none", background: "none", cursor: "pointer" }}>×</button>
    </span>
  );
}
```

> **Imports to add at the top of `ChartPanel.tsx`:** `useState` (from react), `withDefaultParams`, `type IndicatorInstance`, `type IndicatorType` (from `indicatorSeries`), and `ChartControls`. The `type="color"` input value must be a 6-digit hex — the palette's indicator defaults already are; the color picker only overrides, so `describeIndicator(...).color` (override ?? palette default) is a valid hex to seed it.

- [ ] **Step 5: Write the ChartPanel test (LWC mocked)**

```tsx
// ui/src/chrome/panels/ChartPanel.test.tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";

// Mock lightweight-charts so the panel test never touches a real canvas.
const chartApi = {
  addSeries: vi.fn(() => ({ setData: vi.fn(), update: vi.fn(), applyOptions: vi.fn(),
    attachPrimitive: vi.fn(), priceToCoordinate: vi.fn(() => 0) })),
  removeSeries: vi.fn(),
  panes: vi.fn(() => [{ attachPrimitive: vi.fn() }]),
  timeScale: vi.fn(() => ({ timeToCoordinate: vi.fn(() => 0), scrollToRealTime: vi.fn(), scrollPosition: vi.fn(() => 0) })),
  applyOptions: vi.fn(), resize: vi.fn(), remove: vi.fn(),
};
vi.mock("lightweight-charts", () => ({
  createChart: vi.fn(() => chartApi),
  CandlestickSeries: "Candlestick", HistogramSeries: "Histogram", LineSeries: "Line", CrosshairMode: { Magnet: 1 },
}));

import { ChartPanel } from "./ChartPanel";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";

beforeEach(() => { vi.clearAllMocks(); cleanup(); });

function renderChart() {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const commands = { sendCommand: vi.fn(async () => ({ status: "accepted" })) };
  const config = { id: "c1", panelId: "chart", group: "green" as const, settings: { symbol: "US.AAPL", timeframe: "1m" } };
  return render(
    <ThemeProvider>
      <ChartPanel config={config} stores={stores} scheduler={scheduler} width={400} height={300}
        linkGroups={linkGroups} commands={commands} onConfigChange={vi.fn()} />
    </ThemeProvider>,
  );
}

describe("ChartPanel", () => {
  it("creates a chart and registers candle + volume series on mount", async () => {
    const { createChart } = await import("lightweight-charts");
    renderChart();
    expect(createChart).toHaveBeenCalledTimes(1);
    expect(chartApi.addSeries).toHaveBeenCalled(); // candle + volume
  });

  it("removes the chart on unmount", () => {
    const { unmount } = renderChart();
    unmount();
    expect(chartApi.remove).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 6: Run the whole suite + typecheck + lint**

Run: `cd ui && npx vitest run && npm run typecheck && npm run lint`
Expected: PASS across the board. Resolve any type gaps in the facade adapter against the installed LWC d.ts.

- [ ] **Step 7: Commit**

```bash
git add ui/package.json ui/package-lock.json ui/src/render/chart/diamondPrimitive.ts ui/src/render/chart/sessionPrimitive.ts ui/src/chrome/panels/ChartPanel.tsx ui/src/chrome/panels/ChartControls.tsx ui/src/chrome/panels/ChartPanel.test.tsx ui/src/chrome/panels/registry.tsx ui/src/chrome/PanelFrame.tsx ui/src/chrome/AppShell.tsx ui/src/App.tsx
git commit -m "feat(ui/chart): ChartPanel — LWC v5 chart with diamond + session primitives, link-following"
```

---

## Task 10: Theme provider + workspace header + sweep Plan 1 inline hex onto the palette

**Files:**
- Create: `ui/src/chrome/ThemeProvider.tsx`
- Test: `ui/src/chrome/ThemeProvider.test.tsx`
- Create: `ui/src/chrome/WorkspaceHeader.tsx`
- Test: `ui/src/chrome/WorkspaceHeader.test.tsx`
- Modify: `ui/src/chrome/AppShell.tsx`, `ui/src/chrome/PanelFrame.tsx`, `ui/src/chrome/ReconnectOverlay.tsx`, `ui/src/chrome/panels/SmokePainterPanel.tsx`, `ui/src/App.tsx`

**Interfaces:**
- Consumes: `getPalette`, `ThemeMode`, `Palette` (Task 1); the config store via a `sendCommand`-style client (config keys `GetConfig`/`SetConfig`, key `theme`) — reuse the same `commands` adapter.
- Produces: `ThemeProvider` (React context; `useTheme(): { palette: Palette; mode: ThemeMode; setMode(m): void }`), `WorkspaceHeader` (symbol-focus box per group + theme toggle).

- [ ] **Step 1: Write the ThemeProvider test**

```tsx
// ui/src/chrome/ThemeProvider.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider, useTheme } from "./ThemeProvider";

function Probe() {
  const { mode, palette, setMode } = useTheme();
  return (
    <div>
      <span data-testid="mode">{mode}</span>
      <span data-testid="bg">{palette.bg}</span>
      <button onClick={() => setMode(mode === "light" ? "dark" : "light")}>toggle</button>
    </div>
  );
}

describe("ThemeProvider", () => {
  it("defaults to light", () => {
    render(<ThemeProvider><Probe /></ThemeProvider>);
    expect(screen.getByTestId("mode").textContent).toBe("light");
  });

  it("loads the persisted mode from the config store", async () => {
    const commands = { sendCommand: vi.fn(async (n: string) =>
      n === "GetConfig" ? { status: "accepted", value: "dark" } : { status: "accepted" }) };
    render(<ThemeProvider commands={commands}><Probe /></ThemeProvider>);
    await waitFor(() => expect(screen.getByTestId("mode").textContent).toBe("dark"));
  });

  it("toggling persists via SetConfig and swaps the palette", async () => {
    const commands = { sendCommand: vi.fn(async () => ({ status: "accepted" })) };
    render(<ThemeProvider commands={commands}><Probe /></ThemeProvider>);
    fireEvent.click(screen.getByText("toggle"));
    await waitFor(() => expect(screen.getByTestId("mode").textContent).toBe("dark"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "theme", value: "dark" });
  });
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && npx vitest run src/chrome/ThemeProvider.test.tsx`
Expected: FAIL — `Cannot find module './ThemeProvider'`.

- [ ] **Step 3: Write the ThemeProvider**

```tsx
// ui/src/chrome/ThemeProvider.tsx
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getPalette, type Palette, type ThemeMode } from "../render/palette";

interface Commands { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }
interface ThemeCtx { mode: ThemeMode; palette: Palette; setMode(m: ThemeMode): void }

const Ctx = createContext<ThemeCtx | null>(null);

export function ThemeProvider({ commands, children }: { commands?: Commands; children: ReactNode }): JSX.Element {
  const [mode, setModeState] = useState<ThemeMode>("light"); // light is the app default

  useEffect(() => {
    if (!commands) return;
    void commands.sendCommand("GetConfig", { key: "theme" }).then((ack) => {
      if (ack.status === "accepted" && (ack.value === "dark" || ack.value === "light")) setModeState(ack.value);
    });
  }, [commands]);

  const setMode = (m: ThemeMode) => {
    setModeState(m);
    void commands?.sendCommand("SetConfig", { key: "theme", value: m });
  };

  const value = useMemo<ThemeCtx>(() => ({ mode, palette: getPalette(mode), setMode }), [mode]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTheme(): ThemeCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useTheme must be used within a ThemeProvider");
  return ctx;
}
```

- [ ] **Step 4: Run the ThemeProvider test to verify it passes**

Run: `cd ui && npx vitest run src/chrome/ThemeProvider.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Write the WorkspaceHeader test + implementation**

```tsx
// ui/src/chrome/WorkspaceHeader.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { WorkspaceHeader } from "./WorkspaceHeader";
import { LinkGroups, BroadcastChannelBus } from "./linkGroups";

describe("WorkspaceHeader", () => {
  it("typing a symbol into a group box focuses that link group", () => {
    const echo = vi.fn();
    const lg = new LinkGroups(new BroadcastChannelBus(), echo);
    render(<ThemeProvider><WorkspaceHeader workspaceName="trading" linkGroups={lg} /></ThemeProvider>);
    const box = screen.getByLabelText("focus green");
    fireEvent.change(box, { target: { value: "US.NVDA" } });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(lg.symbolFor("green")).toBe("US.NVDA");
    expect(echo).toHaveBeenCalledWith("green", "US.NVDA");
  });

  it("renders a theme toggle", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    render(<ThemeProvider><WorkspaceHeader workspaceName="trading" linkGroups={lg} /></ThemeProvider>);
    expect(screen.getByRole("button", { name: /theme/i })).toBeTruthy();
  });
});
```

```tsx
// ui/src/chrome/WorkspaceHeader.tsx
import { useState } from "react";
import { useTheme } from "./ThemeProvider";
import { LinkGroups, type LinkGroup } from "./linkGroups";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];

// Minimal v1 header: one type-to-focus symbol box per link group + a theme toggle.
export function WorkspaceHeader({ workspaceName, linkGroups }: { workspaceName: string; linkGroups: LinkGroups }): JSX.Element {
  const { palette, mode, setMode } = useTheme();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "4px 10px",
      background: palette.surface, borderBottom: `1px solid ${palette.border}`, color: palette.text, fontSize: 12 }}>
      <strong style={{ textTransform: "capitalize" }}>{workspaceName}</strong>
      {GROUPS.map((g) => <GroupBox key={g} group={g} linkGroups={linkGroups} palette={palette} />)}
      <span style={{ flex: 1 }} />
      <button aria-label="toggle theme" onClick={() => setMode(mode === "light" ? "dark" : "light")}>
        {mode === "light" ? "◐ dark theme" : "◑ light theme"}
      </button>
    </div>
  );
}

function GroupBox({ group, linkGroups, palette }: { group: Exclude<LinkGroup, null>; linkGroups: LinkGroups; palette: import("../render/palette").Palette }): JSX.Element {
  const [text, setText] = useState("");
  const swatch = { red: palette.linkRed, green: palette.linkGreen, blue: palette.linkBlue, yellow: palette.linkYellow }[group];
  return (
    <span style={{ display: "flex", alignItems: "center", gap: 4 }}>
      <span style={{ width: 8, height: 8, borderRadius: 2, background: swatch }} />
      <input aria-label={`focus ${group}`} value={text} placeholder="symbol"
        onChange={(e) => setText(e.target.value.toUpperCase())}
        onKeyDown={(e) => { if (e.key === "Enter" && text.trim()) linkGroups.focus(group, text.trim()); }}
        style={{ width: 84, fontSize: 12 }} />
    </span>
  );
}
```

- [ ] **Step 6: Sweep Plan 1's inline hex onto the palette (light default)**

Replace every hardcoded color in the shell with palette values, and switch the dockview theme to light by default:

- `ui/src/chrome/panels/SmokePainterPanel.tsx`: take `palette` from `useTheme()`; replace `#0F1115` → `palette.bg`, `#e2e8f0` → `palette.text`. (This panel is a temporary stack-proof; still, no stray hex.)
- `ui/src/chrome/PanelFrame.tsx`: the header `background: "#141821"` → `palette.surface`; `borderBottom "#1f2430"` → `palette.border`; the "coming in a later plan" `color: "#64748b"` → `palette.textMuted`. The `swatch()` map already lists link colors — source them from the palette (`palette.linkRed/Green/Blue/Yellow`) via `useTheme()`.
- `ui/src/chrome/ReconnectOverlay.tsx`: any inline colors → palette.
- `ui/src/chrome/AppShell.tsx`: replace `className="dockview-theme-dark"` with a mode-driven class — `dockview-theme-light` when `mode === "light"`, else `dockview-theme-dark` (import both are provided by dockview's CSS; the base CSS import stays). Render `<WorkspaceHeader .../>` above the `<DockviewReact/>` inside a flex column.
- `ui/src/App.tsx`: wrap `<AppShell/>` in `<ThemeProvider commands={commands}>`; the loading text color → palette (or leave neutral).

For each file, after editing, confirm no literal `#` hex remains except inside `render/palette.ts`:
Run: `cd ui && grep -rnE "#[0-9a-fA-F]{3,6}" src --include="*.tsx" --include="*.ts" | grep -v "render/palette.ts"`
Expected: no matches (or only clearly-neutral ones you consciously keep — the goal is palette is the single source of truth).

- [ ] **Step 7: Run the full suite + typecheck + lint**

Run: `cd ui && npx vitest run && npm run typecheck && npm run lint`
Expected: PASS. Update any Plan 1 shell test that asserted a specific dark hex to read from the palette instead.

- [ ] **Step 8: Commit**

```bash
git add ui/src/chrome/ThemeProvider.tsx ui/src/chrome/ThemeProvider.test.tsx ui/src/chrome/WorkspaceHeader.tsx ui/src/chrome/WorkspaceHeader.test.tsx ui/src/chrome/AppShell.tsx ui/src/chrome/PanelFrame.tsx ui/src/chrome/ReconnectOverlay.tsx ui/src/chrome/panels/SmokePainterPanel.tsx ui/src/App.tsx
git commit -m "feat(ui/chrome): theme provider + workspace header; sweep shell colors onto the light-default palette"
```

---

## Task 11: Chart fixtures + mock engine + dev-app verification

**Files:**
- Create: `ui/fixtures/chart-session.json`
- Modify: `ui/mock-engine/run.ts` (serve the chart fixture for the dev app)
- Create/Modify: a fixture-validation test asserting the fixture's bars follow `bucketStartMs`

**Interfaces:**
- Consumes: the `Fixture` shape (mock-engine/server.ts), `bucketStartMs` (Task 3).

- [ ] **Step 1: Write the chart fixture**

A realistic AAPL 1m session: a backfill snapshot (cold-open depth), then in-progress updates to the last bar, a finalize, a new bucket, and a `gap` bar; plus a VWAP indicator instance streamed on `md.indicator` keyed by its instanceId.

```json
// ui/fixtures/chart-session.json
{
  "snapshots": [
    { "topic": "md.bars", "key": "US.AAPL:1m", "payload": [
      { "symbol": "US.AAPL", "timeframe": "1m", "bucketStart": "2026-07-06T13:30:00Z", "o": 3.40, "h": 3.55, "l": 3.38, "c": 3.50, "v": 12000, "inProgress": false },
      { "symbol": "US.AAPL", "timeframe": "1m", "bucketStart": "2026-07-06T13:31:00Z", "o": 3.50, "h": 3.60, "l": 3.49, "c": 3.58, "v": 9000,  "inProgress": false },
      { "symbol": "US.AAPL", "timeframe": "1m", "bucketStart": "2026-07-06T13:32:00Z", "o": 3.58, "h": 3.59, "l": 3.52, "c": 3.53, "v": 7000,  "inProgress": true }
    ] },
    { "topic": "md.indicator", "key": "seed-vwap", "payload": [
      { "timeMs": 1783776600000, "value": 3.48 },
      { "timeMs": 1783776660000, "value": 3.52 },
      { "timeMs": 1783776720000, "value": 3.54 }
    ] }
  ],
  "deltas": [
    { "afterMs": 80,  "topic": "md.bars", "key": "US.AAPL:1m", "payload": { "symbol": "US.AAPL", "timeframe": "1m", "bucketStart": "2026-07-06T13:32:00Z", "o": 3.58, "h": 3.61, "l": 3.52, "c": 3.60, "v": 8200, "inProgress": true } },
    { "afterMs": 160, "topic": "md.bars", "key": "US.AAPL:1m", "payload": { "symbol": "US.AAPL", "timeframe": "1m", "bucketStart": "2026-07-06T13:32:00Z", "o": 3.58, "h": 3.62, "l": 3.52, "c": 3.61, "v": 9100, "inProgress": false } },
    { "afterMs": 240, "topic": "md.bars", "key": "US.AAPL:1m", "payload": { "symbol": "US.AAPL", "timeframe": "1m", "bucketStart": "2026-07-06T13:33:00Z", "o": 3.61, "h": 3.64, "l": 3.60, "c": 3.63, "v": 4000, "inProgress": true } },
    { "afterMs": 320, "topic": "md.indicator", "key": "seed-vwap", "payload": { "timeMs": 1783776780000, "value": 3.57 } }
  ]
}
```

> Note: `md.bars` snapshots/deltas carry a `key` of `SYMBOL:TIMEFRAME` (mirrors `BarStore`'s internal keying). The mock engine already forwards `key` verbatim; `BarStore.apply` reads symbol/timeframe from the payload, so the key is informational here — but keep it, because the real engine keys per (symbol, timeframe) and the tygo contract will formalize it. The VWAP instance is streamed under `seed-vwap`; when a real chart calls `addIndicator`, its own `instanceId` is used — this fixture proves the topic plumbing and the store, and lets the dev app show a VWAP line if the chart pre-registers a `seed-vwap` instance (documented in Step 3).

- [ ] **Step 2: Add a fixture-validation test**

```ts
// ui/fixtures/chart-session.test.ts  (or colocate under src/ if fixtures/ is excluded from the test glob — check vitest.config.ts include globs)
import { describe, it, expect } from "vitest";
import fixture from "./chart-session.json";
import { bucketStartMs } from "../src/render/chart/barBucket";

describe("chart-session fixture", () => {
  it("every md.bars bucketStart is consistent with the engine-mirror bucketing", () => {
    const bars = (fixture.snapshots.find((s) => s.topic === "md.bars")!.payload as Array<{ bucketStart: string; timeframe: string }>);
    for (const b of bars) {
      const ms = Date.parse(b.bucketStart);
      expect(bucketStartMs(ms, b.timeframe as "1m")).toBe(ms);
    }
  });
});
```

> If `vitest.config.ts`'s `include` does not cover `fixtures/**`, place this test at `ui/src/fixtures-chart-session.test.ts` importing `../fixtures/chart-session.json`. Verify the config's include glob before choosing the path.

- [ ] **Step 3: Point the dev mock engine at the chart fixture**

In `ui/mock-engine/run.ts`, load `chart-session.json` (merge its snapshots/deltas with the existing `session-basic.json` so quote/health/events still flow, or select via an env var / CLI arg). Keep `session-basic.json` as the default for the Plan 1 tests untouched. Document the run command in a comment.

- [ ] **Step 4: Run the full suite + typecheck + lint**

Run: `cd ui && npx vitest run && npm run typecheck && npm run lint`
Expected: PASS.

- [ ] **Step 5: Manual dev-app verification (the honest substitute for goldens, which land in Plan 3)**

Run two terminals:
```bash
cd ui && npm run mock-engine        # serves chart-session fixture on the dev WS port
cd ui && npm run dev                # Vite dev server
```
Open the trading workspace (`http://localhost:5173/?workspace=trading`) and confirm by eye:
- [ ] All four chart panels render candles + volume with the **light** palette (default).
- [ ] The last (in-progress) bar updates in place as deltas arrive, then finalizes; a new bar appends; auto-follow keeps the right edge in view.
- [ ] ET session shading is visible (pre/post tinted, RTH clear) behind the bars.
- [ ] A VWAP line renders (if the dev chart pre-registers a `seed-vwap` instance) tracking the indicator deltas.
- [ ] Toggling the theme (workspace header button) flips every chart + all chrome to the dark palette and back, with no remount/flicker of the charts.
- [ ] Typing a symbol into the green group box in the header re-points the green-group charts to that symbol (cold symbol shows the "growing from subscribe time" hint on sub-minute timeframes, not an error).
- [ ] Changing a chart's timeframe dropdown re-backfills that chart.
- [ ] From the chart's `+ indicator` menu: add an EMA → line appears; edit its period → line recomputes; change its color → line recolors in place (no blink); add MACD → sub-pane appears with three independently-colorable series; remove an indicator → its series disappear. Reload the window → the indicators (params + colors) restore from the saved workspace.
- [ ] Kill one chart's paint (temporarily throw in `sync`) → only that panel shows the error card; the others keep updating (painter isolation still holds).

Record the result of this checklist in the commit message or PR description.

- [ ] **Step 6: Commit**

```bash
git add ui/fixtures/chart-session.json ui/mock-engine/run.ts ui/fixtures/chart-session.test.ts
git commit -m "feat(ui/chart): chart-session fixture + mock-engine wiring + fixture-bucketing validation"
```

---

## Task 12: Integration sweep + plan close-out

**Files:** none new — verification + any small fixes surfaced.

- [ ] **Step 1: Full verification**

Run: `cd ui && npm run build && npx vitest run && npm run lint`
Expected: production build succeeds (`typecheck` runs inside `build`), all tests pass, lint clean. `npm run build` also proves the LWC import tree tree-shakes and the chart panel compiles for production.

- [ ] **Step 2: Confirm both seed workspaces render charts**

The seeds already reference `panelId: "chart"` (Plan 1, `seeds/workspaces.ts`). With the panel now registered, both workspaces show real charts (monitoring: 4 pinned symbols; trading: 4 timeframes of the green group). Verify via the dev app (both `?workspace=` values).

- [ ] **Step 3: Commit any fixes + finalize**

```bash
git add -A ui/
git commit -m "chore(ui/chart): Plan 2 integration sweep — charts live in both workspaces"
```

---

## Self-Review (run against the roadmap's Plan-2 scope + the UI spec's Chart panel spec)

**Spec coverage** (ui-design §Chart panel spec + Plan-1 roadmap item 2):
- LWC v5 unforked, candles + volume + MACD sub-pane → Tasks 2, 7, 8, 9 (MACD descriptor → pane 1).
- Do NOT port wickplot viewport/axis math → honored (no `BarWindow`/`ChartViewport`/`priceGrid`/`niceAxisStep`; LWC owns it).
- Custom diamond fill-marker plugin (`drawDiamond`/`hitTestDiamond`, 0.8 factor, borderWidth) → Tasks 5 (pure port) + 9 (`diamondPrimitive`).
- Bar-bucketing test-mirror of the engine → Task 3.
- Indicator instances (VWAP/EMA/SMA/MACD/volume/buy-sell delta) as streamed series → Tasks 6 (store), 7 (descriptors), 8 (subscribe command + feed).
- ET session shading → Tasks 4 (bands) + 9 (`sessionPrimitive`).
- Interaction conventions (crosshair snap, cursor-anchored wheel zoom, drag pan, jump-to-live) → Task 2 (magnet crosshair) + Task 8 (`jumpToLive`, auto-follow-only-at-right-edge); wheel-zoom/drag-pan are LWC defaults (no code needed — verify in Task 11 checklist).
- Cold-symbol / in-progress-bar states → Task 8 (empty-series no-op; in-progress upsert already in `BarStore`) + Task 11 checklist (cold hint).
- Palette: new eTape palette, light default + dark toggle, single source of truth, painters receive it in paint state → Tasks 1, 2, 10; `setPalette` on the controller + primitives.
- Live bar handling / burst tolerance → `BarStore` (Plan 1) + `ChartController.applyBars` (setData-then-update) → Task 8.
- Fills-on-chart live wiring: the plugin + its rendering land here (Tasks 5, 9); **live exec fills stream in Plan 5** (`ExecStore.fills` → `controller.setFills`). Flagged, not silently dropped.

**Cross-plan handoffs left explicit:**
- Golden-image harness (node-canvas, pixel-diff) is Plan 3 — Plan 2 verifies via pure unit tests + fake-facade controller tests + the Task 11 dev-app checklist. Stated in the Tech Stack line and Task 11.
- `controller.setFills(...)` is built and unit-tested; the live `ExecStore.fills → chart` wire is Plan 5.
- Indicator customization is **delivered in Plan 2**, per the UI spec ("add/remove instances, periods, colors"): the catalog (Task 7) drives a real per-chart manager (Task 9 `ChartControls`) — add/remove any catalog type, edit every param, and set a color per drawable slot (including each of MACD's three series). Params flow to the engine via `SubscribeIndicator`; a color-only edit re-applies in place without re-subscribing (`ChartController.updateIndicator`). Timeframe + indicator config persist with the workspace via the new per-panel `onConfigChange` → `WorkspaceStore` path (Tasks 9–10).

**Placeholder scan:** the only intentional elision is the `controllerRef` + timeframe-persistence wiring in Task 9 Step 4 — explicitly called out with "do not ship the elision" and instructions to implement it for real. No `TBD`/"add error handling"/"similar to Task N" anywhere.

**Type consistency:** `Palette` keys (Task 1) are used verbatim in `chartTheme` (2), `diamondMarker.fillColor` (5), `indicatorSeries` (7), `sessionPrimitive`/`diamondPrimitive` (9), and the sweep (10). `Timeframe` (3) flows into `ChartController`. `FillMarker` (5) → `ChartApiFacade.setFillMarkers` → `diamondPrimitive`. `IndicatorInstance`/`SeriesDescriptor` (7) → `ChartController` (8). `SnapshotMsg`/`DeltaMsg` (contract) → `IndicatorStore` (6). `PanelProps` extension (9) is threaded App → AppShell → PanelFrame → panel consistently.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-04-ui-charting.md`.**

Before executing, one thing needs your call (the roadmap's "decide the palette with Earl"): **Task 1 ships a concrete proposed palette** — clean light-default with a dark variant reusing Plan 1's tones. The interface and how it threads through paint state are fixed; the hex values are yours to confirm or adjust. Say the word if you want to tune them (I can bring in the `frontend-design` skill for a more distinctive direction) or run with the proposal.

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — execute tasks in this session using `executing-plans`, batch execution with checkpoints for review.

Which approach?
