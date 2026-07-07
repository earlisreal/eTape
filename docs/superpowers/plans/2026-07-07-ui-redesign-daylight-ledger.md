# eTape UI Redesign — "Daylight Ledger" Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the (architecturally sound) eTape UI a deliberate "Daylight Ledger" visual identity — self-hosted IBM Plex fonts, a re-baked warm-paper/ink palette with bronze reserved for all state, a real CSS layer, a blank-start panel catalog with presets, on-panel symbol linking + type-to-load, and re-presented panels (two-column DOM, full-row tape, merged Account, sortable tables) — with **zero engine changes**.

**Architecture:** `palette.ts` stays the single source of truth for color/type; at theme load its values are mirrored into CSS custom properties on `:root`, and a real `global.css` defines base typography, shared control classes, and panel-frame classes that consume those variables. React chrome moves from per-component inline `style={{}}` objects to those classes; canvas painters keep receiving the resolved `Palette` object directly (they never read CSS vars). The seeded-workspace startup is replaced by a blank workspace + catalog; the old seeds become catalog **presets**. Symbol-group control moves from four top-bar boxes onto each panel header (swatch + picker + inline type-to-load).

**Tech Stack:** TypeScript + React 18, Vite, dockview v4, TradingView Lightweight Charts v5 (chart only), node-canvas + pngjs + pixelmatch (golden-image tests), Vitest (unit/component), Playwright (E2E). No new runtime dependencies beyond self-hosted font files.

## Global Constraints

Every task's requirements implicitly include these. Values are copied verbatim from the spec (`docs/superpowers/specs/2026-07-07-ui-redesign-design.md`).

- **No engine work.** All changes ride existing topics (`sys.health`, `exec.*`, `md.*`, `scanner.*`, `news.item`) and existing config CRUD (`sendCommand("GetConfig"|"SetConfig", { key, value })`). Do not add or modify engine code, wire message types, or topics.
- **Color discipline:** green (`up`) / red (`down`) are reserved **exclusively** for market direction (candles, bid/ask, buy/sell prints, P&L). All *state* — focus ring, armed chip, in-flight/working orders, scanner hits, today's news, degraded latency — uses **bronze** (`accent`). Kill switch and rejects use a distinct **danger red** (`danger`). Connection-link status dots are the one documented exception (ok green / degraded bronze / bad red), matching the approved mockup.
- **Light is the default theme.** Dark stays a first-class theme via the Settings toggle; **golden tests run both modes** and both must pass.
- **Palette values (light, verbatim):** `bg #FBFAF7`, `surface #F2F0EA`, `border #DDD9CF`, `borderStrong #C9C4B8` (new field), `text #171A1E`, `textMuted #6A7280`, `up #177A58`, `down #C2334D`, `accent #9A6A1B` (bronze), `danger #A81E30`.
- **Type roles:** IBM Plex **Serif** (500/600) — panel titles, section labels, wordmark. IBM Plex **Sans** (400/500/600) — controls, chips, body chrome. IBM Plex **Mono** (400/500/600) — every number and symbol (tape, ladder, prices, axes, timestamps, filter summaries), with `font-variant-numeric: tabular-nums`. Fonts are **self-hosted** with `@font-face`; **layout must not depend on Plex metrics** — system fallbacks (`ui-monospace`, `system-ui`, `serif`) must keep the layout intact if fonts fail to load.
- **Painter contract unchanged:** canvas painters receive a `Palette` in their paint state; they never read a global or a CSS var. `getPalette(mode)` signature is unchanged.
- **High-frequency data never flows through React state.** Chart/ladder/tape stay canvas surfaces painted imperatively via the `Scheduler`, coalesced to one repaint per rAF tick. Panels keep stable React keys (no remount on drag) and ResizeObserver-driven canvas resize.
- **Persistence unchanged in mechanism:** every layout move/resize/add/close and every panel-config change (symbol, group, timeframe, filters, **sort**) debounce-saves the workspace document engine-side via `WorkspaceStore` → `SetConfig`; reload restores exactly. Theme (`theme`), order config (`orderConfig`), sound config (`soundConfig`) keep their existing keys. Previously-saved `workspace.<name>` documents must keep loading.
- **Test pools:** Vitest default environment is `node`; files that mount a real `<canvas>` or use node-canvas (golden tests, `LadderPanel`/`TapePanel` component tests, and any **new** canvas-touching test file) MUST be added to `vitest.config.ts` `poolMatchGlobs` with the `"forks"` pool, or they crash with node-canvas's "Module did not self-register". E2E `.spec.ts` live under `e2e/` and are excluded from Vitest.
- **Commit messages:** body only — **never** add a `Co-Authored-By:` / "Generated with" / AI-attribution trailer (per the repo owner's global instruction).

---

## Task ordering & dependency map

Foundation first (everything inherits it), then shell scaffolding, shell UI, panel frame, data-surface painters, tables/panels, E2E.

- **Phase 0 — Visual foundation:** T1 fonts → T2 palette re-bake (+ regenerate all goldens) → T3 CSS layer.
- **Phase 1 — Shell model:** T4 extract `normalizeSymbol` → T5 window/workspace model → T6 registry catalog metadata → T7 presets module.
- **Phase 2 — Shell UI:** T8 latency readout → T9 top bar → T10 empty state + catalog + add/remove wiring → T11 Settings modal.
- **Phase 3 — Panel frame:** T12 ledger header + group swatch picker + focus ring → T13 type-to-load.
- **Phase 4 — Painters:** T14 two-column DOM ladder → T15 full-row tape.
- **Phase 5 — Tables & panels:** T16 sortable-columns utility → T17 Scanner/Movers → T18 News → T19 Account merge → T20 Open Orders → T21 Order Ticket + ChartControls → T22 Connection Status.
- **Phase 6 — E2E:** T23 Playwright updates.

T2 must run before any component/painter task (it changes every color). T3 before every restyle task. T16 (sortable utility) before T17/T19/T20. T6+T7 before T10. T4 before T9/T13.

---

# Phase 0 — Visual foundation

## Task 1: Self-host IBM Plex fonts

**Files:**
- Create: `ui/public/fonts/` — the eight woff2 files (see step 1)
- Create: `ui/src/fonts.css`
- Modify: `ui/src/render/palette.ts:144-150` (add `serif` to `FONTS`)
- Modify: `ui/src/main.tsx` (import `fonts.css`)
- Modify: `ui/index.html` (preload the four hot weights)
- Test: `ui/src/render/palette.test.ts` (assert `FONTS.serif`)

**Interfaces:**
- Produces: `FONTS.serif: string` (used by Task 3's CSS and any serif chrome); self-hosted `@font-face` families `"IBM Plex Serif"`, `"IBM Plex Sans"`, `"IBM Plex Mono"` available app-wide.

- [ ] **Step 1: Add the woff2 files**

Obtain Latin-subset woff2 for IBM Plex (OFL-licensed) and place them in `ui/public/fonts/` with these exact names (copy from the `@fontsource/ibm-plex-*` packages' `files/*-latin-<wght>-normal.woff2`, or the IBM Plex GitHub release, then rename):

```
ui/public/fonts/IBMPlexSerif-Medium.woff2      (500)
ui/public/fonts/IBMPlexSerif-SemiBold.woff2    (600)
ui/public/fonts/IBMPlexSans-Regular.woff2      (400)
ui/public/fonts/IBMPlexSans-Medium.woff2       (500)
ui/public/fonts/IBMPlexSans-SemiBold.woff2     (600)
ui/public/fonts/IBMPlexMono-Regular.woff2      (400)
ui/public/fonts/IBMPlexMono-Medium.woff2       (500)
ui/public/fonts/IBMPlexMono-SemiBold.woff2     (600)
```

Files under `ui/public/` are served at the site root by Vite (`/fonts/<name>.woff2`) and copied verbatim into `dist/`.

- [ ] **Step 2: Write `ui/src/fonts.css`**

```css
/* Self-hosted IBM Plex — Latin subset, woff2. font-display: swap so a font
   fetch never blocks first paint; system fallbacks keep layout stable. */
@font-face { font-family: "IBM Plex Serif"; font-weight: 500; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexSerif-Medium.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Serif"; font-weight: 600; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexSerif-SemiBold.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Sans"; font-weight: 400; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexSans-Regular.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Sans"; font-weight: 500; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexSans-Medium.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Sans"; font-weight: 600; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexSans-SemiBold.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Mono"; font-weight: 400; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexMono-Regular.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Mono"; font-weight: 500; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexMono-Medium.woff2") format("woff2"); }
@font-face { font-family: "IBM Plex Mono"; font-weight: 600; font-style: normal;
  font-display: swap; src: url("/fonts/IBMPlexMono-SemiBold.woff2") format("woff2"); }
```

- [ ] **Step 3: Import `fonts.css` before `global.css` in `ui/src/main.tsx`**

Add at the top of the import block (before `import "./global.css";`):

```ts
import "./fonts.css";
```

- [ ] **Step 4: Add `serif` to `FONTS` in `ui/src/render/palette.ts`**

Replace the `FONTS` export (lines 144-150) with:

```ts
export const FONTS = {
  serif: '"IBM Plex Serif", Georgia, serif',       // panel titles, section labels, wordmark
  mono: '"IBM Plex Mono", ui-monospace, monospace', // data surfaces: tape, ladder, prices, axes
  sans: '"IBM Plex Sans", system-ui, sans-serif',   // chrome: labels, menus, buttons
} as const;
```

- [ ] **Step 5: Preload the four hot weights in `ui/index.html`**

Add inside `<head>` (after the `<title>`), preloading the four most layout-critical weights (body sans, data mono, header serif, medium mono):

```html
    <link rel="preload" href="/fonts/IBMPlexSans-Regular.woff2" as="font" type="font/woff2" crossorigin />
    <link rel="preload" href="/fonts/IBMPlexMono-Regular.woff2" as="font" type="font/woff2" crossorigin />
    <link rel="preload" href="/fonts/IBMPlexSerif-SemiBold.woff2" as="font" type="font/woff2" crossorigin />
    <link rel="preload" href="/fonts/IBMPlexMono-Medium.woff2" as="font" type="font/woff2" crossorigin />
```

- [ ] **Step 6: Write the failing test**

In `ui/src/render/palette.test.ts`, add `FONTS` to the **existing** import (line 2 is already `import { LIGHT, DARK, getPalette, type Palette } from "./palette";` — append `FONTS` to it; do NOT add a second import line), then add:

```ts
describe("FONTS", () => {
  it("declares serif, sans and mono families with fallbacks", () => {
    expect(FONTS.serif).toContain("IBM Plex Serif");
    expect(FONTS.serif).toMatch(/serif$/);
    expect(FONTS.sans).toContain("IBM Plex Sans");
    expect(FONTS.mono).toContain("IBM Plex Mono");
  });
});
```

- [ ] **Step 7: Run tests**

Run: `cd ui && npx vitest run src/render/palette.test.ts`
Expected: PASS.

- [ ] **Step 8: Verify the build copies fonts and preloads resolve**

Run: `cd ui && npm run build && ls dist/fonts/*.woff2 | wc -l`
Expected: build succeeds; output is `8`.

- [ ] **Step 9: Commit**

```bash
cd ui && git add public/fonts src/fonts.css src/main.tsx index.html src/render/palette.ts src/render/palette.test.ts
git commit -m "feat(ui): self-host IBM Plex Serif/Sans/Mono woff2 with @font-face + preload"
```

---

## Task 2: Re-bake the palette (Daylight Ledger) + regenerate goldens

**Files:**
- Modify: `ui/src/render/palette.ts:4-142` (add `borderStrong` to `Palette`; new `LIGHT`/`DARK` values)
- Modify: `ui/src/render/palette.test.ts`
- Regenerate: all `ui/test/golden/goldens/*.png`

**Interfaces:**
- Produces: `Palette.borderStrong: string` (new field, consumed by Task 3 CSS + ladder/tape painters for the center divider / double rules). All existing `Palette` field names are unchanged; only values change (plus the one new field).

**Design note — field roles after the re-bake (no field renames, to minimize churn):**
- `up`/`down` = ink green/red, direction only.
- `accent` = bronze `#9A6A1B` (light) — **all state**.
- `danger` = `#A81E30` — kill/reject/bad-latency.
- `ok`/`warn` are re-pointed to green/bronze so the connection-status dots read ok=green (`ok`), degraded=bronze (`warn`), down=red (`danger`) per the mockup. `ok` value == `up`, `warn` value == `accent`.
- `neutral` (tape/ladder neutral prints) = `textMuted`.
- All derived tokens (`volUp/volDown`, `depthBid/depthAsk`, `flashBuy/flashSell/flashNeutral`, `orderMark`, `sessionPre/Rth/Post/Closed`, indicator defaults, link swatches) are hand-authored rgba literals as today — re-inked from the new base colors with the alphas from the approved mockups (depth bids `rgba(23,122,88,0.13)`, depth asks `rgba(194,51,77,0.11)`, today/hit tint `rgba(154,106,27,0.10)`).

- [ ] **Step 1: Write failing tests for the new palette contract**

In `ui/src/render/palette.test.ts`, add (reuse the existing `getPalette` import at line 2 — do NOT re-import it, that's a duplicate-identifier error):

```ts
describe("Daylight Ledger palette", () => {
  it("light is warm paper with bronze accent and a borderStrong rule", () => {
    const p = getPalette("light");
    expect(p.bg).toBe("#FBFAF7");
    expect(p.surface).toBe("#F2F0EA");
    expect(p.border).toBe("#DDD9CF");
    expect(p.borderStrong).toBe("#C9C4B8");
    expect(p.text).toBe("#171A1E");
    expect(p.textMuted).toBe("#6A7280");
    expect(p.up).toBe("#177A58");
    expect(p.down).toBe("#C2334D");
    expect(p.accent).toBe("#9A6A1B");
    expect(p.danger).toBe("#A81E30");
  });
  it("keeps every dark token role populated (dark stays first-class)", () => {
    const d = getPalette("dark");
    for (const k of Object.keys(getPalette("light")) as (keyof typeof d)[]) {
      expect(d[k], `dark.${k}`).toBeTruthy();
    }
  });
  it("reserves ok/warn for link-status colours (green/bronze), not a 2nd accent", () => {
    const p = getPalette("light");
    expect(p.ok).toBe(p.up);       // link ok == green
    expect(p.warn).toBe(p.accent); // link degraded == bronze
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd ui && npx vitest run src/render/palette.test.ts`
Expected: FAIL (`borderStrong` missing; values differ).

- [ ] **Step 3: Add `borderStrong` to the `Palette` interface**

In `ui/src/render/palette.ts`, in the `Palette` interface `// surfaces / structure` group, add after `border`:

```ts
  border: string;
  borderStrong: string; // double rules, control borders, ladder center divider
```

- [ ] **Step 4: Replace the `LIGHT` object literal** (Daylight Ledger)

```ts
const LIGHT: Palette = {
  bg: "#FBFAF7", surface: "#F2F0EA", border: "#DDD9CF", borderStrong: "#C9C4B8",
  text: "#171A1E", textMuted: "#6A7280",
  grid: "#E7E3DA", crosshair: "#B8B2A4",
  up: "#177A58", down: "#C2334D",
  volUp: "rgba(23,122,88,0.34)", volDown: "rgba(194,51,77,0.30)",
  buyFill: "#177A58", sellFill: "#C2334D", fillOutline: "#FBFAF7",
  neutral: "#6A7280",
  depthBid: "rgba(23,122,88,0.13)", depthAsk: "rgba(194,51,77,0.11)",
  flashBuy: "rgba(23,122,88,0.20)", flashSell: "rgba(194,51,77,0.20)", flashNeutral: "rgba(106,114,128,0.16)",
  orderMark: "#9A6A1B",
  sessionPre: "rgba(154,106,27,0.05)", sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(106,114,128,0.06)", sessionClosed: "rgba(106,114,128,0.10)",
  indVwap: "#9A6A1B", indEma: "#3E7CB1", indSma: "#7A5CA6",
  indMacdLine: "#3E7CB1", indMacdSignal: "#C2334D", indMacdHist: "rgba(106,114,128,0.5)",
  linkRed: "#DB4C56", linkGreen: "#1FA97F", linkBlue: "#3E7CB1", linkYellow: "#CF9A2B",
  accent: "#9A6A1B", ok: "#177A58", warn: "#9A6A1B", danger: "#A81E30",
};
```

- [ ] **Step 5: Replace the `DARK` object literal** (same identity, ink-adjacent surfaces, bronze state)

```ts
const DARK: Palette = {
  bg: "#14120E", surface: "#1C1A15", border: "#2E2A22", borderStrong: "#403A2E",
  text: "#ECE7DB", textMuted: "#9A9385",
  grid: "#241F18", crosshair: "#5A5347",
  up: "#35B888", down: "#E5637A",
  volUp: "rgba(53,184,136,0.34)", volDown: "rgba(229,99,122,0.30)",
  buyFill: "#35B888", sellFill: "#E5637A", fillOutline: "#14120E",
  neutral: "#9A9385",
  depthBid: "rgba(53,184,136,0.16)", depthAsk: "rgba(229,99,122,0.14)",
  flashBuy: "rgba(53,184,136,0.24)", flashSell: "rgba(229,99,122,0.24)", flashNeutral: "rgba(154,147,133,0.18)",
  orderMark: "#C79A4B",
  sessionPre: "rgba(199,154,75,0.07)", sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(154,147,133,0.07)", sessionClosed: "rgba(154,147,133,0.12)",
  indVwap: "#C79A4B", indEma: "#6BA6D8", indSma: "#A98BD0",
  indMacdLine: "#6BA6D8", indMacdSignal: "#E5637A", indMacdHist: "rgba(154,147,133,0.5)",
  linkRed: "#E5636D", linkGreen: "#35B88F", linkBlue: "#6BA6D8", linkYellow: "#D9AE52",
  accent: "#C79A4B", ok: "#35B888", warn: "#C79A4B", danger: "#E5455E",
};
```

- [ ] **Step 6: Run the palette tests**

Run: `cd ui && npx vitest run src/render/palette.test.ts`
Expected: PASS.

- [ ] **Step 7: Safety-check for stray hard-coded palette literals in tests**

The known primitive tests already read colors dynamically (`diamondMarker.test.ts` asserts `fillColor("buy", LIGHT)).toBe(LIGHT.buyFill)`; `sessions.test.ts` has no color assertions), so no edit is expected there. As a guard, grep for any test still asserting an OLD palette literal and repoint it to `getPalette(mode)`:
Run: `cd ui && grep -rnE "#[0-9A-Fa-f]{6}|rgba\(" src/**/*.test.ts | grep -viE "#123456|user|indicator" || echo "none"`
If a real old-palette literal turns up in a test, fix it; otherwise this is a no-op. Then: `cd ui && npx vitest run src/render` — expect PASS.

- [ ] **Step 8: Regenerate all golden images for the new colors**

The 16 ladder/tape goldens + `harness-smoke` are now stale (every pixel's color changed). Regenerate and eyeball:

Run: `cd ui && npm run test:golden:update`
Then inspect `ui/test/golden/__output__/*.png` — confirm warm-paper backgrounds, ink green/red, bronze order marks. Then verify they pass clean:
Run: `cd ui && npm run test:golden`
Expected: PASS (0 differing pixels).

> Note: the ladder/tape *layouts* are unchanged in this task — only colors. Tasks 14/15 rewrite the layouts and regenerate again.

- [ ] **Step 9: Full unit suite green**

Run: `cd ui && npm run test`
Expected: PASS. (Component tests read `palette.*` via `useTheme` and are color-agnostic, so they should be unaffected.)

- [ ] **Step 10: Commit**

```bash
cd ui && git add src/render/palette.ts src/render/palette.test.ts src/render/chart/diamondMarker.test.ts src/render/chart/sessions.test.ts test/golden/goldens
git commit -m "feat(ui): re-bake palette to Daylight Ledger (warm paper, ink green/red, bronze state) + borderStrong; regenerate goldens"
```

---

## Task 3: CSS custom-properties layer + `global.css` + dockview theming

**Files:**
- Create: `ui/src/chrome/cssVars.ts`
- Modify: `ui/src/chrome/ThemeProvider.tsx` (write vars to `:root` on mode change)
- Modify: `ui/src/global.css` (base typography + shared control classes + panel-frame classes + dockview overrides)
- Create: `ui/src/chrome/cssVars.test.ts`
- Modify: `ui/src/chrome/ThemeProvider.test.tsx`

**Interfaces:**
- Produces:
  - `paletteToVars(p: Palette): Record<string, string>` — maps each `Palette` field to a `--<kebab>` custom-property name/value (e.g. `borderStrong` → `--border-strong`).
  - `applyPaletteVars(root: HTMLElement, p: Palette): void` — sets every var on `root.style` and sets `root.dataset.theme = mode` upstream.
  - CSS classes consumed by later tasks: `.btn`, `.btn-primary`, `.ctl`, `.popover`, `.ledger-header`, `.ledger-title`, `.col-head`, `.data-row`, `.chip`, `.chip-working`, `.chip-pending`, `.chip-rejected`, `.kill-switch`, `.panel-focused`, `.sort-active`. All read `var(--*)`.

- [ ] **Step 1: Write `cssVars.ts` failing test**

Create `ui/src/chrome/cssVars.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { paletteToVars } from "./cssVars";
import { getPalette } from "../render/palette";

describe("paletteToVars", () => {
  it("kebab-cases every palette key into a --var", () => {
    const vars = paletteToVars(getPalette("light"));
    expect(vars["--bg"]).toBe("#FBFAF7");
    expect(vars["--border-strong"]).toBe("#C9C4B8");
    expect(vars["--text-muted"]).toBe("#6A7280");
    expect(vars["--accent"]).toBe("#9A6A1B");
    expect(vars["--up"]).toBe("#177A58");
  });
  it("emits one var per palette field", () => {
    const p = getPalette("light");
    const vars = paletteToVars(p);
    expect(Object.keys(vars).length).toBe(Object.keys(p).length);
  });
});
```

- [ ] **Step 2: Run it to fail**

Run: `cd ui && npx vitest run src/chrome/cssVars.test.ts`
Expected: FAIL (module missing).

- [ ] **Step 3: Implement `ui/src/chrome/cssVars.ts`**

```ts
import type { Palette } from "../render/palette";

const kebab = (s: string): string => s.replace(/[A-Z]/g, (m) => `-${m.toLowerCase()}`);

/** Map each Palette field to a CSS custom property (`borderStrong` → `--border-strong`). */
export function paletteToVars(p: Palette): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(p)) out[`--${kebab(k)}`] = v;
  return out;
}

/** Mirror the palette onto :root so CSS classes can consume `var(--*)`. */
export function applyPaletteVars(root: HTMLElement, p: Palette): void {
  const vars = paletteToVars(p);
  for (const [name, value] of Object.entries(vars)) root.style.setProperty(name, value);
}
```

- [ ] **Step 4: Run it to pass**

Run: `cd ui && npx vitest run src/chrome/cssVars.test.ts`
Expected: PASS.

- [ ] **Step 5: Wire `ThemeProvider` to write vars + `data-theme` on mode change**

In `ui/src/chrome/ThemeProvider.tsx`, add the import and an effect that runs whenever `mode` changes:

```ts
import { applyPaletteVars } from "./cssVars";
// ...inside ThemeProvider, after `const value = useMemo(...)` and before the return:
useEffect(() => {
  const root = document.documentElement;
  applyPaletteVars(root, getPalette(mode));
  root.dataset.theme = mode;
}, [mode]);
```

(`getPalette` is already imported in this file.)

- [ ] **Step 6: Extend `ThemeProvider.test.tsx`**

Add a case asserting the side effect:

```ts
it("mirrors the palette onto :root and sets data-theme", async () => {
  render(<ThemeProvider><div /></ThemeProvider>);
  await waitFor(() => {
    expect(document.documentElement.style.getPropertyValue("--bg")).toBe("#FBFAF7");
    expect(document.documentElement.dataset.theme).toBe("light");
  });
});
```

(Use the file's existing render/import helpers; add `waitFor` to the testing-library import if absent.)

- [ ] **Step 7: Rewrite `ui/src/global.css`** — base typography, control classes, panel-frame classes, dockview overrides

Replace the entire file with:

```css
/* Height chain: without this, dockview's content area collapses to 0px. */
html, body, #root { height: 100%; margin: 0; }

/* Base typography — Daylight Ledger. Fonts self-hosted in fonts.css; system
   fallbacks keep layout stable if a font fails to load. */
body {
  font-family: "IBM Plex Sans", system-ui, sans-serif;
  color: var(--text);
  background: var(--bg);
  font-size: 12px;
}
.mono, .num { font-family: "IBM Plex Mono", ui-monospace, monospace; font-variant-numeric: tabular-nums; }
.serif { font-family: "IBM Plex Serif", Georgia, serif; }

/* Ledger panel header — the one signature element: serif title over a double rule. */
.ledger-header {
  display: flex; align-items: center; gap: 8px; padding: 5px 10px;
  font-family: "IBM Plex Serif", Georgia, serif; font-size: 12px;
  background: var(--bg); border-bottom: 3px double var(--border-strong);
}
.ledger-title { font-family: "IBM Plex Serif", Georgia, serif; font-weight: 600; }
.col-head {
  font-family: "IBM Plex Sans", system-ui, sans-serif; font-size: 9.5px;
  letter-spacing: .08em; text-transform: uppercase; color: var(--text-muted);
}
.col-head.sort-active { color: var(--text); font-weight: 600; cursor: pointer; }
.data-row { font-family: "IBM Plex Mono", ui-monospace, monospace; font-size: 11px; font-variant-numeric: tabular-nums; }

/* Shared controls — replace native <select>/<input>/<button>. */
.btn {
  font-family: "IBM Plex Sans", system-ui, sans-serif; font-size: 11px;
  padding: 4px 10px; border-radius: 4px; border: 1px solid var(--border-strong);
  background: var(--bg); color: var(--text); cursor: pointer;
}
.btn:hover { border-color: var(--text-muted); }
.btn-primary { background: var(--text); color: var(--bg); border-color: var(--text); }
.ctl {
  display: inline-flex; align-items: center; gap: 4px;
  border: 1px solid var(--border-strong); border-radius: 4px;
  background: var(--bg); padding: 2px 7px; font-size: 10.5px;
}
.ctl > b, .ctl input { font-family: "IBM Plex Mono", ui-monospace, monospace; font-weight: 500; }
.popover {
  position: absolute; z-index: 30; background: var(--bg);
  border: 1px solid var(--border-strong); border-radius: 6px;
  box-shadow: 0 6px 18px rgba(23,26,30,.14); padding: 8px;
}

/* Lifecycle chips (open orders). Green = market direction exception intentionally
   avoided: WORKING uses green outline per spec; PENDING/REPLACING bronze; REJECTED danger. */
.chip { font-size: 9px; font-weight: 600; letter-spacing: .05em; padding: 1px 6px; border-radius: 2px; border: 1px solid; display: inline-block; }
.chip-working { color: var(--up); border-color: var(--up); background: rgba(23,122,88,.07); }
.chip-pending { color: var(--accent); border-color: var(--accent); background: rgba(154,106,27,.08); }
.chip-rejected { color: var(--danger); border-color: var(--danger); background: rgba(168,30,48,.06); }

/* Kill switch — deliberately loud. */
.kill-switch {
  width: 100%; text-align: center; padding: 6px; border: 2px solid var(--danger);
  color: var(--danger); font-weight: 800; letter-spacing: .12em; border-radius: 3px;
  background: rgba(168,30,48,.05); cursor: pointer;
}

/* Focused panel — bronze ring + tinted header (Task 12 toggles .panel-focused). */
.panel-focused { outline: 2px solid var(--accent); outline-offset: -2px; }
.panel-focused .ledger-header { background: rgba(154,106,27,.07); }

/* dockview chrome themed from our tokens (was VS-Code greys). */
.dockview-theme-light, .dockview-theme-dark {
  --dv-group-view-background-color: var(--bg);
  --dv-tabs-and-actions-container-background-color: var(--surface);
  --dv-activegroup-visiblepanel-tab-background-color: var(--bg);
  --dv-inactivegroup-visiblepanel-tab-background-color: var(--surface);
  --dv-tab-divider-color: var(--border);
  --dv-separator-border: var(--border);
  --dv-paneview-active-outline-color: var(--accent);
  --dv-icon-hover-background-color: var(--surface);
  --dv-activegroup-visiblepanel-tab-color: var(--text);
  --dv-inactivegroup-visiblepanel-tab-color: var(--text-muted);
}
```

- [ ] **Step 8: Run the theme provider + full suite**

Run: `cd ui && npx vitest run src/chrome/ThemeProvider.test.tsx src/chrome/cssVars.test.ts`
Expected: PASS.

- [ ] **Step 9: Manual smoke — vars applied, dockview re-themed**

Run: `cd ui && npm run dev` and open the app; confirm the top bar/panels sit on warm paper, dockview tab bars use the surface color (not VS-Code grey), and toggling theme (once Settings lands, or via devtools `document.documentElement.dataset.theme`) swaps `:root` vars. (This is a visual check; no assertion.)

- [ ] **Step 10: Commit**

```bash
cd ui && git add src/chrome/cssVars.ts src/chrome/cssVars.test.ts src/chrome/ThemeProvider.tsx src/chrome/ThemeProvider.test.tsx src/global.css
git commit -m "feat(ui): mirror palette to :root CSS vars, add global.css control/ledger classes, theme dockview from tokens"
```

---

# Phase 1 — Shell model

## Task 4: Extract `normalizeSymbol` to a shared module

**Files:**
- Create: `ui/src/chrome/symbol.ts`
- Create: `ui/src/chrome/symbol.test.ts`
- Modify: `ui/src/chrome/WorkspaceHeader.tsx` (re-export or import from new module — this file is deleted in Task 9, but stays valid until then)

**Interfaces:**
- Produces: `normalizeSymbol(raw: string): string` — uppercases; prefixes bare tickers with `US.` unless already prefixed with an allow-listed market (`US.`/`HK.`); preserves dotted tickers (`BRK.B` → `US.BRK.B`). Consumed by Task 9 (top bar removes the symbol boxes but the fn is still needed) and Task 13 (type-to-load Enter commit).

- [ ] **Step 1: Write the failing test** — `ui/src/chrome/symbol.test.ts`

```ts
import { describe, it, expect } from "vitest";
import { normalizeSymbol } from "./symbol";

describe("normalizeSymbol", () => {
  it("uppercases and US-prefixes a bare ticker", () => expect(normalizeSymbol("aapl")).toBe("US.AAPL"));
  it("leaves an already-qualified symbol", () => expect(normalizeSymbol("HK.00700")).toBe("HK.00700"));
  it("US-prefixes a dotted ticker rather than treating the dot as a market", () =>
    expect(normalizeSymbol("brk.b")).toBe("US.BRK.B"));
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/symbol.test.ts`
Expected: FAIL (module missing).

- [ ] **Step 3: Create `ui/src/chrome/symbol.ts`** (move the fn verbatim from `WorkspaceHeader.tsx`)

```ts
const MARKET_PREFIXES = ["US.", "HK."];

/** Uppercase; prefix bare tickers with `US.` unless already market-qualified.
 * Allow-list (not a regex) so dotted US tickers like BRK.B aren't misread. */
export function normalizeSymbol(raw: string): string {
  const upper = raw.toUpperCase();
  return MARKET_PREFIXES.some((p) => upper.startsWith(p)) ? upper : `US.${upper}`;
}
```

- [ ] **Step 4: Re-point `WorkspaceHeader.tsx`** to the shared fn

Delete the local `MARKET_PREFIXES` + `normalizeSymbol` **and the explanatory comment block directly above them** (the allow-list rationale, ~lines 7-20 — don't leave a stale comment orphaned above an import). Add `import { normalizeSymbol } from "./symbol";`. Keep the `export { normalizeSymbol }` behavior for any test importing it from the header by re-exporting: `export { normalizeSymbol } from "./symbol";` — `WorkspaceHeader.test.tsx:5` does `import { WorkspaceHeader, normalizeSymbol } from "./WorkspaceHeader"`, so this shim is required until Task 9 deletes both.

- [ ] **Step 5: Run affected tests**

Run: `cd ui && npx vitest run src/chrome/symbol.test.ts src/chrome/WorkspaceHeader.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/symbol.ts src/chrome/symbol.test.ts src/chrome/WorkspaceHeader.tsx
git commit -m "refactor(ui): extract normalizeSymbol to shared chrome/symbol module"
```

---

## Task 5: Generalize the window/workspace model

**Files:**
- Create: `ui/src/chrome/windows.ts`
- Create: `ui/src/chrome/windows.test.ts`
- Modify: `ui/src/chrome/workspace.ts` (`WorkspaceStore.load` accepts any name; fix load/save key-casing asymmetry)
- Modify: `ui/src/chrome/workspace.test.ts`
- Modify: `ui/src/main.tsx` (parse arbitrary `?workspace=<name>`, default `main`)
- Modify: `ui/src/App.tsx`, `ui/src/chrome/AppShell.tsx` (prop type `workspaceName: string`)

**Interfaces:**
- Produces:
  - `parseWorkspaceName(search: string): string` — reads `?workspace=`; returns a sanitized name or `"main"` when absent/invalid.
  - `nextWindowName(existing: string[]): string` — lowest free `window-N` (N≥2) not in `existing`. (`main` is window 1.)
  - `WorkspaceStore.load(name: string): Promise<Workspace>` — now any string; returns a **blank** workspace (`{ name, panels: [], layout: null }`) when the config key is absent (no seed fallback — seeds are gone; presets are opt-in per Task 7/10).
- Consumes: `Workspace` type from `workspace.ts` (unchanged shape `{ name, panels, layout }`).

- [ ] **Step 1: Write `windows.test.ts` (failing)**

```ts
import { describe, it, expect } from "vitest";
import { parseWorkspaceName, nextWindowName } from "./windows";

describe("parseWorkspaceName", () => {
  it("defaults to main when absent", () => expect(parseWorkspaceName("")).toBe("main"));
  it("reads the workspace param", () => expect(parseWorkspaceName("?workspace=window-2")).toBe("window-2"));
  it("sanitizes to [a-z0-9-] and lowercases", () => expect(parseWorkspaceName("?workspace=Win Dow!")).toBe("main"));
});
describe("nextWindowName", () => {
  it("first extra window is window-2", () => expect(nextWindowName(["main"])).toBe("window-2"));
  it("fills the lowest gap", () => expect(nextWindowName(["main", "window-2", "window-4"])).toBe("window-3"));
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/windows.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement `ui/src/chrome/windows.ts`**

```ts
const NAME_RE = /^[a-z0-9-]{1,32}$/;

/** Parse `?workspace=<name>`; default `main`; reject anything not [a-z0-9-]. */
export function parseWorkspaceName(search: string): string {
  const raw = new URLSearchParams(search).get("workspace");
  if (!raw) return "main";
  const name = raw.toLowerCase();
  return NAME_RE.test(name) ? name : "main";
}

/** Lowest free `window-N` (N starts at 2; `main` is window 1). */
export function nextWindowName(existing: string[]): string {
  const taken = new Set(existing);
  for (let n = 2; ; n++) {
    const candidate = `window-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}
```

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/windows.test.ts`
Expected: PASS.

- [ ] **Step 5: Generalize `WorkspaceStore` + fix the key-casing footgun**

In `ui/src/chrome/workspace.ts`:
- Change `load(name: "monitoring" | "trading")` to `load(name: string)`.
- Replace the seed fallback with a blank workspace:

```ts
async load(name: string): Promise<Workspace> {
  const key = `workspace.${name}`;
  const ack = await this.client.sendCommand("GetConfig", { key });
  if (ack.status === "accepted" && ack.value) return ack.value as Workspace;
  return { name, panels: [], layout: null };
}
```

- Fix `writeNow` to derive the key from the same normalization as `load` (both raw, no `.toLowerCase()` mismatch — `parseWorkspaceName` already lowercased):

```ts
const key = `workspace.${ws.name}`;
```

- Remove the now-unused `import { SEED_WORKSPACES } from "../seeds/workspaces";`.

- [ ] **Step 6: Update `workspace.test.ts`**

Replace the seed-fallback assertion with a blank-fallback one:

```ts
it("returns a blank workspace when no doc is saved", async () => {
  const client = { sendCommand: vi.fn().mockResolvedValue({ status: "accepted", value: null }) };
  const store = new WorkspaceStore(client);
  const ws = await store.load("main");
  expect(ws).toEqual({ name: "main", panels: [], layout: null });
});
```

Keep the debounce/flush test; change any `load("monitoring")` calls to `load("main")` and assert the write key is `workspace.main`.

- [ ] **Step 7: Update `main.tsx`**

```tsx
import { parseWorkspaceName } from "./chrome/windows";
const workspaceName = parseWorkspaceName(location.search);
// ...
<App workspaceName={workspaceName} />
```

- [ ] **Step 8: Widen prop types**

In `App.tsx` change `workspaceName: "monitoring" | "trading"` → `workspaceName: string`; in `AppShell.tsx` change the `Props.workspaceName` type likewise. Remove the App-mount topic-subscription block that reads `SEED_WORKSPACES[workspaceName].panels` — replace with subscribing the **union of all catalog panels' topics** (Task 6 provides the list; for now subscribe the union of `Object.values(PANELS).flatMap(d => d.topics)` deduped, since a blank workspace can add any panel). Add a code comment that this over-subscribes slightly but is correct for a build-anything catalog.

- [ ] **Step 9: Run affected suites**

Run: `cd ui && npx vitest run src/chrome/workspace.test.ts src/chrome/windows.test.ts`
Expected: PASS. Then `cd ui && npx tsc --noEmit` to confirm the widened prop types compile across `App.tsx`/`AppShell.tsx`/`main.tsx`.

- [ ] **Step 10: Commit**

```bash
cd ui && git add src/chrome/windows.ts src/chrome/windows.test.ts src/chrome/workspace.ts src/chrome/workspace.test.ts src/main.tsx src/App.tsx src/chrome/AppShell.tsx
git commit -m "feat(ui): generalize workspace addressing (?workspace=<name>, default main), blank-load fallback, window-N allocation"
```

---

## Task 6: Registry catalog metadata + dev-gate the smoke panel

**Files:**
- Modify: `ui/src/chrome/panels/registry.tsx` (add `title`/`glyph`/`description`/`symbolBearing` to `PanelDef`; export `CATALOG`; dev-gate `smoke-painter`)
- Modify: `ui/src/chrome/panels/registry.test.tsx`

**Interfaces:**
- Produces:
  - `PanelDef` gains: `title: string`, `glyph: string`, `description: string`, `symbolBearing: boolean` (chart/ladder/tape/news = true; drives type-to-load eligibility in Task 13).
  - `CATALOG: { panelId: string; title: string; glyph: string; description: string }[]` — ordered list for the empty-state/add-panel UI (Task 10), **excluding** `smoke-painter`.
  - `isDevPanel(panelId: string): boolean` — true only for `smoke-painter`.
- Consumes: existing `PANELS` component/topics.

- [ ] **Step 1: Extend `registry.test.tsx` (failing)**

```ts
import { PANELS, CATALOG, isDevPanel } from "./registry";

describe("catalog metadata", () => {
  it("every non-dev panel has title/glyph/description", () => {
    for (const [id, def] of Object.entries(PANELS)) {
      if (isDevPanel(id)) continue;
      expect(def.title, id).toBeTruthy();
      expect(def.glyph, id).toBeTruthy();
      expect(def.description, id).toBeTruthy();
    }
  });
  it("CATALOG omits the dev smoke panel and lists chart first", () => {
    expect(CATALOG.map((c) => c.panelId)).not.toContain("smoke-painter");
    expect(CATALOG[0].panelId).toBe("chart");
  });
  it("marks symbol-bearing panels", () => {
    expect(PANELS["chart"].symbolBearing).toBe(true);
    expect(PANELS["scanner"].symbolBearing).toBe(false);
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/panels/registry.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Extend `PanelDef` and annotate every entry**

In `registry.tsx`, change the interface and each registration. Add fields (use the glyphs + one-liners from the approved catalog mockup):

```ts
export interface PanelDef {
  component: FC<PanelProps>;
  topics: TopicName[];
  title: string;
  glyph: string;
  description: string;
  symbolBearing: boolean;
}
```

Annotate (titles/glyphs/descriptions verbatim from `empty-state.html`):

| panelId | title | glyph | description | symbolBearing |
|---|---|---|---|---|
| `chart` | Chart | `▁▃▅▇` | Candles, volume, indicators | true |
| `ladder` | DOM Ladder | `≡` | 10-level depth, working orders | true |
| `tape` | Time & Sales | `⋮⋮` | Live prints, buy/sell colored | true |
| `scanner` | Scanner | `%` | Pre-market gappers, filters | false |
| `movers` | Movers | `↕` | RTH % leaders | false |
| `news` | News | `¶` | Headlines for focused symbol | true |
| `account-bar` | Account | `Σ` | Equity, BP, day P&L, arm | false |
| `positions` | Positions | `□` | Live P&L, flatten per row | false |
| `open-orders` | Open Orders | `◷` | Lifecycle, cancel, cancel-all | false |
| `order-ticket` | Order Ticket | `$` | Presets, sizing, kill switch | false |
| `connection-status` | Connection | `⇄` | Link latency, event log | false |
| `smoke-painter` | Smoke | `•` | Dev-only painter probe | false |

> Note: the merged single **`account`** panel (Σ) does not exist yet — it's created in Task 19, which then replaces both the `account-bar` and `positions` rows above with one `account` row (and updates `CATALOG_ORDER`). Annotating both existing ids here keeps the widened `PanelDef` (every non-dev entry needs `title`/`glyph`/`description`) valid at Task 6's commit. This matches the interim shown in the `empty-state.html` mockup (separate Account + Positions cards).

- [ ] **Step 4: Dev-gate `smoke-painter` and export `CATALOG` + `isDevPanel`**

```ts
export const DEV_PANELS = new Set(["smoke-painter"]);
export const isDevPanel = (panelId: string): boolean => DEV_PANELS.has(panelId);

const CATALOG_ORDER = ["chart", "ladder", "tape", "scanner", "movers", "news",
  "account-bar", "positions", "open-orders", "order-ticket", "connection-status"];
// Task 19 replaces "account-bar","positions" here with the single merged "account".

export const CATALOG = CATALOG_ORDER
  .filter((id) => PANELS[id])
  .map((id) => ({ panelId: id, title: PANELS[id].title, glyph: PANELS[id].glyph, description: PANELS[id].description }));
```

Keep `smoke-painter` in `PANELS` (so a saved workspace referencing it still renders) but excluded from `CATALOG`. The add-panel UI (Task 10) only surfaces `smoke-painter` behind `import.meta.env.DEV`.

- [ ] **Step 5: Run to pass**

Run: `cd ui && npx vitest run src/chrome/panels/registry.test.tsx`
Expected: PASS. (Until Task 19, adjust the `CATALOG[0]` assertion only if needed; `chart` stays first.)

- [ ] **Step 6: Commit**

```bash
cd ui && git add src/chrome/panels/registry.tsx src/chrome/panels/registry.test.tsx
git commit -m "feat(ui): add catalog metadata (title/glyph/description/symbolBearing) to panel registry; dev-gate smoke panel"
```

---

## Task 7: Presets module (replaces seeds/workspaces.ts)

**Files:**
- Create: `ui/src/chrome/presets.ts`
- Create: `ui/src/chrome/presets.test.ts`
- Delete: `ui/src/seeds/workspaces.ts` (and its dir if empty) — **only after** confirming no remaining importers (Task 5 removed the `WorkspaceStore` importer; Task 5 step 8 removed the App importer)
- Modify: any remaining importers found by grep

**Interfaces:**
- Produces:
  - `PRESETS: Preset[]` where `interface Preset { id: string; name: string; description: string; thumb: "monitoring" | "trading"; build(): { panels: PanelConfig[]; layout: SerializedDockview } }`.
  - `applyPreset(api: DockviewApi, preset: Preset): void` — clears the layout and applies the preset's serialized dockview layout.
- Consumes: `PanelConfig` (`workspace.ts`), dockview `DockviewApi`/serialized-layout JSON.

**Design note:** the old seed `layout` was a placeholder string, never fed to `fromJSON`; presets need **real** dockview serialized layout JSON to hit the specced proportions (Monitoring `2fr 1fr` with a 2×2 chart wall; Trading `1.7fr 1fr 1.05fr`). Author each preset's layout by building it once in the running app and copying `api.toJSON()`, OR construct it programmatically via `addPanel` + `api.toJSON()` in a one-off. The `build()` function returns both the panel-config list (for `ws.panels`) and the serialized layout (for `ws.layout`), so applying a preset = write `ws = { name, panels, layout }` then `api.fromJSON(layout)`.

- [ ] **Step 1: Write `presets.test.ts` (failing)**

```ts
import { describe, it, expect } from "vitest";
import { PRESETS } from "./presets";
import { PANELS, isDevPanel } from "./panels/registry";

describe("presets", () => {
  it("exposes Monitoring and Trading", () => {
    expect(PRESETS.map((p) => p.id).sort()).toEqual(["monitoring", "trading"]);
  });
  for (const preset of PRESETS) {
    it(`${preset.id}: every panel id is a real, non-dev registered panel`, () => {
      const { panels, layout } = preset.build();
      expect(panels.length).toBeGreaterThan(0);
      for (const p of panels) {
        expect(PANELS[p.panelId], p.panelId).toBeTruthy();
        expect(isDevPanel(p.panelId), p.panelId).toBe(false);
      }
      // layout JSON references exactly the panel ids we declared
      expect(layout && typeof layout).toBe("object");
    });
    it(`${preset.id}: layout panel ids match the config list`, () => {
      const { panels, layout } = preset.build();
      const layoutIds = Object.keys((layout as { panels: Record<string, unknown> }).panels).sort();
      expect(layoutIds).toEqual(panels.map((p) => p.id).sort());
    });
  }
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/presets.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement `ui/src/chrome/presets.ts`**

Build the two presets. Panel configs follow the mockup (`presets.html`): Monitoring = 4 charts one per link group (red TSLA, green NVDA, blue AAPL, yellow SPY, all `1m`) left ⅔ in a 2×2 wall + scanner(pinned, pre-market) / movers(pinned, RTH) / news(blue) right ⅓; Trading = 2 blue charts (1m VWAP/EMA9, 10s Δ) left + DOM/tape center + ticket(blue)/account(pinned)/open-orders(pinned) right.

```ts
import type { PanelConfig } from "./workspace";
import type { DockviewApi, SerializedDockview } from "dockview";

export interface Preset {
  id: string;
  name: string;
  description: string;
  thumb: "monitoring" | "trading";
  build(): { panels: PanelConfig[]; layout: SerializedDockview };
}

const chart = (id: string, symbol: string, timeframe: string, group: PanelConfig["group"]): PanelConfig =>
  ({ id, panelId: "chart", group, settings: { symbol, timeframe } });

// NOTE: `layout` below is real dockview serialized JSON (SerializedDockview),
// authored by building the arrangement once and copying api.toJSON(). Regenerate
// with: build in dev, run `copy(api.toJSON())`, paste here. The proportions match
// docs mockups presets.html (Monitoring 2fr/1fr with 2x2 wall; Trading 1.7/1/1.05).
const MONITORING_LAYOUT: SerializedDockview = /* paste api.toJSON() — see step 4 */ ({} as SerializedDockview);
const TRADING_LAYOUT: SerializedDockview = /* paste api.toJSON() — see step 4 */ ({} as SerializedDockview);

export const PRESETS: Preset[] = [
  {
    id: "monitoring", name: "Monitoring", thumb: "monitoring",
    description: "Chart wall + scanner, movers, news. Watching the market, not trading it.",
    build: () => ({
      panels: [
        chart("m-chart-red", "US.TSLA", "1m", "red"),
        chart("m-chart-green", "US.NVDA", "1m", "green"),
        chart("m-chart-blue", "US.AAPL", "1m", "blue"),
        chart("m-chart-yellow", "US.SPY", "1m", "yellow"),
        { id: "m-scanner", panelId: "scanner", group: null, settings: { thresholds: { minChangePct: 10, floatCapShares: 20_000_000, minVolume: 100_000 } } },
        { id: "m-movers", panelId: "movers", group: null, settings: { thresholds: { minChangePct: 5, floatCapShares: null, minVolume: 500_000 } } },
        { id: "m-news", panelId: "news", group: "blue", settings: {} },
      ],
      layout: MONITORING_LAYOUT,
    }),
  },
  {
    id: "trading", name: "Trading", thumb: "trading",
    description: "Focused charts + DOM, tape, ticket, positions. The execution seat.",
    build: () => ({
      panels: [
        chart("t-chart-1m", "US.AAPL", "1m", "blue"),
        chart("t-chart-10s", "US.AAPL", "10s", "blue"),
        { id: "t-dom", panelId: "ladder", group: "blue", settings: { symbol: "US.AAPL" } },
        { id: "t-tape", panelId: "tape", group: "blue", settings: { symbol: "US.AAPL", minSize: 0 } },
        { id: "t-ticket", panelId: "order-ticket", group: "blue", settings: {} },
        // t-account uses account-bar until Task 19 swaps this panelId to the merged "account"
        { id: "t-account", panelId: "account-bar", group: null, settings: {} },
        { id: "t-orders", panelId: "open-orders", group: null, settings: {} },
      ],
      layout: TRADING_LAYOUT,
    }),
  },
];

/** Replace the current dockview layout with the preset's. Caller writes ws.panels/layout first. */
export function applyPreset(api: DockviewApi, preset: Preset): void {
  const { layout } = preset.build();
  api.clear();
  api.fromJSON(layout);
}
```

- [ ] **Step 4: Author the two real layout JSON blobs**

Run `npm run dev`, temporarily add each preset's panels via `api.addPanel(...)` arranged per the mockup proportions, then in the devtools console run `copy(dockviewApi.toJSON())` and paste the result over `MONITORING_LAYOUT` / `TRADING_LAYOUT`. The panel `id`s in the layout MUST equal the `PanelConfig.id`s above (the `presets.test.ts` "layout panel ids match" test enforces this). Grid proportions: Monitoring outer split `[2, 1]` (charts | right rail), charts inner 2×2, right rail rows `[1.2, 1, 0.9]`; Trading outer `[1.7, 1, 1.05]`, left rows `[1,1]`, center rows `[1.15, 1]`, right rows `[auto, 1, 1]`.

- [ ] **Step 5: Run to pass, then fix the two remaining `SEED_WORKSPACES` importers, then delete seeds**

Run: `cd ui && npx vitest run src/chrome/presets.test.ts`
Expected: PASS. Then fix the two known importers (grep-verified) before deleting the seed file:
- `ui/src/chrome/workspace.test.ts:3` imports `SEED_WORKSPACES` and the debounce test (~lines 28-30) uses `SEED_WORKSPACES.trading` — replace that usage with an inline `Workspace` literal (e.g. `{ name: "trading", panels: [], layout: null }`) and remove the import.
- `ui/src/chrome/panels/registry.test.tsx:3` imports `SEED_WORKSPACES` and asserts on `SEED_WORKSPACES.monitoring.panels` (scanner/movers thresholds) — remove that seed-dependent `describe`/assertion block and the import (the seed no longer exists; preset thresholds are now covered by `presets.test.ts`).

Then confirm nothing else references it and delete:
Run: `cd ui && grep -rn "seeds/workspaces\|SEED_WORKSPACES" src && rm src/seeds/workspaces.ts && rmdir src/seeds 2>/dev/null; true`
(The grep must print nothing before you delete; if it prints a straggler, fix it first.)

- [ ] **Step 6: tsc + full unit suite**

Run: `cd ui && npx tsc --noEmit && npm run test`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd ui && git add src/chrome/presets.ts src/chrome/presets.test.ts && git rm src/seeds/workspaces.ts
git commit -m "feat(ui): add catalog presets (Monitoring/Trading) with real dockview layouts; remove seed workspaces"
```

---

# Phase 2 — Shell UI

## Task 8: Latency readout component

**Files:**
- Create: `ui/src/chrome/LatencyReadout.tsx`
- Create: `ui/src/chrome/LatencyReadout.test.tsx`

**Interfaces:**
- Produces: `LatencyReadout({ health, onOpen }: { health: HealthStore; onOpen: () => void }): JSX.Element` — renders the three links (`eng`=ui-engine, `moo`=engine-moomoo, `tz`=engine-tz) as `dot + label + ms`, threshold-colored (ok green / degraded bronze / down red), reading `HealthStore` via `useSyncExternalStore`. Clicking anywhere calls `onOpen` (Task 9/10 wires it to add/focus the Connection Status panel).
- Consumes: `HealthStore` (`src/data/HealthStore.ts`), `HealthLink`/`LinkName`/`LinkStatus` (`src/gen/wsmsg.ts`), `useTheme` palette. **No engine change** — same `sys.health` topic and cadence.

- [ ] **Step 1: Write `LatencyReadout.test.tsx` (failing)**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LatencyReadout } from "./LatencyReadout";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";

function storeWith(links: unknown[]): HealthStore {
  const s = new HealthStore();
  // SnapshotMsg/DeltaMsg carry `payload` (NOT `data`); HealthStore reads m.payload.links
  s.apply({ kind: "snapshot", topic: "sys.health", payload: { links } } as never);
  return s;
}

describe("LatencyReadout", () => {
  it("shows all three links with ms and threshold color classes", () => {
    const s = storeWith([
      { link: "ui-engine", ms: 0.5, min: 0.2, avg: 0.4, max: 1, status: "ok" },
      { link: "engine-moomoo", ms: 4.2, min: 3, avg: 4, max: 6, status: "ok" },
      { link: "engine-tz", ms: 184, min: 90, avg: 150, max: 300, status: "degraded" },
    ]);
    render(<ThemeProvider><LatencyReadout health={s} onOpen={() => {}} /></ThemeProvider>);
    expect(screen.getByText("eng")).toBeInTheDocument();
    expect(screen.getByText("moo")).toBeInTheDocument();
    expect(screen.getByText("tz")).toBeInTheDocument();
    expect(screen.getByTestId("lat-tz")).toHaveTextContent("184");
  });
  it("calls onOpen when clicked", () => {
    const onOpen = vi.fn();
    render(<ThemeProvider><LatencyReadout health={storeWith([])} onOpen={onOpen} /></ThemeProvider>);
    fireEvent.click(screen.getByTestId("latency-readout"));
    expect(onOpen).toHaveBeenCalled();
  });
});
```

> This test mounts jsdom + no real canvas, so it stays in the default pool. Confirm the `SnapshotMsg` shape against `HealthStore.test.ts` and adjust the `apply(...)` payload if the real message uses a different envelope.

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/LatencyReadout.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `LatencyReadout.tsx`**

```tsx
import { useSyncExternalStore } from "react";
import type { HealthStore } from "../data/HealthStore";
import type { HealthLink, LinkName, LinkStatus } from "../gen/wsmsg";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

const LABEL: Record<LinkName, string> = { "ui-engine": "eng", "engine-moomoo": "moo", "engine-tz": "tz" };
const ORDER: LinkName[] = ["ui-engine", "engine-moomoo", "engine-tz"];
const dotColor = (s: LinkStatus, p: Palette): string => (s === "ok" ? p.ok : s === "degraded" ? p.warn : p.danger);

export function LatencyReadout({ health, onOpen }: { health: HealthStore; onOpen: () => void }): JSX.Element {
  const { palette } = useTheme();
  // ReactStore exposes subscribe(cb) + getSnapshot() (NOT snapshot()).
  const state = useSyncExternalStore(health.subscribe.bind(health), health.getSnapshot.bind(health));
  const byName = new Map<LinkName, HealthLink>(state.links.map((l) => [l.link, l]));
  return (
    <button data-testid="latency-readout" className="ctl mono" onClick={onOpen}
      title="Connection status" style={{ gap: 10, cursor: "pointer" }}>
      {ORDER.map((name) => {
        const l = byName.get(name);
        return (
          <span key={name} data-testid={`lat-${LABEL[name]}`} style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
            <span style={{ width: 7, height: 7, borderRadius: "50%", background: l ? dotColor(l.status, palette) : palette.border }} />
            <span className="serif" style={{ fontSize: 9, letterSpacing: ".06em", textTransform: "uppercase", color: palette.textMuted }}>{LABEL[name]}</span>
            <span>{l && l.ms !== null ? l.ms : "—"}</span>
          </span>
        );
      })}
      <span style={{ color: palette.textMuted, fontSize: 9 }}>ms</span>
    </button>
  );
}
```

> `HealthStore` extends `ReactStore`, which exposes `subscribe(cb): () => void` and `getSnapshot(): S` (confirmed in `src/data/store.ts`) — used above.

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/LatencyReadout.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/LatencyReadout.tsx src/chrome/LatencyReadout.test.tsx
git commit -m "feat(ui): top-bar latency readout (eng/moo/tz, threshold-colored, click to open Connection panel)"
```

---

## Task 9: Top bar (replaces WorkspaceHeader)

**Files:**
- Create: `ui/src/chrome/TopBar.tsx`
- Create: `ui/src/chrome/TopBar.test.tsx`
- Modify: `ui/src/chrome/AppShell.tsx` (render `TopBar` instead of `WorkspaceHeader`; pass callbacks)
- Delete: `ui/src/chrome/WorkspaceHeader.tsx`, `ui/src/chrome/WorkspaceHeader.test.tsx`

**Interfaces:**
- Produces: `TopBar({ workspaceName, health, armed, onArmToggle, onAddPanel, onNewWindow, onOpenSettings, onOpenConnection }: TopBarProps): JSX.Element` — left→right: **eTape** serif wordmark + `· <workspaceName>` · `LatencyReadout` · spacer · **+ Add panel** · **⧉ New window** · **⚙ Settings** · **arm chip** (bronze `ARMED` / muted `DISARMED`). The four link-group symbol boxes are **gone**.
- Consumes: `LatencyReadout` (Task 8), `useTheme`. Arm state is passed in (AppShell derives it from `stores.exec.status()`), toggled via `onArmToggle` (same command as the account panel).

- [ ] **Step 1: Write `TopBar.test.tsx` (failing)**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TopBar } from "./TopBar";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";

const base = {
  workspaceName: "main", health: new HealthStore(), armed: false,
  onArmToggle: vi.fn(), onAddPanel: vi.fn(), onNewWindow: vi.fn(),
  onOpenSettings: vi.fn(), onOpenConnection: vi.fn(),
};

describe("TopBar", () => {
  it("renders wordmark, workspace name, and the shell buttons", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.getByText("eTape")).toBeInTheDocument();
    expect(screen.getByText("· main")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /add panel/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /new window/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /settings/i })).toBeInTheDocument();
  });
  it("arm chip reflects state and toggles", () => {
    render(<ThemeProvider><TopBar {...base} armed /></ThemeProvider>);
    const chip = screen.getByTestId("arm-chip");
    expect(chip).toHaveTextContent("ARMED");
    fireEvent.click(chip);
    expect(base.onArmToggle).toHaveBeenCalled();
  });
  it("has no link-group symbol boxes", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.queryByLabelText(/focus green/i)).toBeNull();
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/TopBar.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `TopBar.tsx`**

```tsx
import { LatencyReadout } from "./LatencyReadout";
import type { HealthStore } from "../data/HealthStore";
import { useTheme } from "./ThemeProvider";

export interface TopBarProps {
  workspaceName: string;
  health: HealthStore;
  armed: boolean;
  onArmToggle: () => void;
  onAddPanel: () => void;
  onNewWindow: () => void;
  onOpenSettings: () => void;
  onOpenConnection: () => void;
}

export function TopBar(p: TopBarProps): JSX.Element {
  const { palette } = useTheme();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "7px 12px",
      background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      <span className="serif" style={{ fontWeight: 600, fontSize: 14 }}>eTape</span>
      <span style={{ color: palette.textMuted }}>· {p.workspaceName}</span>
      <LatencyReadout health={p.health} onOpen={p.onOpenConnection} />
      <span style={{ flex: 1 }} />
      <button className="btn" onClick={p.onAddPanel}>+ Add panel</button>
      <button className="btn" onClick={p.onNewWindow}>⧉ New window</button>
      <button className="btn" aria-label="Settings" onClick={p.onOpenSettings}>⚙ Settings</button>
      <button data-testid="arm-chip" className="btn" onClick={p.onArmToggle}
        style={{ fontWeight: 600, letterSpacing: ".08em",
          color: p.armed ? palette.accent : palette.textMuted,
          borderColor: p.armed ? palette.accent : palette.borderStrong,
          background: p.armed ? "rgba(154,106,27,.12)" : "rgba(106,114,128,.12)" }}>
        {p.armed ? "ARMED" : "DISARMED"}
      </button>
    </div>
  );
}
```

- [ ] **Step 4: Wire it into `AppShell.tsx`**

Replace `<WorkspaceHeader workspaceName={workspaceName} linkGroups={linkGroups} />` with `<TopBar .../>`. AppShell currently has NO exec/armed wiring — add it net-new following `AccountBarPanel.tsx`: `const oc = useOrderCommands(commands, stores.exec, toast)` (get `toast` from `useToasts()` — the real hook, see below), `armed = stores.exec.status()?.masterArmed ?? false` (subscribe via `useSyncExternalStore` on `stores.exec`), `onArmToggle={() => (armed ? oc.disarm() : oc.arm())}`. Leave `onAddPanel`/`onNewWindow`/`onOpenSettings`/`onOpenConnection` as stubs that Task 10/11 implements (e.g. `() => {}` with a `// TODO(T10)` comment), so this task compiles and tests pass; do NOT delete the AppShell add/remove wiring that Task 10 fills in.

- [ ] **Step 5: Delete `WorkspaceHeader`**

```bash
cd ui && git rm src/chrome/WorkspaceHeader.tsx src/chrome/WorkspaceHeader.test.tsx
```

Then `grep -rn "WorkspaceHeader" src` — fix any remaining import (should be only `AppShell.tsx`, already replaced). `normalizeSymbol` is now imported from `./symbol` (Task 4) wherever needed.

- [ ] **Step 6: Run affected suites + tsc**

Run: `cd ui && npx vitest run src/chrome/TopBar.test.tsx && npx tsc --noEmit`
Expected: PASS / clean.

- [ ] **Step 7: Commit**

```bash
cd ui && git add src/chrome/TopBar.tsx src/chrome/TopBar.test.tsx src/chrome/AppShell.tsx
git commit -m "feat(ui): replace WorkspaceHeader with Daylight top bar (wordmark, latency, add-panel/new-window/settings, arm chip); remove symbol boxes"
```

---

## Task 10: Blank start, catalog, add/remove panel wiring, new window

**Files:**
- Create: `ui/src/chrome/Catalog.tsx` (presets-first content, reused by empty state + dropdown)
- Create: `ui/src/chrome/EmptyState.tsx`
- Create: `ui/src/chrome/Catalog.test.tsx`
- Modify: `ui/src/chrome/AppShell.tsx` (empty-state vs dockview switch; add/remove panel; preset apply with confirm; new-window; dynamic topic subscription)

**Interfaces:**
- Produces:
  - `Catalog({ onAddPanel, onApplyPreset }: { onAddPanel: (panelId: string) => void; onApplyPreset: (presetId: string) => void }): JSX.Element` — "Start from a preset" hero (Monitoring/Trading cards with layout thumbnails) beside "Or add panels one by one" (compact rows from `CATALOG`); dev panels shown only under `import.meta.env.DEV`.
  - `EmptyState(props)` — the blank-workspace hero wrapping `Catalog` with the heading + lede copy.
  - AppShell gains: `addPanel(panelId)` (allocate a fresh `PanelConfig` id, push to `ws.panels`, `api.addPanel`, save), `removePanel(id)` (drop from `ws.panels`, save; last panel → empty state), `applyPresetToWorkspace(presetId)` (confirm-replace if non-empty, write panels+layout, `applyPreset`).
- Consumes: `CATALOG`, `PANELS`, `isDevPanel` (Task 6); `PRESETS`, `applyPreset` (Task 7); `nextWindowName` (Task 5).

- [ ] **Step 1: Write `Catalog.test.tsx` (failing)**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Catalog } from "./Catalog";
import { ThemeProvider } from "./ThemeProvider";

describe("Catalog", () => {
  it("lists both presets and the non-dev panel index", () => {
    // Vitest sets NODE_ENV=test so import.meta.env.DEV is TRUE by default; stub it
    // false to assert the prod behaviour (Smoke hidden).
    vi.stubEnv("DEV", false);
    render(<ThemeProvider><Catalog onAddPanel={() => {}} onApplyPreset={() => {}} /></ThemeProvider>);
    expect(screen.getByText("Monitoring")).toBeInTheDocument();
    expect(screen.getByText("Trading")).toBeInTheDocument();
    expect(screen.getByText("Chart")).toBeInTheDocument();
    expect(screen.queryByText("Smoke")).toBeNull(); // dev panel hidden when DEV=false
    vi.unstubAllEnvs();
  });
  it("adds a panel on click and applies a preset on click", () => {
    const onAddPanel = vi.fn(), onApplyPreset = vi.fn();
    render(<ThemeProvider><Catalog onAddPanel={onAddPanel} onApplyPreset={onApplyPreset} /></ThemeProvider>);
    fireEvent.click(screen.getByText("Chart"));
    expect(onAddPanel).toHaveBeenCalledWith("chart");
    fireEvent.click(screen.getByText("Monitoring"));
    expect(onApplyPreset).toHaveBeenCalledWith("monitoring");
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/Catalog.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `Catalog.tsx`** (presets-first, variant 2 of the empty-state mockup)

```tsx
import { CATALOG, isDevPanel, PANELS } from "./panels/registry";
import { PRESETS } from "./presets";
import { useTheme } from "./ThemeProvider";

function Thumb({ kind }: { kind: "monitoring" | "trading" }): JSX.Element {
  // Simple grid thumbnail; green cells mark chart slots. Purely decorative.
  const cells = kind === "monitoring"
    ? { cols: "1fr 1fr 1fr", rows: "1fr 1fr", green: [0, 1, 2] }
    : { cols: "2fr 1fr 1fr", rows: "2fr 1fr", green: [0] };
  const { palette } = useTheme();
  return (
    <div style={{ width: 84, height: 56, border: `1px solid ${palette.borderStrong}`, borderRadius: 3,
      display: "grid", gap: 1, background: palette.border, gridTemplateColumns: cells.cols, gridTemplateRows: cells.rows }}>
      {Array.from({ length: 6 }, (_, i) => (
        <i key={i} style={{ background: cells.green.includes(i) ? "rgba(23,122,88,.18)" : palette.surface }} />
      ))}
    </div>
  );
}

export function Catalog({ onAddPanel, onApplyPreset }: { onAddPanel: (id: string) => void; onApplyPreset: (id: string) => void }): JSX.Element {
  const { palette } = useTheme();
  const panels = CATALOG.concat(import.meta.env.DEV && !CATALOG.some((c) => isDevPanel(c.panelId))
    ? [{ panelId: "smoke-painter", title: PANELS["smoke-painter"].title, glyph: PANELS["smoke-painter"].glyph, description: PANELS["smoke-painter"].description }]
    : []);
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1.1fr 1fr", gap: 28, alignItems: "start" }}>
      <div>
        <div className="col-head serif" style={{ borderBottom: `3px double ${palette.borderStrong}`, paddingBottom: 5, marginBottom: 12 }}>Start from a preset</div>
        {PRESETS.map((p) => (
          <button key={p.id} className="btn" onClick={() => onApplyPreset(p.id)}
            style={{ display: "flex", gap: 12, width: "100%", textAlign: "left", padding: 12, marginBottom: 12, alignItems: "center" }}>
            <Thumb kind={p.thumb} />
            <span><span className="serif" style={{ fontWeight: 600, display: "block" }}>{p.name}</span>
              <span style={{ color: palette.textMuted, fontSize: 10.5 }}>{p.description}</span></span>
          </button>
        ))}
      </div>
      <div>
        <div className="col-head serif" style={{ borderBottom: `3px double ${palette.borderStrong}`, paddingBottom: 5, marginBottom: 12 }}>Or add panels one by one</div>
        {panels.map((c) => (
          <div key={c.panelId} role="button" tabIndex={0} onClick={() => onAddPanel(c.panelId)}
            style={{ display: "flex", alignItems: "baseline", gap: 10, padding: "7px 2px", borderBottom: `1px solid ${palette.border}`, cursor: "pointer" }}>
            <span className="mono" style={{ color: palette.up, width: 24 }}>{c.glyph}</span>
            <span style={{ fontWeight: 600, width: 110 }}>{c.title}</span>
            <span style={{ color: palette.textMuted, fontSize: 10.5 }}>{c.description}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Implement `EmptyState.tsx`**

```tsx
import { Catalog } from "./Catalog";
import { useTheme } from "./ThemeProvider";

export function EmptyState({ onAddPanel, onApplyPreset }: { onAddPanel: (id: string) => void; onApplyPreset: (id: string) => void }): JSX.Element {
  const { palette } = useTheme();
  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", padding: "36px 40px", background: palette.bg }}>
      <div style={{ maxWidth: 720, width: "100%" }}>
        <h4 className="serif" style={{ fontWeight: 600, fontSize: 17, margin: "0 0 4px" }}>Empty workspace</h4>
        <p style={{ color: palette.textMuted, margin: "0 0 22px" }}>Load a preset and rearrange it, or build from the panel list. Everything is saved as you go.</p>
        <Catalog onAddPanel={onAddPanel} onApplyPreset={onApplyPreset} />
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Run Catalog test to pass**

Run: `cd ui && npx vitest run src/chrome/Catalog.test.tsx`
Expected: PASS.

- [ ] **Step 6: Wire AppShell — empty-state switch, add/remove, preset apply, new window, add-panel dropdown**

In `AppShell.tsx`:
- Render `<EmptyState .../>` in place of `<DockviewReact/>` when `ws.panels.length === 0`; otherwise render dockview.
- Hold the `DockviewApi` from `onReady` in a ref so `addPanel`/`removePanel`/`applyPresetToWorkspace` can call it.
- `addPanel(panelId)`: allocate `id = `${panelId}-${crypto.randomUUID().slice(0,8)}``, default `group` = `null`, default `settings` per panel (chart → `{ symbol: "US.AAPL", timeframe: "1m" }`, else `{}`); push to `ws.panels`, `api.addPanel({ id, component: id, title: PANELS[panelId].title })`, `workspaceStore.save(next)`. If the workspace was empty (first panel), this transitions out of the empty state on next render.
- `removePanel(id)`: filter `ws.panels`, `api.getPanel(id)?.api.close()`, save. When it hits 0, next render shows the empty state.
- Subscribe to dockview `onDidRemovePanel` to keep `ws.panels` in sync when the user closes a dockview tab (currently only layout is re-saved, not the panel list) — filter the removed id out of `ws.panels` and save.
- `applyPresetToWorkspace(presetId)`: find preset; if `ws.panels.length > 0`, `window.confirm("Replace current layout?")` (inline confirm) and bail if declined; build `{ panels, layout }`, set `ws`, `applyPreset(api, preset)`, save.
- `onNewWindow`: compute `nextWindowName` from known workspace names (best-effort: track opened windows in a `localStorage` key `etape.windows`, or just increment from `window-2`), `const url = `?workspace=${name}``, `const w = window.open(url, "_blank")`; if `!w` (popup blocked) call `toast.push(...)` with the URL to open manually. **Note:** the real toast hook is `useToasts()` (from `src/chrome/Toast.tsx`), returning `{ push, dismiss }` — there is no `useToast` singular. `ToastProvider` already wraps the app.
- Add-panel dropdown: a `+ Add panel` popover (Task 3 `.popover` class) rendering `<Catalog onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} />`, toggled by `TopBar`'s `onAddPanel`. Wire `onOpenConnection` to `addPanel("connection-status")` if absent else focus it.
- Dynamic topics: keep the Task 5 union subscription (over-subscribe the full catalog) so any added panel already has its data stream.

- [ ] **Step 7: tsc + full suite + manual smoke**

Run: `cd ui && npx tsc --noEmit && npm run test`
Expected: PASS. Then `npm run dev`: fresh `?workspace=main` shows the empty state; clicking **Chart** adds a chart and leaves empty state; **Monitoring** preset fills the 2×2 wall; closing all panels returns to empty state; **⧉ New window** opens `?workspace=window-2` blank.

- [ ] **Step 8: Commit**

```bash
cd ui && git add src/chrome/Catalog.tsx src/chrome/EmptyState.tsx src/chrome/Catalog.test.tsx src/chrome/AppShell.tsx
git commit -m "feat(ui): blank-start empty state + presets-first catalog, add/remove panel wiring, new-window, add-panel dropdown"
```

---

## Task 11: Unified Settings modal

**Files:**
- Create: `ui/src/chrome/SettingsModal.tsx`
- Create: `ui/src/chrome/AppearanceSection.tsx`
- Create: `ui/src/chrome/SettingsModal.test.tsx`
- Modify: `ui/src/chrome/exec/OrderSettingsModal.tsx` (extract its body as `OrderSettingsSection`, drop the outer overlay/title/close)
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx` (gear opens the unified Settings modal to the Orders section)
- Modify: `ui/src/chrome/AppShell.tsx` (own the Settings modal open/section state; `onOpenSettings`)

**Interfaces:**
- Produces:
  - `SettingsModal({ open, section, onSection, onClose }: { open: boolean; section: SettingsSection; onSection: (s: SettingsSection) => void; onClose: () => void }): JSX.Element | null` where `type SettingsSection = "appearance" | "orders" | "sounds"`. Renders a left nav (Appearance / Orders & hotkeys / Sounds) + the active section: Appearance = `<AppearanceSection/>`, Orders = `<OrderSettingsSection/>` (extracted), Sounds = `<SoundsSection/>`.
  - `AppearanceSection()` — Light/Dark radio bound to `useTheme().setMode`.
  - `OrderSettingsSection({ config, status, onSave })` — the existing template/hotkey/gate-limits editor sans its own overlay chrome.
- Consumes: `useTheme` (Appearance), `useOrderConfig`/`ExecStatus` (Orders), `SoundsSection` (prop-less).

- [ ] **Step 1: Write `SettingsModal.test.tsx` (failing)**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SettingsModal } from "./SettingsModal";
import { AppProviders } from "../test/providers"; // ThemeProvider + OrderConfigProvider + SoundConfigProvider wrapper

describe("SettingsModal", () => {
  it("returns null when closed", () => {
    const { container } = render(<AppProviders><SettingsModal open={false} section="appearance" onSection={() => {}} onClose={() => {}} /></AppProviders>);
    expect(container.firstChild).toBeNull();
  });
  it("shows the three sections and switches", () => {
    const onSection = vi.fn();
    render(<AppProviders><SettingsModal open section="appearance" onSection={onSection} onClose={() => {}} /></AppProviders>);
    expect(screen.getByRole("button", { name: /appearance/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /sounds/i }));
    expect(onSection).toHaveBeenCalledWith("sounds");
  });
  it("appearance toggles theme", () => {
    render(<AppProviders><SettingsModal open section="appearance" onSection={() => {}} onClose={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByLabelText(/dark/i));
    expect(document.documentElement.dataset.theme).toBe("dark");
  });
});
```

> No `ui/src/test/` dir exists yet — create `ui/src/test/providers.tsx` composing `ThemeProvider`/`OrderConfigProvider`/`SoundConfigProvider`. **`OrderConfigProvider` and `SoundConfigProvider` require `commands` (not optional) and call `commands.sendCommand(...)` unconditionally on mount** — pass a working stub, e.g. `const commands = { sendCommand: async () => ({ status: "accepted" as const }), sendQuery: async () => ({}) }`, not an empty object. (`ThemeProvider`'s `commands` is optional and guarded.) Reuse this wrapper in later component tests.

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/SettingsModal.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Extract `OrderSettingsSection` from `OrderSettingsModal.tsx`**

Split `OrderSettingsModal.tsx` so the template/hotkey/gate-limits body becomes `export function OrderSettingsSection({ config, status, onSave }: {...})` with no fixed-overlay wrapper, no title bar, no Cancel/Save footer (Save becomes an auto-persist-on-change or a section-local Save — keep the existing `onSave` semantics; simplest: keep a Save button inside the section). Keep `<SoundsSection/>` OUT of this section (it moves to the Sounds tab). Preserve every `data-testid` (`save`, template ids, hotkey capture) so `OrderSettingsModal.test.tsx` still passes — repoint that test to render `OrderSettingsSection`.

- [ ] **Step 4: Implement `AppearanceSection.tsx`**

```tsx
import { useTheme } from "./ThemeProvider";

export function AppearanceSection(): JSX.Element {
  const { mode, setMode } = useTheme();
  return (
    <div>
      <div className="col-head serif" style={{ marginBottom: 8 }}>Theme</div>
      <label style={{ display: "block", marginBottom: 6 }}>
        <input type="radio" name="theme" aria-label="Light" checked={mode === "light"} onChange={() => setMode("light")} /> Light (default)
      </label>
      <label style={{ display: "block" }}>
        <input type="radio" name="theme" aria-label="Dark" checked={mode === "dark"} onChange={() => setMode("dark")} /> Dark
      </label>
    </div>
  );
}
```

- [ ] **Step 5: Implement `SettingsModal.tsx`**

```tsx
import { AppearanceSection } from "./AppearanceSection";
import { OrderSettingsSection } from "./exec/OrderSettingsModal";
import { SoundsSection } from "../sound/SoundsSection";
import { useOrderConfig } from "./exec/useOrderConfig";
import { useTheme } from "./ThemeProvider";
import type { ExecStatus } from "../gen/wsmsg";

export type SettingsSection = "appearance" | "orders" | "sounds";
const NAV: { id: SettingsSection; label: string }[] = [
  { id: "appearance", label: "Appearance" }, { id: "orders", label: "Orders & hotkeys" }, { id: "sounds", label: "Sounds" },
];

export function SettingsModal({ open, section, onSection, onClose, status }:
  { open: boolean; section: SettingsSection; onSection: (s: SettingsSection) => void; onClose: () => void; status?: ExecStatus | null }): JSX.Element | null {
  const { palette } = useTheme();
  const oc = useOrderConfig();
  if (!open) return null;
  return (
    <div onClick={onClose} style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 10000 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ background: palette.surface, border: `1px solid ${palette.borderStrong}`, borderRadius: 6, width: 680, maxHeight: "82vh", overflow: "auto", display: "grid", gridTemplateColumns: "160px 1fr" }}>
        <nav style={{ borderRight: `1px solid ${palette.border}`, padding: 12 }}>
          <div className="serif" style={{ fontWeight: 600, marginBottom: 10 }}>Settings</div>
          {NAV.map((n) => (
            <button key={n.id} className="btn" aria-label={n.label} onClick={() => onSection(n.id)}
              style={{ display: "block", width: "100%", textAlign: "left", marginBottom: 4, background: section === n.id ? palette.bg : "transparent", borderColor: section === n.id ? palette.accent : "transparent" }}>
              {n.label}
            </button>
          ))}
        </nav>
        <section style={{ padding: 16 }}>
          {section === "appearance" && <AppearanceSection />}
          {section === "orders" && <OrderSettingsSection config={oc.config} status={status ?? null} onSave={oc.save} />}
          {section === "sounds" && <SoundsSection />}
        </section>
      </div>
    </div>
  );
}
```

> Confirm `useOrderConfig` exposes `{ config, save }` (recon: `useOrderConfig.tsx`). Adjust names to the real API.

- [ ] **Step 6: Redirect the ticket gear + own the modal in AppShell**

In `AppShell.tsx`, hold `const [settings, setSettings] = useState<{ open: boolean; section: SettingsSection }>({ open: false, section: "appearance" })`; pass `onOpenSettings={() => setSettings({ open: true, section: "appearance" })}` to `TopBar`; render `<SettingsModal open={settings.open} section={settings.section} onSection={(s) => setSettings((v) => ({ ...v, section: s }))} onClose={() => setSettings((v) => ({ ...v, open: false }))} status={execStatus} />`. In `OrderTicketPanel.tsx`, change the `⚙` handler from opening its local `OrderSettingsModal` to invoking a passed-down `onOpenSettings?: () => void` prop (threaded through `PanelProps` or a context) that opens Settings to the `"orders"` section; remove the local `showSettings` state + inline `<OrderSettingsModal/>`. Simplest wiring: add `onOpenOrderSettings` to a small React context provided by AppShell and consumed by the ticket. Keep the ticket's other behavior unchanged.

- [ ] **Step 7: Run affected suites + tsc**

Run: `cd ui && npx vitest run src/chrome/SettingsModal.test.tsx src/chrome/exec/OrderSettingsModal.test.tsx src/chrome/panels/OrderTicketPanel.test.tsx && npx tsc --noEmit`
Expected: PASS / clean.

- [ ] **Step 8: Commit**

```bash
cd ui && git add src/chrome/SettingsModal.tsx src/chrome/AppearanceSection.tsx src/chrome/SettingsModal.test.tsx src/chrome/exec/OrderSettingsModal.tsx src/chrome/exec/OrderSettingsModal.test.tsx src/chrome/panels/OrderTicketPanel.tsx src/chrome/AppShell.tsx src/test/providers.tsx
git commit -m "feat(ui): unified Settings modal (Appearance/Orders&hotkeys/Sounds); ticket gear opens it to Orders"
```

---

# Phase 3 — Panel frame

## Task 12: Ledger panel header + group swatch picker + focus ring

**Files:**
- Create: `ui/src/chrome/GroupPicker.tsx`
- Create: `ui/src/chrome/GroupPicker.test.tsx`
- Modify: `ui/src/chrome/PanelFrame.tsx` (ledger header: swatch+picker · symbol · per-type controls · close; `.panel-focused` when active)
- Modify: `ui/src/chrome/panels/registry.tsx` (`PanelProps` gains `active: boolean` and `onGroupChange`)
- Modify: `ui/src/chrome/AppShell.tsx` (wire dockview `onDidActivePanelChange` → active-panel id state → `PanelFrame`; support `group` mutation via `onConfigChange`)
- Create: `ui/src/chrome/PanelFrame.test.tsx`

**Interfaces:**
- Produces:
  - `GroupPicker({ group, onPick, onClose }: { group: LinkGroup; onPick: (g: LinkGroup) => void; onClose: () => void }): JSX.Element` — popover with Red/Green/Blue/Yellow rows + "Pinned — own symbol" + the hint line "Panels in the same group load the same symbol together."
  - `PanelFrame` header (left→right): **group swatch** (click → `GroupPicker`) · **symbol** (mono bold, on symbol-bearing panels) · per-type controls slot · **✕ close**; over a `3px double` rule (`.ledger-header`); `.panel-focused` applied when this panel is the active dockview panel.
  - `PanelProps` gains `active: boolean` and `onGroupChange: (group: LinkGroup) => void`.
- Consumes: `LinkGroup`/`LinkGroups` (link groups), `swatch` helper, `onConfigChange` (extended to persist `group`), symbol resolution (`linkGroups.symbolFor(config.group) ?? settings.symbol`).

- [ ] **Step 1: Write `GroupPicker.test.tsx` (failing)**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { GroupPicker } from "./GroupPicker";
import { ThemeProvider } from "./ThemeProvider";

describe("GroupPicker", () => {
  it("lists the four groups + pinned and reports the pick", () => {
    const onPick = vi.fn();
    render(<ThemeProvider><GroupPicker group="blue" onPick={onPick} onClose={() => {}} /></ThemeProvider>);
    expect(screen.getByText(/red group/i)).toBeInTheDocument();
    expect(screen.getByText(/pinned/i)).toBeInTheDocument();
    fireEvent.click(screen.getByText(/green group/i));
    expect(onPick).toHaveBeenCalledWith("green");
    fireEvent.click(screen.getByText(/pinned/i));
    expect(onPick).toHaveBeenCalledWith(null);
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/GroupPicker.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `GroupPicker.tsx`**

```tsx
import type { LinkGroup } from "./linkGroups";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

const GROUPS: Exclude<LinkGroup, null>[] = ["red", "green", "blue", "yellow"];
const sw = (g: Exclude<LinkGroup, null>, p: Palette): string =>
  ({ red: p.linkRed, green: p.linkGreen, blue: p.linkBlue, yellow: p.linkYellow }[g]);

export function GroupPicker({ group, onPick, onClose }: { group: LinkGroup; onPick: (g: LinkGroup) => void; onClose: () => void }): JSX.Element {
  const { palette } = useTheme();
  const row = (sel: boolean): React.CSSProperties => ({ display: "flex", alignItems: "center", gap: 8, padding: "4px 6px", borderRadius: 4, cursor: "pointer", fontSize: 11.5, background: sel ? palette.surface : "transparent", fontWeight: sel ? 600 : 400 });
  return (
    <div className="popover" style={{ top: 26, left: 6, width: 180 }} onMouseLeave={onClose}>
      <div className="col-head" style={{ marginBottom: 6 }}>Follows</div>
      {GROUPS.map((g) => (
        <div key={g} role="button" style={row(group === g)} onClick={() => { onPick(g); onClose(); }}>
          <span style={{ width: 10, height: 10, borderRadius: 2, background: sw(g, palette) }} /> {g[0].toUpperCase() + g.slice(1)} group
        </div>
      ))}
      <div role="button" style={row(group === null)} onClick={() => { onPick(null); onClose(); }}>
        <span style={{ width: 10, height: 10, borderRadius: 2, border: `1.5px solid ${palette.textMuted}` }} /> Pinned — own symbol
      </div>
      <div style={{ fontSize: 10, color: palette.textMuted, marginTop: 6, borderTop: `1px solid ${palette.border}`, paddingTop: 6, lineHeight: 1.4 }}>
        Panels in the same group load the same symbol together.
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/GroupPicker.test.tsx`
Expected: PASS.

- [ ] **Step 5: Rebuild the `PanelFrame` header (ledger anatomy + focus ring)**

Extend `PanelProps` in `registry.tsx`:

```ts
export interface PanelProps {
  // ...existing...
  active: boolean;
  onGroupChange: (group: LinkGroup) => void;
}
```

Rewrite `PanelFrame.tsx`'s header. Key behaviors:
- Root div gets `className={active ? "panel-focused" : undefined}`.
- Header uses `className="ledger-header"`.
- Left: group swatch button (`aria-label="link group"`) — colored square for a group, outlined square for pinned; click toggles a local `showPicker`; render `<GroupPicker group={config.group} onPick={onGroupChange} onClose={() => setShowPicker(false)} />` when open.
- Symbol slot (only when `PANELS[config.panelId].symbolBearing`): mono bold, showing the effective symbol (bare form via existing `bareSymbol` if available, else the resolved symbol). This slot becomes the type-to-load edit target in Task 13 (leave a stable `data-testid="panel-symbol"` and a hook point).
- Title: `PANELS[config.panelId].title` in serif when not symbol-bearing; symbol-bearing panels show symbol as the primary label with the title as the dockview tab.
- Per-type controls slot: keep whatever the panel body already renders (chart timeframe/indicators via `ChartControls`, tape min-size, scanner ⚙) — these stay in the body/controls strip; the frame header just hosts swatch+symbol+close for now, with a `children`/controls prop if a panel needs header controls. Do not regress existing controls.
- Right: `✕` close button (`aria-label="close panel"`) calling a new `onClose` behavior — wire it to dockview panel close (thread an `onRequestClose` from AppShell, or call `panel.api.close()`). Simplest: add `onClose?: () => void` to `PanelProps`, AppShell passes `() => removePanel(config.id)`.

Add `onGroupChange` handling: it calls `onConfigChange` but for the `group` field. Since `onConfigChange` today only replaces `settings`, extend AppShell's `onConfigChange` to accept an optional group change — cleanest is a separate `onGroupChange(panelId, group)` callback in AppShell that updates `ws.panels[i].group` and saves. Wire `PanelFrame`'s `onGroupChange` prop to it.

- [ ] **Step 6: Wire active-panel tracking in `AppShell.tsx`**

In `onReady`, register `event.api.onDidActivePanelChange((p) => setActiveId(p?.id ?? null))`; hold `const [activeId, setActiveId] = useState<string | null>(null)`; pass `active={p.id === activeId}` into each `PanelFrame`. Add `onGroupChange(panelId, group)` that mutates `ws.panels` + saves. Ensure the dockview panel factory still keys by `p.id` (no remount).

- [ ] **Step 7: Write `PanelFrame.test.tsx`**

Mount a `PanelFrame` for a symbol-bearing panel (e.g. `chart`) with a fake `LinkGroups`/`stores`/`scheduler` and assert: the ledger header renders the title/symbol; the swatch button opens the `GroupPicker`; picking a group calls `onGroupChange` with that group; `active` toggles the `panel-focused` class. Use the `test/providers` wrapper. Since `chart`/`ladder`/`tape` mount real canvas, prefer a non-canvas symbol-bearing panel for this test (or a stub body) to stay in the default pool — otherwise add the new test file to `poolMatchGlobs`.

Run: `cd ui && npx vitest run src/chrome/PanelFrame.test.tsx`
Expected: PASS (after implementation).

- [ ] **Step 8: tsc + full suite + manual smoke**

Run: `cd ui && npx tsc --noEmit && npm run test`
Expected: PASS. Smoke (`npm run dev`): panel headers show serif titles over the double rule; the active panel has a bronze ring + tinted header; clicking a panel's swatch opens the picker and re-links it (a grouped chart follows its group's symbol; "Pinned" detaches it); the ✕ closes the panel.

- [ ] **Step 9: Commit**

```bash
cd ui && git add src/chrome/GroupPicker.tsx src/chrome/GroupPicker.test.tsx src/chrome/PanelFrame.tsx src/chrome/PanelFrame.test.tsx src/chrome/panels/registry.tsx src/chrome/AppShell.tsx
git commit -m "feat(ui): ledger panel header (serif + double rule), group swatch picker, bronze focus ring wired to active dockview panel"
```

---

## Task 13: Type-to-load (inline header symbol editing)

**Files:**
- Create: `ui/src/chrome/typeToLoad.ts` (pure state machine)
- Create: `ui/src/chrome/typeToLoad.test.ts`
- Modify: `ui/src/chrome/PanelFrame.tsx` (inline header edit UI driven by the machine; Enter/Esc; error revert)
- Modify: `ui/src/chrome/PanelFrame.test.tsx` (add capture-rule cases)
- Modify: `ui/src/chrome/linkGroups.ts` (+ `linkGroups.test.ts`) — add an ack-aware `focusChecked` so a grouped commit can validate before moving the group
- Modify: `ui/src/App.tsx` — the `onEcho` callback must **return** the `FocusGroup` ack promise (today it discards it with `void`)

**Interfaces:**
- Produces:
  - `type TypeToLoadState = { editing: false } | { editing: true; draft: string }`.
  - `reduceTypeToLoad(state, event): TypeToLoadState` where `event` is `{ kind: "key"; key: string; ctrl: boolean; meta: boolean; alt: boolean } | { kind: "commit" } | { kind: "cancel" }`. Encodes every capture rule from the spec (below).
  - `canStartTypeToLoad(ctx: { active: boolean; symbolBearing: boolean; targetIsFormField: boolean; modalOpen: boolean }): boolean`.
- Consumes: `normalizeSymbol` (Task 4), `linkGroups.focus` (grouped) / `onConfigChange` (pinned), engine subscribe/backfill error → toast.

**Capture rules (from the spec, encoded as unit tests):**
- A printable char (`A–Z`, `0–9`, `.`) starts editing, seeded with that char — **only** if not already editing.
- Never starts while Ctrl/Cmd/Alt is held (order hotkeys unaffected).
- Never starts when a real input/textarea/select/contenteditable or any modal has focus.
- Only on the active, symbol-bearing panel; inactive panels ignore keys.
- While editing: Backspace edits the draft, Enter commits, Esc cancels; non-printable keys are ignored.

- [ ] **Step 1: Write `typeToLoad.test.ts` (failing) — table-driven**

```ts
import { describe, it, expect } from "vitest";
import { reduceTypeToLoad, canStartTypeToLoad, type TypeToLoadState } from "./typeToLoad";

const idle: TypeToLoadState = { editing: false };
const key = (k: string, mod: Partial<{ ctrl: boolean; meta: boolean; alt: boolean }> = {}) =>
  ({ kind: "key" as const, key: k, ctrl: false, meta: false, alt: false, ...mod });

describe("canStartTypeToLoad", () => {
  it("requires active + symbol-bearing, no form field, no modal", () => {
    expect(canStartTypeToLoad({ active: true, symbolBearing: true, targetIsFormField: false, modalOpen: false })).toBe(true);
    expect(canStartTypeToLoad({ active: false, symbolBearing: true, targetIsFormField: false, modalOpen: false })).toBe(false);
    expect(canStartTypeToLoad({ active: true, symbolBearing: false, targetIsFormField: false, modalOpen: false })).toBe(false);
    expect(canStartTypeToLoad({ active: true, symbolBearing: true, targetIsFormField: true, modalOpen: false })).toBe(false);
    expect(canStartTypeToLoad({ active: true, symbolBearing: true, targetIsFormField: false, modalOpen: true })).toBe(false);
  });
});

describe("reduceTypeToLoad", () => {
  it("printable char starts editing seeded with the char", () => {
    expect(reduceTypeToLoad(idle, key("n"))).toEqual({ editing: true, draft: "N" });
    expect(reduceTypeToLoad(idle, key("."))).toEqual({ editing: true, draft: "." });
    expect(reduceTypeToLoad(idle, key("5"))).toEqual({ editing: true, draft: "5" });
  });
  it("does not start with a modifier held", () => {
    expect(reduceTypeToLoad(idle, key("n", { ctrl: true }))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("n", { meta: true }))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("n", { alt: true }))).toEqual(idle);
  });
  it("ignores non-printables when idle", () => {
    expect(reduceTypeToLoad(idle, key("Enter"))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("Shift"))).toEqual(idle);
    expect(reduceTypeToLoad(idle, key("ArrowUp"))).toEqual(idle);
  });
  it("appends and uppercases while editing", () => {
    const s1 = reduceTypeToLoad({ editing: true, draft: "N" }, key("v"));
    expect(s1).toEqual({ editing: true, draft: "NV" });
  });
  it("Backspace trims; empty draft stays editing", () => {
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Backspace"))).toEqual({ editing: true, draft: "N" });
    expect(reduceTypeToLoad({ editing: true, draft: "N" }, key("Backspace"))).toEqual({ editing: true, draft: "" });
  });
  it("Esc/cancel and Enter/commit exit editing", () => {
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, { kind: "cancel" })).toEqual(idle);
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Escape"))).toEqual(idle);
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, { kind: "commit" })).toEqual(idle);
    expect(reduceTypeToLoad({ editing: true, draft: "NV" }, key("Enter"))).toEqual(idle);
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/typeToLoad.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement `ui/src/chrome/typeToLoad.ts`**

```ts
export type TypeToLoadState = { editing: false } | { editing: true; draft: string };
export type TypeToLoadEvent =
  | { kind: "key"; key: string; ctrl: boolean; meta: boolean; alt: boolean }
  | { kind: "commit" }
  | { kind: "cancel" };

const PRINTABLE = /^[A-Za-z0-9.]$/;

export function canStartTypeToLoad(ctx: { active: boolean; symbolBearing: boolean; targetIsFormField: boolean; modalOpen: boolean }): boolean {
  return ctx.active && ctx.symbolBearing && !ctx.targetIsFormField && !ctx.modalOpen;
}

export function reduceTypeToLoad(state: TypeToLoadState, ev: TypeToLoadEvent): TypeToLoadState {
  if (ev.kind === "cancel") return { editing: false };
  if (ev.kind === "commit") return { editing: false };
  // ev.kind === "key"
  if (ev.ctrl || ev.meta || ev.alt) return state; // never capture with a modifier
  if (!state.editing) {
    return PRINTABLE.test(ev.key) ? { editing: true, draft: ev.key.toUpperCase() } : state;
  }
  if (ev.key === "Enter") return { editing: false };
  if (ev.key === "Escape") return { editing: false };
  if (ev.key === "Backspace") return { editing: true, draft: state.draft.slice(0, -1) };
  if (PRINTABLE.test(ev.key)) return { editing: true, draft: (state.draft + ev.key).toUpperCase() };
  return state; // ignore other non-printables while editing
}
```

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/typeToLoad.test.ts`
Expected: PASS.

- [ ] **Step 5: Add an ack-aware `focusChecked` to `LinkGroups` (never a half-switched group)**

Today `LinkGroups.focus(group, symbol)` synchronously `setLocal`s + `bus.post`s + fires `onEcho`, and `App.tsx` wires `onEcho` as `(group, symbol) => { void client.sendCommand("FocusGroup", { group, symbol }); }` — the ack is discarded, so a bad symbol can't be caught. Change:
- In `App.tsx`, make `onEcho` **return** the ack promise: `(group, symbol) => client.sendCommand("FocusGroup", { group, symbol })`. Widen the `onEcho` type in `LinkGroups`' constructor to `(group, symbol) => Promise<AckMsg> | void`.
- Add to `LinkGroups`:

```ts
/** Validate with the engine BEFORE moving the group; returns false (group unchanged) on reject. */
async focusChecked(group: Exclude<LinkGroup, null>, symbol: string): Promise<{ ok: true } | { ok: false; reason: string }> {
  const ackP = this.onEcho(group, symbol);
  const ack = ackP && "then" in ackP ? await ackP : { status: "accepted" as const };
  if (ack.status !== "accepted") return { ok: false, reason: ack.reason ?? "symbol rejected" };
  this.setLocalPublic(group, symbol); // setLocal + bus.post (extract a small helper; keep remote path unchanged)
  this.bus.post({ group, symbol });
  return { ok: true };
}
```

Keep the existing synchronous `focus` for the remote-bus path and any non-validated caller; `focusChecked` is the type-to-load commit path. Add a `linkGroups.test.ts` case: a rejecting `onEcho` leaves `symbolFor(group)` unchanged and returns `{ ok: false }`; an accepting one updates it and broadcasts. (Confirm `AckMsg` has `status`/`reason` — it does: `src/gen/wsmsg.ts`.)

- [ ] **Step 6: Wire the machine into `PanelFrame.tsx`**

- Hold `const [tl, setTl] = useState<TypeToLoadState>({ editing: false })`.
- Attach a `keydown` handler on the frame root (only meaningful when `active`). Determine `targetIsFormField` from `document.activeElement` tag (`INPUT`/`TEXTAREA`/`SELECT`/`isContentEditable`); `modalOpen` from a passed-in flag/context (Settings/OrderSettings). If idle and `canStartTypeToLoad(...)`, feed the key; if editing, feed it and call **both** `e.preventDefault()` AND `e.stopPropagation()` on printables/Backspace/Enter/Escape. `stopPropagation` is required, not optional: `useHotkeys` (`src/chrome/exec/useHotkeys.ts`) listens on `window` in the bubble phase and does NOT check `defaultPrevented` — `OrderSettingsModal.tsx` documents this exact hazard and stops propagation. Without it, a user-rebound bare-key hotkey would fire alongside type-to-load.
- When editing, render the symbol slot as the bronze underlined edit affordance (`.symedit` styling from `panel-frame-v2.html`: `color: var(--accent); border-bottom: 2px solid var(--accent)`) with a caret, seeded from `draft`, and a right-aligned hint `⏎ load · esc keep <currentSymbol>`.
- **Commit (Enter):** `const sym = normalizeSymbol(tl.draft)`; then:
  - grouped panel (`config.group !== null`): `const r = await linkGroups.focusChecked(config.group, sym)`; if `r.ok` the group (all member panels across windows) follows; if `!r.ok`, `toast.push({ ... r.reason })` and **do not** move the group — the header reverts to the current symbol. Never a half-switched group.
  - pinned panel (`config.group === null`): `onConfigChange({ ...config.settings, symbol: sym })` (no engine validation gate today; the panel's own subscribe error, if any, surfaces on the existing error channel).
  - Then `setTl({ editing: false })`.
- **Cancel (Esc / blur / focus loss):** `setTl({ editing: false })` — header reverts to the current symbol (no state change).
- Get `toast` from `useToasts()` (the real hook; `{ push, dismiss }`).

- [ ] **Step 7: Add capture-rule component cases to `PanelFrame.test.tsx`**

Add jsdom cases: typing `N`,`V`,`D`,`A` on the active symbol-bearing panel shows `NVDA` in the edit slot; `Enter` on a grouped panel calls the fake `linkGroups.focusChecked("blue", "US.NVDA")` (stub it resolving `{ ok: true }`); a rejecting `focusChecked` (`{ ok: false, reason }`) pushes a toast and leaves the symbol unchanged; `Enter` on a pinned panel calls `onConfigChange` with `symbol: "US.NVDA"`; `Escape` restores; a keystroke while `active={false}` does nothing; a keystroke with `ctrlKey` does nothing.

Run: `cd ui && npx vitest run src/chrome/PanelFrame.test.tsx`
Expected: PASS.

- [ ] **Step 8: tsc + full suite + manual smoke**

Run: `cd ui && npx tsc --noEmit && npm run test`
Expected: PASS. Smoke: focus a blue-group chart, type `NVDA`, Enter → the chart + any blue DOM/tape follow; type a bad symbol → toast + header reverts, group unchanged; Esc mid-type restores; order hotkeys still fire (Ctrl-held keys don't start editing).

- [ ] **Step 9: Commit**

```bash
cd ui && git add src/chrome/typeToLoad.ts src/chrome/typeToLoad.test.ts src/chrome/PanelFrame.tsx src/chrome/PanelFrame.test.tsx src/chrome/linkGroups.ts src/chrome/linkGroups.test.ts src/App.tsx
git commit -m "feat(ui): type-to-load — inline header symbol editing with a unit-tested capture state machine; ack-checked group commit + bad-symbol revert"
```

---

# Phase 4 — Data-surface painters

## Task 14: Two-column DOM ladder

**Files:**
- Modify: `ui/src/render/ladder/paintLadder.ts` (classic two-column layout; drop CUM; center divider; outward depth bars)
- Modify: `ui/src/render/ladder/ladderState.ts` (only if geometry helpers need to move; the state shape is unchanged)
- Modify: `ui/src/render/ladder/ladderState.test.ts` (adjust only if a helper signature changes)
- Modify: `ui/test/golden/ladder.golden.test.ts` (new geometry constants + new goldens)
- Regenerate: ladder goldens

**Interfaces:**
- Consumes: `LadderPaintState` (unchanged: `symbol, entitled, asks[], bids[], decimals, spread, last, flash, orders, nowMs, width, height, palette`). `paintLadder(ctx, s)` signature unchanged.
- Produces: same painter contract; internal layout rewritten. `LADDER_ROW_H` stays exported (panels use it for sizing).

**Layout (from `panels-review-v3.html` / `presets.html`):**
- Spread line at top (`232.47 × 232.48 · spread 1¢`), centered, muted, mono, `borderBottom`.
- Column header row: `Size | Bid | (divider) | Ask | Size` — uppercase 9.5px muted.
- Two columns: bids left (best at top, descending down), asks right (best at top, ascending down), a 1px center divider (`border`/`borderStrong`).
- Depth bars grow **outward from the divider**: bid bars fill leftward from the center (`linear-gradient(to left, depthBid var(--d), transparent var(--d))`), ask bars fill rightward (`to right, depthAsk ...`). Cumulative reads from the bar length (`cumFraction`); the separate **Cum column is dropped**.
- Bid prices in `up`/green, ask prices in `down`/red, sizes right-aligned in text color.
- Working orders: **bronze inner edge** on the price row (`inset ±3px 0 0 orderMark`, on the divider side). Keep the last-trade flash overlay behavior (per row, `flashAlpha` decay over `FLASH_MS`).
- Preserve the early-return branches: not-entitled and waiting-for-depth (each keeps its golden).

- [ ] **Step 1: Update the golden fixture geometry + add new-layout cases (failing)**

In `ui/test/golden/ladder.golden.test.ts`, recompute `H` for the new layout and add goldens for: full book with working-order marks (both sides), one-sided book (bids only / asks only), empty book, no-entitlement, waiting-for-depth — each × light/dark. Use fixed `NOW`, `width`, `palette` as today. Compute `H` from the new constants (spread row + header row + `LADDER_LEVELS`×`LADDER_ROW_H`). Name new goldens `ladder2col-<case>-<mode>`.

Run: `cd ui && npm run test:golden -- ladder` (or run the whole golden dir) — expect FAIL (missing/renamed goldens) until step 3+.

- [ ] **Step 2: Rewrite `paintLadder.ts`**

Rewrite the body to the two-column layout. Skeleton (fill in against the real state fields; keep pure — read only `s.*`):

```ts
export const LADDER_ROW_H = 22;
const SPREAD_H = 18;
const HEADER_H = 18;
const PAD = 8;
const ORDER_EDGE = 3;

export function paintLadder(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  const w = s.width;
  ctx.clearRect(0, 0, w, s.height);
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, w, s.height);

  // early returns preserved: no entitlement / waiting for depth
  if (!s.entitled) { drawCentered(ctx, s, "L2 depth not entitled"); return; }
  if (s.asks.length === 0 && s.bids.length === 0) { drawCentered(ctx, s, "waiting for depth…"); return; }

  const mid = Math.round(w / 2);
  // spread line
  ctx.font = `10px ${FONTS.mono}`; ctx.fillStyle = p.textMuted; ctx.textAlign = "center"; ctx.textBaseline = "middle";
  if (s.spread !== null) ctx.fillText(spreadLabel(s), mid, SPREAD_H / 2);
  // column header
  const headY = SPREAD_H + HEADER_H / 2;
  // Use FONTS.mono for ALL canvas text: the golden harness only registers IBM
  // Plex Mono, so any FONTS.sans text would render with a non-deterministic
  // node-canvas fallback and defeat the pixel goldens. (Uppercase mono reads fine
  // for the tiny column header.) Same rule applies to the tape painter.
  ctx.font = `9.5px ${FONTS.mono}`; ctx.fillStyle = p.textMuted;
  ctx.textAlign = "right"; ctx.fillText("SIZE", mid - PAD, headY); ctx.fillText("BID", mid - (mid - PAD) / 2, headY);
  ctx.textAlign = "left"; ctx.fillText("ASK", mid + PAD, headY); ctx.textAlign = "right"; ctx.fillText("SIZE", w - PAD, headY);
  // divider
  ctx.strokeStyle = p.border; ctx.beginPath(); ctx.moveTo(mid + 0.5, SPREAD_H); ctx.lineTo(mid + 0.5, s.height); ctx.stroke();

  const top = SPREAD_H + HEADER_H;
  for (let i = 0; i < s.bids.length; i++) drawSide(ctx, s, s.bids[i], "bid", mid, top + i * LADDER_ROW_H);
  for (let i = 0; i < s.asks.length; i++) drawSide(ctx, s, s.asks[i], "ask", mid, top + i * LADDER_ROW_H);
}
```

`drawSide(ctx, s, row, side, mid, y)`:
- Depth bar: `barLen = row.cumFraction * (side === "bid" ? mid : w - mid)`; bid fills `[mid - barLen, mid]` (grows left), ask fills `[mid, mid + barLen]` (grows right); fill `p.depthBid`/`p.depthAsk`.
- Last-trade flash: if `s.flash && s.flash.price === row.price && flashAlpha(...) > 0`, overlay the half-row at `alpha` in `flashBuy/flashSell/flashNeutral` (reuse existing `flashAlpha`).
- Text: price (`up` for bid, `down` for ask) near the divider, size at the outer edge — bids `textAlign="right"` at `mid - PAD` (price) and left size at `PAD` (right-aligned to `mid - PAD/2`), asks mirrored. Match the mockup's `Size | Bid ‖ Ask | Size` column order.
- Working-order mark: if any `s.orders` matches `row.price`, draw a bronze inner edge (`fillRect` a 3px bar on the divider side) using `p.orderMark`.

Keep `spreadLabel(s)` and any `formatSize`/`formatPrice` helpers. Remove the CUM column and the old absolute stacked layout.

- [ ] **Step 3: Regenerate + verify ladder goldens**

Run: `cd ui && npm run test:golden:update` then eyeball `ui/test/golden/__output__/ladder2col-*.png` (bids left green, asks right red, bars growing outward from center, bronze order edge). Then:
Run: `cd ui && npm run test:golden`
Expected: PASS. Delete any now-orphaned old `ladder-*` golden PNGs the renamed tests no longer reference (`git rm`).

- [ ] **Step 4: Run ladder unit tests + component test**

Run: `cd ui && npx vitest run src/render/ladder/ladderState.test.ts src/chrome/panels/LadderPanel.test.tsx`
Expected: PASS. (LadderPanel test is already in the `forks` pool; if `H`/sizing assertions exist there, update them to the new geometry.)

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/render/ladder test/golden/ladder.golden.test.ts test/golden/goldens
git commit -m "feat(ui): classic two-column DOM ladder (bids left / asks right, outward depth bars, no Cum column, bronze order edge) + new goldens"
```

---

## Task 15: Full-row-colored time & sales tape

**Files:**
- Modify: `ui/src/render/tape/tapeState.ts` (`TapeRow` gains `isBlock: boolean`; compute in `buildTapeRows`)
- Modify: `ui/src/render/tape/tapeState.test.ts` (assert `isBlock` threshold)
- Modify: `ui/src/render/tape/paintTape.ts` (full-row background color, dimmed timestamp within the row color, bold ≥10k blocks)
- Modify: `ui/test/golden/tape.golden.test.ts` (new goldens: buy/sell/neutral rows, bold block, min-size filter)
- Regenerate: tape goldens

**Interfaces:**
- Produces: `TapeRow` gains `isBlock: boolean` (true when raw size ≥ `BLOCK_THRESHOLD = 10_000`); `export const BLOCK_THRESHOLD = 10_000`.
- Consumes: `TapePaintState` (unchanged shape). `paintTape(ctx, s)` signature unchanged.

- [ ] **Step 1: Add the `isBlock` test (failing)**

In `ui/src/render/tape/tapeState.test.ts`:

```ts
import { BLOCK_THRESHOLD } from "./tapeState";
it("flags prints >= the block threshold", () => {
  // build a source with one 10k print and one small print; assert row.isBlock
  // (use the existing test's source-builder helpers)
  expect(BLOCK_THRESHOLD).toBe(10_000);
  // ...construct rows and assert the 12,500-share row has isBlock true, the 400-share row false
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/render/tape/tapeState.test.ts`
Expected: FAIL (`BLOCK_THRESHOLD` / `isBlock` missing).

- [ ] **Step 3: Add `isBlock` in `tapeState.ts`**

```ts
export const BLOCK_THRESHOLD = 10_000; // shares; v1.1 fixed (see spec open items)
```

In the `TapeRow` interface add `isBlock: boolean;`. In `buildTapeRows`, when constructing each row, compute `isBlock: t.size >= BLOCK_THRESHOLD` from the **raw** numeric tick size (before it's formatted to the `size` string).

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/render/tape/tapeState.test.ts`
Expected: PASS.

- [ ] **Step 5: Rewrite the tape row loop in `paintTape.ts`**

```ts
for (let i = 0; i < s.rows.length; i++) {
  const top = i * TAPE_ROW_H;
  if (top > s.height) break;
  const r = s.rows[i];
  const midY = top + TAPE_ROW_H / 2;
  const dir = r.direction === "BUY" ? p.up : r.direction === "SELL" ? p.down : p.neutral;
  // full-row tint background
  ctx.fillStyle = r.direction === "BUY" ? p.flashBuy : r.direction === "SELL" ? p.flashSell : p.flashNeutral;
  ctx.fillRect(0, top, s.width, TAPE_ROW_H);
  ctx.font = `${r.isBlock ? "600 " : ""}11px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  // timestamp dimmed within the row color
  ctx.globalAlpha = 0.65; ctx.fillStyle = dir; ctx.textAlign = "left"; ctx.fillText(r.time, PAD, midY); ctx.globalAlpha = 1;
  // price + size at full strength in the direction color
  ctx.fillStyle = dir; ctx.textAlign = "right";
  ctx.fillText(r.price, s.width * 0.68, midY);
  ctx.fillText(r.size, s.width - PAD, midY);
}
```

Keep the `s.paused` top strip (2px `p.warn` bar) exactly as today.

- [ ] **Step 6: Update tape goldens**

In `ui/test/golden/tape.golden.test.ts`, add/rename cases: live (buy/sell/neutral mix), a row ≥10k rendered bold, min-size-filtered view, paused, paused-empty — each × light/dark. Name new goldens `tapecolor-<case>-<mode>`.

Run: `cd ui && npm run test:golden:update` → eyeball `__output__/tapecolor-*.png` (whole rows tinted by direction, dimmed timestamps, bold blocks). Then:
Run: `cd ui && npm run test:golden`
Expected: PASS. `git rm` orphaned old `tape-*` goldens.

- [ ] **Step 7: Component test + full suite**

Run: `cd ui && npx vitest run src/chrome/panels/TapePanel.test.tsx && npm run test`
Expected: PASS. (TapePanel test is already in the `forks` pool.)

- [ ] **Step 8: Commit**

```bash
cd ui && git add src/render/tape test/golden/tape.golden.test.ts test/golden/goldens
git commit -m "feat(ui): full-row-colored time & sales (direction tint, dimmed timestamp, bold >=10k blocks) + new goldens"
```

---

# Phase 5 — Tables & panel presentation

## Task 16: Sortable-columns utility

**Files:**
- Create: `ui/src/chrome/sortColumns.ts`
- Create: `ui/src/chrome/sortColumns.test.ts`

**Interfaces:**
- Produces:
  - `type SortState = { col: string; dir: "asc" | "desc" } | null`.
  - `toggleSort(state: SortState, col: string): SortState` — click cycles: unset → desc → asc → desc… on the same col; clicking a new col starts at desc.
  - `sortRows<T>(rows: T[], state: SortState, accessors: Record<string, (r: T) => number | string | null>): T[]` — pure, non-mutating, stable; `null` values sort last regardless of direction.
  - `sortIndicator(state, col): "" | "▴" | "▾"`.
- Consumes: nothing. Used by T17 (Scanner/Movers), T19 (positions), T20 (open orders); sort state persists in panel config (`settings.sort`) via `onConfigChange`.

- [ ] **Step 1: Write `sortColumns.test.ts` (failing)**

```ts
import { describe, it, expect } from "vitest";
import { toggleSort, sortRows, sortIndicator, type SortState } from "./sortColumns";

const rows = [{ s: "B", n: 2 }, { s: "A", n: null }, { s: "C", n: 1 }];
const acc = { sym: (r: typeof rows[number]) => r.s, val: (r: typeof rows[number]) => r.n };

describe("toggleSort", () => {
  it("unset → desc → asc → desc on same col", () => {
    let st: SortState = null;
    st = toggleSort(st, "val"); expect(st).toEqual({ col: "val", dir: "desc" });
    st = toggleSort(st, "val"); expect(st).toEqual({ col: "val", dir: "asc" });
    st = toggleSort(st, "val"); expect(st).toEqual({ col: "val", dir: "desc" });
  });
  it("new col starts at desc", () => expect(toggleSort({ col: "val", dir: "asc" }, "sym")).toEqual({ col: "sym", dir: "desc" }));
});

describe("sortRows", () => {
  it("nulls sort last in both directions; stable otherwise", () => {
    const desc = sortRows(rows, { col: "val", dir: "desc" }, acc).map((r) => r.s);
    expect(desc).toEqual(["B", "C", "A"]); // 2,1, then null
    const asc = sortRows(rows, { col: "val", dir: "asc" }, acc).map((r) => r.s);
    expect(asc).toEqual(["C", "B", "A"]); // 1,2, then null
  });
  it("null state returns input order (copy)", () => {
    const out = sortRows(rows, null, acc);
    expect(out).toEqual(rows); expect(out).not.toBe(rows);
  });
});

describe("sortIndicator", () => {
  it("marks only the active column", () => {
    expect(sortIndicator({ col: "val", dir: "asc" }, "val")).toBe("▴");
    expect(sortIndicator({ col: "val", dir: "desc" }, "val")).toBe("▾");
    expect(sortIndicator({ col: "val", dir: "desc" }, "sym")).toBe("");
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/sortColumns.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement `ui/src/chrome/sortColumns.ts`**

```ts
export type SortDir = "asc" | "desc";
export type SortState = { col: string; dir: SortDir } | null;

export function toggleSort(state: SortState, col: string): SortState {
  if (!state || state.col !== col) return { col, dir: "desc" };
  return { col, dir: state.dir === "desc" ? "asc" : "desc" };
}

export function sortRows<T>(rows: T[], state: SortState, accessors: Record<string, (r: T) => number | string | null>): T[] {
  const copy = rows.slice();
  if (!state) return copy;
  const get = accessors[state.col];
  if (!get) return copy;
  const mul = state.dir === "asc" ? 1 : -1;
  return copy
    .map((r, i) => ({ r, i, v: get(r) }))
    .sort((a, b) => {
      if (a.v === null && b.v === null) return a.i - b.i;
      if (a.v === null) return 1;   // nulls last regardless of dir
      if (b.v === null) return -1;
      const c = typeof a.v === "number" && typeof b.v === "number" ? a.v - b.v : String(a.v).localeCompare(String(b.v));
      return c !== 0 ? c * mul : a.i - b.i; // stable tiebreak
    })
    .map((x) => x.r);
}

export function sortIndicator(state: SortState, col: string): "" | "▴" | "▾" {
  if (!state || state.col !== col) return "";
  return state.dir === "asc" ? "▴" : "▾";
}
```

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/sortColumns.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/sortColumns.ts src/chrome/sortColumns.test.ts
git commit -m "feat(ui): shared sortable-columns utility (toggle cycle, stable null-last sort, ▴/▾ indicator)"
```

---

## Task 17: Scanner / Movers — filters popover, summary line, sortable columns

**Files:**
- Modify: `ui/src/chrome/panels/ScannerPanel.tsx`
- Modify: `ui/src/chrome/panels/scannerFilter.ts` (filter-summary formatter; keep the 3 existing threshold fields)
- Modify: `ui/src/chrome/panels/scannerFilter.test.ts`
- Modify: `ui/src/chrome/panels/ScannerPanel.test.tsx`

**Interfaces:**
- Produces: `formatFilterSummary(t: ScannerThresholds): string` — e.g. `"change ≥ 10% · float ≤ 20M · vol ≥ 100k"`; `null`/`0` fields render as omitted or `off` per the mockup (`float ≤` omitted when null).
- Consumes: `ScannerThresholds` (unchanged 3 fields — no price filter exists; do not invent one), `applyScannerFilters`/`sortByChangeDesc`, `sortColumns` utility (T16), `onConfigChange` for `settings.sort` + `settings.thresholds`.

**Presentation deltas (from mockup):** the inline input row is **gone**; parameters live behind a **⚙ filters** popover in the header (`.popover`, with Reset defaults + Apply); a one-line mono summary sits under the header; new-hit rows keep the bronze left edge + tint for one refresh (`accent`+alpha + `inset 2px 0 0 accent`); already-seen rows muted (opacity ~0.55); "no print yet" honest states kept. Default sort: % change descending. Sortable headers (Sym, %, Last, Float, Vol) with `▴/▾`.

- [ ] **Step 1: Add `formatFilterSummary` test (failing)** in `scannerFilter.test.ts`

```ts
import { formatFilterSummary } from "./scannerFilter";
describe("formatFilterSummary", () => {
  it("formats set fields with human units, omits nulls/zeros", () => {
    expect(formatFilterSummary({ minChangePct: 10, floatCapShares: 20_000_000, minVolume: 100_000 }))
      .toBe("change ≥ 10% · float ≤ 20M · vol ≥ 100k");
    expect(formatFilterSummary({ minChangePct: 5, floatCapShares: null, minVolume: 0 }))
      .toBe("change ≥ 5%");
  });
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/panels/scannerFilter.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement `formatFilterSummary` in `scannerFilter.ts`**

```ts
const compact = (n: number): string =>
  n >= 1_000_000 ? `${+(n / 1_000_000).toFixed(1)}M` : n >= 1_000 ? `${+(n / 1_000).toFixed(0)}k` : `${n}`;

export function formatFilterSummary(t: ScannerThresholds): string {
  const parts: string[] = [];
  if (t.minChangePct > 0) parts.push(`change ≥ ${t.minChangePct}%`);
  if (t.floatCapShares !== null) parts.push(`float ≤ ${compact(t.floatCapShares)}`);
  if (t.minVolume > 0) parts.push(`vol ≥ ${compact(t.minVolume)}`);
  return parts.length ? parts.join(" · ") : "no filters";
}
```

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/panels/scannerFilter.test.ts`
Expected: PASS.

- [ ] **Step 5: Rework `ScannerPanel.tsx`**

- Remove the inline `<input>` filter row.
- Header: session tag + `updated <time>` + a `⚙ filters` button that toggles a `.popover` containing the three number inputs (min change %, float ≤, vol ≥ — keep `aria-label`s `min change %`/`float cap`/`min volume` so existing tests resolve), a **Reset defaults** button, and an **Apply** button that `onConfigChange({ ...settings, thresholds })`.
- Under the header: a `<div className="mono">` rendering `formatFilterSummary(thresholds)`.
- Table headers become sortable via T16: hold `sort` from `config.settings.sort` (default `{ col: "changePct", dir: "desc" }`), `onClick` → `toggleSort` → `onConfigChange({ ...settings, sort })`; render `sortIndicator` on each `<th>` with `.col-head.sort-active` on the active column. Apply `sortRows(view.rows, sort, accessors)` (accessors: `sym`→symbol, `changePct`, `last`, `float`, `vol`); when `sort` is the default, this equals today's `sortByChangeDesc`.
- Keep new-hit tint (`isNewHit`) and seen muting (`muted`) — restyle to bronze edge (`boxShadow: inset 2px 0 0 var(--accent)` + `background: rgba(154,106,27,.10)`).
- Movers reuse is unchanged (still `session="rth"`).

- [ ] **Step 6: Update `ScannerPanel.test.tsx`**

Assert: no persistent input row on load; the ⚙ button reveals the inputs; the summary line reflects thresholds; clicking the `%` header toggles sort and persists (`onConfigChange` called with a `sort` key); default view is %-desc. Keep the existing new-hit/seen assertions.

Run: `cd ui && npx vitest run src/chrome/panels/ScannerPanel.test.tsx`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd ui && git add src/chrome/panels/ScannerPanel.tsx src/chrome/panels/scannerFilter.ts src/chrome/panels/scannerFilter.test.ts src/chrome/panels/ScannerPanel.test.tsx
git commit -m "feat(ui): scanner/movers filters popover + one-line summary + sortable columns (default %-desc)"
```

---

## Task 18: News — today/fresh treatment

**Files:**
- Modify: `ui/src/chrome/panels/NewsPanel.tsx`
- Modify: `ui/src/chrome/panels/NewsPanel.test.tsx`

**Interfaces:**
- Consumes: `NewsStore` items (unchanged), `formatTapeTime`. Adds a pure date-classifier for testability.

**Presentation deltas:** each row shows headline + meta line **date + seen-time + source** in mono. Rows from **today** get the bronze left edge + tint (same "fresh" language as scanner hits) with the date rendered as `"today"`; older rows show `"Jul 4"`-style muted dates.

- [ ] **Step 1: Add a date-classifier test (failing)** in `NewsPanel.test.tsx`

Add a small exported helper `newsDateLabel(seenAtISO: string, nowMs: number): { label: string; today: boolean }` and test it:

```ts
import { newsDateLabel } from "./NewsPanel";
it("labels today vs older dates", () => {
  const now = Date.parse("2026-07-07T12:00:00Z");
  expect(newsDateLabel("2026-07-07T09:00:00Z", now)).toEqual({ label: "today", today: true });
  expect(newsDateLabel("2026-07-04T16:00:00Z", now).today).toBe(false);
  expect(newsDateLabel("2026-07-04T16:00:00Z", now).label).toMatch(/Jul\s*4/);
});
```

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/panels/NewsPanel.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `newsDateLabel` + apply in the row**

```ts
export function newsDateLabel(seenAtISO: string, nowMs: number): { label: string; today: boolean } {
  const d = new Date(seenAtISO);
  const now = new Date(nowMs);
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  if (sameDay) return { label: "today", today: true };
  return { label: d.toLocaleDateString("en-US", { month: "short", day: "numeric" }), today: false };
}
```

In the row: meta line becomes `<span className="mono">{label} · {seenTime} · {source}</span>`; today rows get `style={{ background: "rgba(154,106,27,.08)", boxShadow: "inset 2px 0 0 var(--accent)" }}` and the date span in `accent`; older rows show the date muted. Use `Date.now()` for `nowMs` in the component (pass it in tests). Keep the empty `halt-slot`.

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/panels/NewsPanel.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/panels/NewsPanel.tsx src/chrome/panels/NewsPanel.test.tsx
git commit -m "feat(ui): news rows show date+seen-time+source; today rows get the bronze fresh edge + 'today' label"
```

---

## Task 19: Merge Account + Positions into one Account panel

**Files:**
- Create: `ui/src/chrome/panels/AccountPanel.tsx` (stats strip + arm chip + sortable positions table)
- Create: `ui/src/chrome/panels/AccountPanel.test.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (register `account`; keep `account-bar`/`positions` as aliases pointing at `AccountPanel` for back-compat with saved docs, or map them; add the single `account` CATALOG row and drop the two old rows)
- Modify: `ui/src/chrome/presets.ts` (swap the Trading preset's `t-account` slot to the merged `account`)
- Delete: `ui/src/chrome/panels/AccountBarPanel.tsx` + test, `ui/src/chrome/panels/PositionsPanel.tsx` + test (fold their behavior into `AccountPanel`)

**Interfaces:**
- Produces: `AccountPanel(props: PanelProps)` — a stats strip (Equity, Buying power, Day P&L, Realized — mono, P&L in market colors) with the bronze arm/disarm chip at right, and a sortable positions table below (Symbol, Qty, Avg, Last, Unrl P&L, Flatten). Topics: `exec.account` + `exec.positions` + `exec.status` + `md.quote`.
- Consumes: `stores.exec` (`accounts()`, `positions()`, `status()`), `useOrderCommands` (arm/disarm, flatten), `sortColumns` (T16). Connection-link dots are **not** here (they moved to the top bar).

- [ ] **Step 1: Write `AccountPanel.test.tsx` (failing)**

Port the merged behavior from the two old tests: stats hydrate + aggregate across venues (`acct-equity`/`acct-bp`/`acct-daypnl`/`acct-realized`); master arm chip toggles (`arm-toggle`); positions render net+per-venue rows (`pos-net`); Flatten submits an opposite-side MARKET order priced off quote and stays clickable while disarmed (`data-armed=false`); the positions P&L column header sorts. Use the `test/providers` wrapper.

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/panels/AccountPanel.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement `AccountPanel.tsx`** — compose the two old components

Stats strip = `AccountBarPanel`'s 4 stat cells + master arm chip (keep `data-testid`s: `acct-equity`, `acct-bp`, `acct-daypnl`, `acct-realized`, `arm-toggle`); restyle the arm chip to the bronze `.chip`-style (`ARMED` bronze / `DISARMED` muted). Positions table = `PositionsPanel`'s table (keep `pos-net`, per-row `Flatten`, sign colors), now with sortable headers via T16 (default `{ col: "unrealizedPnl", dir: "desc" }`, persisted to `settings.sort`). Header meta shows `"{n} open positions"`. Per-venue arm chips from `AccountBarPanel` may stay in the stats strip (keep `venue-arm-<venue>` testids) — the spec's merged panel keeps the arm control; per-venue chips are additive, not required, keep them for parity.

- [ ] **Step 4: Register `account` + reconcile ids**

In `registry.tsx`: add `"account": { component: AccountPanel, topics: ["exec.account", "exec.positions", "exec.status", "md.quote"], title: "Account", glyph: "Σ", description: "Equity, BP, day P&L, positions, arm", symbolBearing: false }`. For back-compat with any saved workspace doc referencing the old ids, alias `"account-bar"` and `"positions"` to `AccountPanel` too (same component; a doc with a lone `positions` panel now renders the full merged Account — acceptable). Update `CATALOG_ORDER`: replace the `"account-bar", "positions"` entries with a single `"account"`.

Then in `presets.ts`, swap the Trading preset's `t-account` slot `panelId` from `"account-bar"` to `"account"` — a one-line change; the serialized `TRADING_LAYOUT` is unchanged because the dockview panel id (`t-account`) and its slot are the same. Re-run `presets.test.ts` to confirm `PANELS["account"]` now resolves.

- [ ] **Step 5: Delete the old panels**

```bash
cd ui && git rm src/chrome/panels/AccountBarPanel.tsx src/chrome/panels/AccountBarPanel.test.tsx src/chrome/panels/PositionsPanel.tsx src/chrome/panels/PositionsPanel.test.tsx
```

Then `grep -rn "AccountBarPanel\|PositionsPanel" src` and fix stragglers (registry import lines). Note: the `trading` preset (Task 7) already uses ids `t-account` (panelId `account`) and no separate `positions` — confirm.

- [ ] **Step 6: Run affected suites + tsc**

Run: `cd ui && npx vitest run src/chrome/panels/AccountPanel.test.tsx src/chrome/panels/registry.test.tsx && npx tsc --noEmit`
Expected: PASS / clean.

- [ ] **Step 7: Commit**

```bash
cd ui && git add src/chrome/panels/AccountPanel.tsx src/chrome/panels/AccountPanel.test.tsx src/chrome/panels/registry.tsx src/chrome/presets.ts
git commit -m "feat(ui): merge Account + Positions into one Account panel (stats strip + arm chip + sortable positions); connection dots now top-bar only"
```

---

## Task 20: Open Orders — lifecycle chips, sortable, stream-gap restyle

**Files:**
- Modify: `ui/src/chrome/panels/OpenOrdersPanel.tsx`
- Modify: `ui/src/chrome/panels/OpenOrdersPanel.test.tsx`

**Interfaces:**
- Consumes: `displayStatus`/`STATUS_LABEL` (`exec/orderStatus.ts`, unchanged), `sortColumns` (T16), `stores.exec`. No new store.

**Presentation deltas:** lifecycle **chips** using the Task 3 classes — green outline `.chip-working` for WORKING (Submitted/Accepted/Partially filled), bronze `.chip-pending` for PENDING/REPLACING, danger `.chip-rejected` for REJECTED (with the verbatim R-code in the note column); per-row `✕`; **Cancel all**; the bronze stream-gap badge ("stream gap — reconciled, verify") restyled to bronze (currently `warn`-bg — now `.chip-pending`-adjacent bronze). Add a `<thead>` with sortable columns (Symbol, Side, Qty@Px, State) — default sort by created time desc.

- [ ] **Step 1: Update `OpenOrdersPanel.test.tsx` (failing)**

Assert: WORKING orders render a `.chip-working` element (query by class or a `data-chip="working"` attr); REJECTED renders `.chip-rejected` + the reject reason text; the reconcile badge appears when a venue is reconciling and reads bronze (assert `data-testid="reconcile-badge"` still present with the new copy); clicking a column header sorts + persists. Keep the existing cancel / cancel-all assertions.

- [ ] **Step 2: Run to fail**

Run: `cd ui && npx vitest run src/chrome/panels/OpenOrdersPanel.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Implement**

- Map `displayStatus(order, optimistic)` → chip class: WORKING statuses → `chip chip-working`; `PendingNew`/`Replacing` → `chip chip-pending`; `REJECTED`/`BLOCKED` → `chip chip-rejected`; terminal others → plain muted text. Render `<span className={cls} data-chip={variant}>{STATUS_LABEL[ds]}</span>`.
- Add a `<thead className="col-head">` with sortable headers via T16 (`sortIndicator` + `.sort-active`), persisting `settings.sort`; default `{ col: "createdMs", dir: "desc" }` (matches today's `ExecStore` order). Apply `sortRows`.
- Restyle the reconcile badge to bronze (`.chip chip-pending` styling or `background: rgba(154,106,27,.12); color: var(--accent)`), copy "stream gap — reconciled, verify".
- Keep per-row `cancel-<id>` and `cancel-all` behavior and testids.

- [ ] **Step 4: Run to pass**

Run: `cd ui && npx vitest run src/chrome/panels/OpenOrdersPanel.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/panels/OpenOrdersPanel.tsx src/chrome/panels/OpenOrdersPanel.test.tsx
git commit -m "feat(ui): open-orders lifecycle chips (working/pending/rejected), sortable columns, bronze stream-gap badge"
```

---

## Task 21: Order Ticket + ChartControls restyle

**Files:**
- Modify: `ui/src/chrome/panels/OrderTicketPanel.tsx`
- Modify: `ui/src/chrome/panels/ChartControls.tsx`
- Modify: their tests as needed (preserve all `data-testid`s)

**Interfaces:** unchanged (behavior identical; only presentation moves to the shared classes). Kill switch keeps `data-testid="kill"`; venue select keeps `data-testid="venue"`; all order-entry testids preserved.

**Presentation deltas (from `presets.html` ticket mockup):** bid × ask quote line at top (click a side seeds the price field — behavior already exists via `quoteBtn`), BUY/SELL/SHORT/COVER side row (BUY highlighted green when selected), chip-styled (`.ctl`) type/price/qty/sizing/TIF controls, venue selector in the header (`⚙` now opens unified Settings per T11), hotkey preset buttons, and the kill switch as `.kill-switch` (full-width, danger-bordered, always visible). ChartControls: timeframe `▾` + indicators styled with `.ctl`/`.btn`.

- [ ] **Step 1: Restyle `OrderTicketPanel.tsx`** to the shared classes

Replace the shared inline `inp` style object and per-control inline styles with `.ctl`/`.btn`/`.btn-primary`/`.kill-switch` classes and the ledger side buttons (`.side` styling: selected BUY → green outline `border-color: var(--up); color: var(--up); background: rgba(23,122,88,.08)`; SELL/SHORT/COVER neutral until selected). Keep the quote line (bid green / ask red, mono, click-to-seed). Keep the armed banner. Do not change any handler or testid. The `⚙` handler now calls the T11 `onOpenSettings("orders")` context (already wired in T11 — this task just ensures the styling matches).

- [ ] **Step 2: Restyle `ChartControls.tsx`**

Timeframe `<select>` → styled control (`.ctl` wrapper or a styled native select using the `.btn` class); add-indicator select likewise; `InstanceChip` uses `.ctl` for the chip body and keeps color inputs. Preserve `aria-label="timeframe"` / `aria-label="add indicator"` / `aria-label="remove ${id}"`.

- [ ] **Step 3: Run affected suites**

Run: `cd ui && npx vitest run src/chrome/panels/OrderTicketPanel.test.tsx src/chrome/panels/ChartPanel.test.tsx`
Expected: PASS.

- [ ] **Step 4: Manual smoke**

Run: `cd ui && npm run dev`; load the Trading preset; confirm the ticket reads as designed (quote line, side row, chip controls, loud kill switch), the chart timeframe/indicator controls are styled, and submitting a paper order still works.

- [ ] **Step 5: Commit**

```bash
cd ui && git add src/chrome/panels/OrderTicketPanel.tsx src/chrome/panels/ChartControls.tsx
git commit -m "feat(ui): restyle order ticket + chart controls to shared Daylight controls; kill switch as .kill-switch"
```

---

## Task 22: Connection Status panel re-skin

**Files:**
- Modify: `ui/src/chrome/panels/ConnectionStatusPanel.tsx`
- Modify: `ui/src/chrome/panels/ConnectionStatusPanel.test.tsx` (or `src/chrome/ConnectionStatusPanel.test.tsx` — whichever exists)

**Interfaces:** unchanged (`{ health }`); content unchanged (per-link now + min/avg/max, event log). Only styling → shared classes (mono data rows, ledger-consistent header). The existing `dotColor` stays as-is — note its **input** is the `LinkStatus` wire enum `"ok" | "degraded" | "down"`, which it maps to palette fields `ok`/`warn`/`danger` respectively (`status === "ok" ? p.ok : status === "degraded" ? p.warn : p.danger`). Do not compare status against `"warn"`/`"danger"`.

- [ ] **Step 1: Re-skin** — apply `.data-row`/`.mono`/`.col-head` classes; keep `dotColor(status, palette)` mapping `"ok"→ok`, `"degraded"→warn`, `"down"→danger` unchanged. Keep the `min/avg/max` compact string and the last-50 event log. No behavior change.

- [ ] **Step 2: Run its test + full suite**

Run: `cd ui && npx vitest run src/chrome && npm run test`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd ui && git add src/chrome/panels/ConnectionStatusPanel.tsx src/chrome/*ConnectionStatusPanel.test.tsx
git commit -m "feat(ui): re-skin Connection Status panel to shared Daylight classes"
```

---

# Phase 6 — E2E

## Task 23: Playwright E2E updates

**Files:**
- Modify: `ui/e2e/smoke.spec.ts`
- Modify: `ui/e2e/error-matrix.spec.ts`
- Modify: `ui/e2e/serve.sh` only if the workspace bootstrap changed (it serves `ui/dist` + a replay engine; the default workspace is now `main` and starts blank — see step 1)

**Context (from recon):** all seven E2E specs `page.goto("/?workspace=trading")` or `.../monitoring")` and gate hydration on `data-testid="acct-equity"`; `smoke.spec.ts` uses `page.getByLabel("focus green")` (the removed symbol box). The seeds are gone; `?workspace=trading` now loads a **blank** workspace (no auto-seed). So the specs must **apply a preset** (or add panels) to reach a populated layout, use the merged `account` panel's testids, and switch the link-group test from the header box to **type-to-load on a panel**.

- [ ] **Step 1: Update workspace bootstrap in specs**

Two viable approaches — pick the one matching how the app addresses presets:
- (a) Navigate to a fresh workspace then click the Trading preset card in the empty-state catalog (`page.getByRole("button", { name: /Trading/ })`), which applies the layout; or
- (b) If a preset can be requested via URL (only if implemented — it is **not** in this plan), skip. Use (a).

Replace each `page.goto("/?workspace=trading")` with: `await page.goto("/?workspace=e2e-trading"); await page.getByRole("button", { name: /^Trading/ }).click();` (a unique per-spec workspace name keeps runs isolated and blank). For the monitoring case, click the Monitoring preset. Then wait on the hydration gate (`acct-equity`).

- [ ] **Step 2: Repoint account testids**

`acct-equity` still exists (Task 19 preserved it on the merged `account` panel). Confirm the Trading preset includes the `account` panel (it does: `t-account`). No id change needed beyond ensuring the merged panel mounts. If any spec referenced `positions` panel specifically, point it at the `account` panel region.

- [ ] **Step 3: Replace the symbol-box link test with type-to-load**

In `smoke.spec.ts`, replace the `page.getByLabel("focus green")` block with a type-to-load flow: focus a blue-group chart panel (click it to make it the active dockview panel), `await page.keyboard.type("NVDA"); await page.keyboard.press("Enter");` then assert a blue-group DOM/tape panel now shows `US.NVDA` (via the panel header symbol `data-testid="panel-symbol"`). This exercises the new symbol-linking path end-to-end (type-to-load + group follow). Keep the assertion resilient (wait for the symbol text).

- [ ] **Step 4: Add a sort-survives-reload check**

Add a spec: on the Trading (or Monitoring) preset, click a sortable column header (e.g. the scanner `%` or positions `Unrl P&L`), reload the page (`page.reload()`), and assert the sort indicator (`▴`/`▾`) is still on that column — proving `settings.sort` persisted through `WorkspaceStore`.

- [ ] **Step 5: Add a preset-confirm-replace check (optional but specced)**

On a populated workspace, trigger applying a different preset via the **+ Add panel** dropdown's preset cards; assert the inline confirm appears (`window.confirm` — use `page.on("dialog", d => d.accept())`) and the layout replaces. Keep it minimal.

- [ ] **Step 6: Run E2E**

Run: `cd ui && npm run build && npm run e2e`
Expected: PASS (all specs). `serve.sh` builds `dist` + boots the replay engine; the blank-start + preset-apply path must reach the same populated state the old seeds gave. If a spec is flaky on the empty-state→preset transition, add an explicit `await expect(page.getByTestId("latency-readout")).toBeVisible()` before interacting.

- [ ] **Step 7: Commit**

```bash
cd ui && git add e2e
git commit -m "test(ui): update E2E for blank-start + preset apply, merged account, type-to-load link, sort-survives-reload"
```

---

# Final verification

## Task 24: Full-suite green + build + visual sweep

- [ ] **Step 1: Unit + component suite**

Run: `cd ui && npm run test`
Expected: PASS (all files, both pools).

- [ ] **Step 2: Golden suite, both themes**

Run: `cd ui && npm run test:golden`
Expected: PASS (0 differing pixels; light + dark).

- [ ] **Step 3: Type + lint + build**

Run: `cd ui && npx tsc --noEmit && npm run lint && npm run build`
Expected: clean; `dist/` built with fonts.

- [ ] **Step 4: E2E**

Run: `cd ui && npm run e2e`
Expected: PASS.

- [ ] **Step 5: Manual visual sweep against the mockups**

Run: `cd ui && npm run dev`. Verify against `docs` mockups (`.superpowers/brainstorm/34393-1783409886/content/`): warm-paper surfaces + serif ledger headers over double rules; bronze focus ring + armed chip + scanner hits + today's news + working-order edge; green/red only on direction; danger-red kill switch; two-column DOM; full-row tape; merged Account; top-bar latency readout; blank start → catalog → preset; type-to-load on a grouped chart moves DOM/tape; dark theme (Settings → Appearance → Dark) keeps every role. No console errors.

- [ ] **Step 6: Sensitive-sweep + final commit (if any residual changes)**

Per the repo's publish discipline (public repo), scan the diff for anything sensitive before the final push. Commit any leftover fixups.

---

## Self-review — spec coverage check

Verified against `docs/superpowers/specs/2026-07-07-ui-redesign-design.md`:

| Spec section | Task(s) |
|---|---|
| Visual system — palette (light + dark, `borderStrong`) | T2 |
| Type — self-hosted Plex Serif/Sans/Mono, `FONTS.serif` | T1 |
| CSS architecture — palette→`:root` vars, `global.css`, dockview `--dv-*` | T3 |
| Top bar (wordmark, latency, +Add panel, ⧉ New window, ⚙ Settings, arm chip; symbol boxes gone) | T8, T9 |
| Latency readout (eng/moo/tz, threshold-colored, click opens Connection) | T8 |
| Blank start & catalog (presets-first empty state + Add-panel dropdown) | T6, T10 |
| Presets (Monitoring/Trading, rebuilt proportions) + confirm-replace | T7, T10, T23 |
| New window / `?workspace=<name>` / `window-N` / popup-blocked toast | T5, T10 |
| Settings modal (Appearance / Orders & hotkeys / Sounds) | T11 |
| Panel frame — ledger header, group swatch + picker, focus ring | T12 |
| Type-to-load (state machine + all capture rules + error revert) | T13 |
| Sortable tables (all tabular panels; sort persists) | T16, T17, T19, T20 |
| Chart re-ink | T2 (palette auto-re-inks via `setPalette`) |
| Scanner/Movers — filters popover + summary + new-hit tint | T17 |
| News — date+seen-time+source, today fresh edge | T18 |
| DOM Ladder — classic two-column, outward bars, no Cum, order edge | T14 |
| Time & Sales — full-row color, dimmed timestamp, bold ≥10k | T15 |
| Account (merged) + arm chip + sortable positions | T19 |
| Open Orders — chips, R-code, cancel/cancel-all, stream-gap badge | T20 |
| Order Ticket — quote line, side row, chip controls, venue, kill switch | T21 |
| Connection Status — re-skin | T22 |
| Persistence (sort + group + settings + layout debounce-save) | T5, T12, T16–T20 |
| Error handling table (bad symbol, popup blocked, preset-over-nonempty, font fallback, WS save fail) | T13, T10, T1, T5 |
| Testing (unit machine/sort/window/summary/preset; goldens; E2E) | T13, T16, T5, T17, T7, T14, T15, T23 |
| Out of scope (workspace manager, per-ws theming, ladder click-trade, etc.) | not implemented (correct) |

Open items carried from the spec, resolved here: **dark-palette values authored** (T2); **tape block threshold fixed at 10k** (`BLOCK_THRESHOLD`, T15); **Plex woff2 subset + 4 preloads** (T1). Deferred as spec allows: latency-readout compact-mode on narrow windows (not implemented; revisit if needed).

---

## Execution handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-07-ui-redesign-daylight-ledger.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best here given the 24 tasks with clean dependency boundaries.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review.

**Which approach?**

> Note for the executor: this is `ui/`-only work. Per the project's memory, the `ui/` tree has a node-canvas fork-pool quirk that only manifests in **worktree** checkouts — prefer executing in the main checkout, and always add any new canvas-touching test file to `vitest.config.ts` `poolMatchGlobs`. Verify pwd/branch before committing.
