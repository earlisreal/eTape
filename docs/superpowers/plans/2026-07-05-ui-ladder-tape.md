# eTape UI — Plan 3 of 6: L2 Ladder & Time & Sales

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the two remaining Trading-workspace market-data surfaces — the L2 ladder (10 levels per side, price/size/cumulative with depth bars, last-trade flash, display-only working-order marks, non-US "no depth entitlement" state) and the time & sales tape (canvas over `TapeRing`, BUY/SELL/NEUTRAL coloring, minimum-size filter, pause-on-scroll with jump-to-live auto-resume) — plus the golden-image test harness (node-canvas, strict pixel-diff against checked-in PNGs) that all remaining painter work verifies against.

**Architecture:** Both panels are pure canvas painters in the established four-layer shape: a pure state-builder (`buildLadderState` / `buildTapeRows`) turns store contents into a plain paint-state object, a pure painter (`paintLadder` / `paintTape`) draws that state, and a thin chrome panel wires stores → builder → painter through one `Surface` registered with the per-window rAF `Scheduler`. No viewport/window classes anywhere: ladder rows are indexed by book level, tape rows by ring index — layout is `y = rowIndex × rowHeight` (Plan-1 roadmap, item 3). Painters receive the `Palette` and a `nowMs` timestamp in their paint state (never read globals or clocks), which is exactly what makes the golden-image tests deterministic.

**Tech Stack:** Adds devDependencies `canvas` (node-canvas — also upgrades jsdom's `<canvas>` to a real 2D context), `pixelmatch`, `pngjs`, `@types/node`, and a checked-in IBM Plex Mono TTF (OFL) so text renders identically on every machine. No new runtime dependencies.

## Wickplot ports (the complete list — nothing else in wickplot applies to these panels)

Copied from the Plan-1 roadmap, item 3 (`docs/superpowers/plans/2026-07-04-ui-foundation-data-plane.md:33`); source file `~/Projects/wickplot/src/commonMain/kotlin/io/github/earlisreal/wickplot/CandlestickChartMath.kt` and `.../jvmTest/.../CanvasChartSampleRenderTest.kt`:

1. `accumulatePan` → `scrollAccumulate(remainder, deltaPx, rowPx)` — the tape's sub-row wheel-scroll carry, ported **with its table-driven tests** (Task 2).
2. The `volumeToHeight` normalization idiom (`value / max` with a zero-max guard) → `depthFraction` for the ladder's cumulative-size depth bars (Task 5).
3. `axisDecimals` → the shared price-decimals formatter (ladder + tape here; the order ticket reuses it in Plan 5) (Task 1).
4. The golden-image harness *shape* from `CanvasChartSampleRenderTest` — fixture-state generators → render the real painter offscreen at 2× → PNGs to a samples dir for eyeballing — upgraded from wickplot's size-only assertion to **strict pixel-diff against checked-in goldens** (node-canvas) (Task 4).

## Global Constraints

Inherited verbatim from Plan 1 (`docs/superpowers/plans/2026-07-04-ui-foundation-data-plane.md` §Global Constraints) — every task implicitly includes these. Restated here are the ones Plan 3 touches most, plus Plan-3-specific additions.

- **Hard rule:** high-frequency data (book, ticks) never flows through React state. Ladder and tape are canvas surfaces mounted once via refs, painted imperatively, coalesced to one repaint per rAF tick. React renders chrome only (the tape's min-size input and paused pill are low-rate, user-event-driven state — allowed). (stack-decision, ui-design §Architecture)
- **Dependency direction:** `chrome → render → data → wire`, never backwards. `render/ladder/*` and `render/tape/*` may import `data/*` types, `wire/contract` types, and `render/palette.ts`; they must never import from `chrome/*`. (ui-design §Architecture)
- **Honesty policy:** never render stale as live. An empty book is "waiting for depth…", never fabricated zeros; a non-US symbol is an explicit "no depth entitlement" state, never a silently empty ladder; a paused tape is visibly paused (warn strip + pill) and a reconnect that rebuilds the ring **drops the pause and returns to live** rather than silently rendering a stale anchor. A quiet symbol is not a stale symbol. (ui-design §Error handling)
- **Per-consumer dirty tracking:** shared `PaintStore`s (`BookStore`, `TapeRing`) are observed via `getRev()` cursors held per surface — never `consumeDirty()`, which steals the signal from other panels sharing the store (commit `db79c39` lesson; `ChartPanel` precedent).
- **Palette:** all colors come from Plan 2's `ui/src/render/palette.ts` — painters receive the `Palette` in their paint state, never read a global. New tokens this plan adds (`neutral`, `depthBid`, `depthAsk`, `flashBuy`, `flashSell`, `flashNeutral`, `orderMark`) are defined in **both** light and dark variants, and every golden fixture state renders in both. (Plan-1 roadmap item 3, Plan 2 §Task 1)
- **Golden fixture states (mandated by the roadmap):** full book, empty book, flash mid-decay, min-size-filtered tape, "no depth entitlement" — each rendered in both light (default) and dark palette variants.
- **US-only depth:** LV3 full order book is a US entitlement (CLAUDE.md scope; HK verified LV1 = 1-level). `entitledForDepth(symbol)` is `symbol.startsWith("US.")`; everything else renders the no-depth state.
- **Wire format:** snapshot-then-delta per topic; `md.book` payload is a full 10-level replace (`Book`), `md.tape` payload is a `Tick[]` batch. Plan 3 adds **no contract change**. Book sides arrive best-first (`bids[0]` = best bid, descending; `asks[0]` = best ask, ascending) — OpenD's order-book ordering, which the fixtures mirror. (wire/contract.ts, go-engine-design §uihub)
- **Working-order marks are display-only** (v1 has no ladder click-trading). `ExecStore` rows stay untyped until Plan 5 (see `ExecStore.ts:10-11`), so the ladder projects them through a **tolerant mapper** (`workingOrderMarks`) that renders only rows with this symbol, a positive price/qty, and a recognizably working status — Plan 5 replaces the input with typed orders and this mapper's status set with the real 9-state lifecycle.

---

## File Structure (Plan 3)

```
ui/
  package.json                          MODIFY — devDeps canvas/pixelmatch/pngjs/@types/node; test:golden scripts
  .gitignore                            MODIFY — ignore test/golden/__output__/
  src/
    render/
      palette.ts                        MODIFY — + neutral/depthBid/depthAsk/flashBuy/flashSell/flashNeutral/orderMark
      palette.test.ts                   MODIFY — assert new tokens in both variants
      format.ts                         NEW    — axisDecimals port + priceDecimals/formatPrice/formatSize/formatTapeTime
      format.test.ts                    NEW
      scroll.ts                         NEW    — scrollAccumulate port (wheel sub-row carry)
      scroll.test.ts                    NEW
      canvas.ts                         NEW    — applyCanvasSize DPR helper (none existed; SmokePainterPanel predates it)
      canvas.test.ts                    NEW
      ladder/
        ladderState.ts                  NEW    — pure: LadderPaintState builder, depthFraction, order marks, flash math
        ladderState.test.ts             NEW
        paintLadder.ts                  NEW    — pure painter paint(ctx, state)
      tape/
        tapeState.ts                    NEW    — pure: TapeSource view window, filter, pause anchor math
        tapeState.test.ts               NEW
        paintTape.ts                    NEW    — pure painter paint(ctx, state)
    data/
      TapeRing.ts                       MODIFY — + lastSeq/oldestSeq/generation/tickBySeq (pause anchoring)
      TapeRing.test.ts                  MODIFY — + seq/generation/tickBySeq tests
    chrome/panels/
      LadderPanel.tsx                   NEW    — canvas mount + Surface + link-following + flash tracking
      LadderPanel.test.tsx              NEW
      TapePanel.tsx                     NEW    — canvas mount + Surface + wheel pause + min-size input + live pill
      TapePanel.test.tsx                NEW
      registry.tsx                      MODIFY — register "ladder" + "tape"
    seeds/workspaces.ts                 MODIFY — t-ladder/t-tape seed settings (symbol, minSize)
  fixtures/
    ladder-tape.json                    NEW    — md.book + md.tape + exec.orders replay for the dev app
  mock-engine/
    run.ts                              MODIFY — document the new fixture in the selection comment
  test/golden/
    harness.ts                          NEW    — renderScene + expectGolden (node-canvas + pixelmatch)
    harness.golden.test.ts              NEW    — harness smoke golden
    ladder.golden.test.ts               NEW    — 8 ladder goldens
    tape.golden.test.ts                 NEW    — 6 tape goldens
    fonts/IBMPlexMono-Regular.ttf       NEW    — checked in (OFL) for deterministic text metrics
    fonts/OFL.txt                       NEW    — the font's license
    goldens/*.png                       NEW    — checked-in golden images (written by UPDATE_GOLDENS=1)
    __output__/                         (gitignored) — every run's renders + diffs, for eyeballing
```

**Design note — pause anchoring by sequence number:** the tape freezes on the *ticks the user is looking at*, not on a distance-from-newest, so scrolled-back rows must stay put while new ticks stream in. `TapeRing` therefore gains a monotonic per-generation sequence (`lastSeq()` = total ticks appended; tick *k* has seq *k*) and a `generation()` counter bumped on every snapshot rebuild (reconnect re-sync). A paused view is `{anchorSeq, generation}`; a generation mismatch invalidates the anchor (the old ticks are gone — resuming live is the honest behavior), and an anchor that falls off the ring tail clamps to the oldest retained tick. Scrolling steps through *filter-matching* ticks so one wheel row always moves one on-screen row regardless of filter density.

**Design note — flash decay without impure painters:** the last-trade flash is `{price, direction, atMs}` in the paint state plus `nowMs`; `flashAlpha` derives the decay (400 ms linear). The panel keeps the surface dirty while a flash is active so the scheduler animates it; the golden "flash mid-decay" fixture simply sets `nowMs - atMs = 200`.

**Design note — golden determinism:** node-canvas resolves `"IBM Plex Mono"` (the first family in `FONTS.mono`) against fonts registered via `registerFont`, so the harness registers the checked-in TTF before any canvas exists — same glyph metrics on every machine, strict `pixelmatch` diff with zero tolerated pixels. Every run also writes its renders (and diffs on failure) to `test/golden/__output__/` for eyeballing, keeping wickplot's samples-dir habit. Caveat: glyph *metrics* are machine-independent but cairo/freetype *rasterization* (antialiasing) can differ across OS/arch — goldens are canonical for the machine that generated them (Earl's mac today; the repo has no CI). If CI ever runs these, regenerate the goldens on the CI platform or grant a small differing-pixel budget.

---

## Task 1: Shared price/size/time formatting (`axisDecimals` port)

**Files:**
- Create: `ui/src/render/format.ts`
- Test: `ui/src/render/format.test.ts`

**Interfaces:**
- Consumes: nothing (pure, zero imports).
- Produces: `axisDecimals(step: number): number`, `priceDecimals(prices: number[]): number`, `formatPrice(price: number, decimals: number): string`, `formatSize(size: number): string`, `formatTapeTime(ts: string): string` — used by Tasks 5–8; Plan 5's order ticket reuses `priceDecimals`/`formatPrice`.

- [ ] **Step 1: Write the failing tests**

```ts
// ui/src/render/format.test.ts
import { describe, it, expect } from "vitest";
import { axisDecimals, priceDecimals, formatPrice, formatSize, formatTapeTime } from "./format";

describe("axisDecimals (wickplot CandlestickChartMath port)", () => {
  it.each([
    [0.05, 2],
    [0.5, 1],
    [2.5, 1],
    [1, 0],
    [10, 0],
    [0.0001, 4],
    [0.25, 2],
  ])("%f needs %i fractional digits", (step, want) => {
    expect(axisDecimals(step)).toBe(want);
  });
});

describe("priceDecimals", () => {
  it("floors at 2 for whole-dollar US equity prices", () => {
    expect(priceDecimals([187, 190.5])).toBe(2);
  });
  it("expands to what sub-penny prices need", () => {
    expect(priceDecimals([0.1234, 3.5])).toBe(4);
  });
  it("caps at 4", () => {
    expect(priceDecimals([0.00001])).toBe(4);
  });
  it("defaults to 2 on empty input", () => {
    expect(priceDecimals([])).toBe(2);
  });
});

describe("formatPrice / formatSize", () => {
  it("prints a uniform decimal column", () => {
    expect(formatPrice(3.5, 2)).toBe("3.50");
  });
  it("absorbs float dust from level arithmetic", () => {
    expect(formatPrice(3.49 - 9 * 0.01, 2)).toBe("3.40"); // 3.3999999999999995
  });
  it("groups thousands", () => {
    expect(formatSize(12345)).toBe("12,345");
  });
});

describe("formatTapeTime", () => {
  it("renders the exchange timestamp as ET wall clock", () => {
    expect(formatTapeTime("2026-07-06T13:30:05Z")).toBe("09:30:05"); // EDT = UTC-4
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/render/format.test.ts`
Expected: FAIL — `Cannot find module './format'`

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/format.ts
// Shared numeric formatting for the canvas data surfaces (ladder + tape here;
// the Plan-5 order ticket reuses priceDecimals/formatPrice). axisDecimals is a
// direct port of wickplot's CandlestickChartMath.axisDecimals.

/** Fractional digits needed to print step exactly: 0.05 → 2, 0.5 → 1, 2.5 → 1, 1 → 0, 10 → 0. */
export function axisDecimals(step: number): number {
  let d = 0;
  let s = step;
  while (d < 8 && Math.abs(s - Math.round(s)) > 1e-9) {
    s *= 10;
    d++;
  }
  return d;
}

/**
 * Uniform decimal count for a column of prices (a book's levels, a tape
 * window): enough digits that every price prints exactly, floored at the US
 * equity convention of 2 and capped at the sub-penny tick limit of 4.
 */
export function priceDecimals(prices: number[]): number {
  let d = 2;
  for (const p of prices) d = Math.max(d, axisDecimals(p));
  return Math.min(d, 4);
}

export function formatPrice(price: number, decimals: number): string {
  return price.toFixed(decimals);
}

/** Integer share sizes with thousands separators: 12345 → "12,345". */
export function formatSize(size: number): string {
  return Math.round(size).toLocaleString("en-US");
}

/** Exchange timestamp (ISO string) → ET wall-clock HH:MM:SS for tape rows. */
export function formatTapeTime(ts: string): string {
  return new Date(ts).toLocaleTimeString("en-US", { hour12: false, timeZone: "America/New_York" });
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/render/format.test.ts`
Expected: PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/format.ts ui/src/render/format.test.ts
git commit -m "feat(ui/render): shared price/size/time formatting (wickplot axisDecimals port)"
```

---

## Task 2: Wheel-scroll carry (`accumulatePan` port)

**Files:**
- Create: `ui/src/render/scroll.ts`
- Test: `ui/src/render/scroll.test.ts`

**Interfaces:**
- Consumes: nothing (pure).
- Produces: `interface ScrollDelta { rows: number; remainder: number }`, `scrollAccumulate(remainder: number, deltaPx: number, rowPx: number): ScrollDelta` — consumed by `TapePanel` (Task 10).

- [ ] **Step 1: Write the failing tests** (ported from wickplot's `CandlestickChartMathTest` pan-accumulation table)

```ts
// ui/src/render/scroll.test.ts
import { describe, it, expect } from "vitest";
import { scrollAccumulate } from "./scroll";

describe("scrollAccumulate (wickplot accumulatePan port)", () => {
  it("slow scroll accumulates sub-row movement across events until a whole row is crossed", () => {
    // Row = 8px; four slow wheel events of 2px each = one full row, not four discarded rounds.
    let acc = scrollAccumulate(0, 2, 8);
    expect(acc.rows).toBe(0);
    acc = scrollAccumulate(acc.remainder, 2, 8);
    expect(acc.rows).toBe(0);
    acc = scrollAccumulate(acc.remainder, 2, 8);
    expect(acc.rows).toBe(0);
    acc = scrollAccumulate(acc.remainder, 2, 8);
    expect(acc.rows).toBe(1);
    expect(acc.remainder).toBeCloseTo(0, 6);
  });

  it("fast scroll emits multiple rows and carries the sub-row residue", () => {
    const acc = scrollAccumulate(0, -20, 8);
    expect(acc.rows).toBe(-2); // truncation toward zero, like the Kotlin original
    expect(acc.remainder).toBeCloseTo(-0.5, 6);
  });

  it("is safe when the row height is not positive", () => {
    const acc = scrollAccumulate(0.4, 10, 0);
    expect(acc.rows).toBe(0);
    expect(acc.remainder).toBeCloseTo(0.4, 6);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/render/scroll.test.ts`
Expected: FAIL — `Cannot find module './scroll'`

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/scroll.ts

/** Result of scrollAccumulate: whole rows to scroll now plus the sub-row remainder to carry forward. */
export interface ScrollDelta {
  rows: number;
  remainder: number;
}

/**
 * Port of wickplot's CandlestickChartMath.accumulatePan, re-signed for row
 * scrolling. Wheel events arrive a few pixels at a time; rounding each event
 * to rows independently discards movement smaller than a row, which makes slow
 * scrolls do nothing. Feed the previous remainder back in on every event so
 * slow movement accumulates. Positive deltaPx yields positive rows — direction
 * semantics belong to the caller (the original's drag-inversion is dropped).
 */
export function scrollAccumulate(remainder: number, deltaPx: number, rowPx: number): ScrollDelta {
  if (rowPx <= 0) return { rows: 0, remainder };
  const total = remainder + deltaPx / rowPx;
  const rows = Math.trunc(total); // truncate toward zero; the fraction is carried forward
  return { rows, remainder: total - rows };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/render/scroll.test.ts`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/scroll.ts ui/src/render/scroll.test.ts
git commit -m "feat(ui/render): scrollAccumulate wheel carry (wickplot accumulatePan port)"
```

---

## Task 3: Ladder/tape palette tokens + DPR canvas-sizing helper

**Files:**
- Modify: `ui/src/render/palette.ts` (interface at lines 6–46, `LIGHT` at 48–81, `DARK` at 83–116)
- Modify: `ui/src/render/palette.test.ts`
- Create: `ui/src/render/canvas.ts`
- Test: `ui/src/render/canvas.test.ts`

**Interfaces:**
- Consumes: the existing `Palette` interface and `LIGHT`/`DARK` objects.
- Produces: seven new `Palette` tokens (`neutral`, `depthBid`, `depthAsk`, `flashBuy`, `flashSell`, `flashNeutral`, `orderMark`) and `applyCanvasSize(canvas: HTMLCanvasElement, ctx: CanvasRenderingContext2D, cssWidth: number, cssHeight: number, dpr: number): boolean` — consumed by Tasks 5–10.

- [ ] **Step 1: Write the failing palette test** (append to the existing `ui/src/render/palette.test.ts`)

```ts
it("defines the ladder/tape tokens (Plan 3) in both variants", () => {
  for (const p of [LIGHT, DARK]) {
    for (const k of ["neutral", "depthBid", "depthAsk", "flashBuy", "flashSell", "flashNeutral", "orderMark"] as const) {
      expect(p[k]).toBeTruthy();
    }
    // depth bars and flashes are translucent fills layered under text — must be rgba()
    for (const k of ["depthBid", "depthAsk", "flashBuy", "flashSell", "flashNeutral"] as const) {
      expect(p[k].startsWith("rgba(")).toBe(true);
    }
  }
});
```

(Match the existing file's import style — it already imports `LIGHT`/`DARK`.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/render/palette.test.ts`
Expected: FAIL — TS error / undefined `neutral` on `Palette`

- [ ] **Step 3: Add the tokens to `palette.ts`**

Append to the `Palette` interface (after the `fillOutline` group, before `// ET session shading`):

```ts
  // ladder / tape (Plan 3)
  neutral: string;      // NEUTRAL tick prints + last-trade text with no direction
  depthBid: string;     // ladder cumulative depth bar fill, bid side (rgba, low alpha)
  depthAsk: string;     // ladder cumulative depth bar fill, ask side (rgba, low alpha)
  flashBuy: string;     // last-trade flash row fill at full strength (painter decays via globalAlpha)
  flashSell: string;
  flashNeutral: string;
  orderMark: string;    // display-only working-order marks on the ladder
```

Add to `LIGHT` (after `fillOutline`, hues derived from the existing `up` #17A67C / `down` #E0526E):

```ts
  neutral: "#5F6B78",
  depthBid: "rgba(23,166,124,0.14)",
  depthAsk: "rgba(224,82,110,0.12)",
  flashBuy: "rgba(23,166,124,0.30)",
  flashSell: "rgba(224,82,110,0.28)",
  flashNeutral: "rgba(90,102,114,0.22)",
  orderMark: "#C0872E",
```

Add to `DARK` (hues from `up` #2BB894 / `down` #F0647E):

```ts
  neutral: "#8B98A5",
  depthBid: "rgba(43,184,148,0.16)",
  depthAsk: "rgba(240,100,126,0.14)",
  flashBuy: "rgba(43,184,148,0.32)",
  flashSell: "rgba(240,100,126,0.30)",
  flashNeutral: "rgba(122,135,148,0.25)",
  orderMark: "#E0A64B",
```

- [ ] **Step 4: Write the failing canvas-helper test** (fake objects — no DOM, runs in the default node environment)

```ts
// ui/src/render/canvas.test.ts
import { describe, it, expect } from "vitest";
import { applyCanvasSize } from "./canvas";

function fakes() {
  const canvas = { width: 0, height: 0 } as HTMLCanvasElement;
  const transforms: number[][] = [];
  const ctx = {
    setTransform: (...args: number[]) => {
      transforms.push(args);
    },
  } as unknown as CanvasRenderingContext2D;
  return { canvas, ctx, transforms };
}

describe("applyCanvasSize", () => {
  it("sizes the backing store by dpr and normalizes the transform to CSS pixels", () => {
    const { canvas, ctx, transforms } = fakes();
    expect(applyCanvasSize(canvas, ctx, 300, 200, 2)).toBe(true);
    expect(canvas.width).toBe(600);
    expect(canvas.height).toBe(400);
    expect(transforms.at(-1)).toEqual([2, 0, 0, 2, 0, 0]);
  });

  it("leaves the backing store alone when the size is unchanged (no canvas clear)", () => {
    const { canvas, ctx } = fakes();
    applyCanvasSize(canvas, ctx, 300, 200, 2);
    const w = canvas.width;
    applyCanvasSize(canvas, ctx, 300, 200, 2);
    expect(canvas.width).toBe(w);
  });

  it("declines zero/negative sizes (panel still measuring)", () => {
    const { canvas, ctx } = fakes();
    expect(applyCanvasSize(canvas, ctx, 0, 200, 2)).toBe(false);
    expect(canvas.width).toBe(0);
  });
});
```

- [ ] **Step 5: Write the helper**

```ts
// ui/src/render/canvas.ts

/**
 * Size a canvas's backing store for the device pixel ratio and normalize the
 * context transform so painters draw in CSS pixels. Assigning width/height
 * clears the canvas, so only touch them when they actually change. Returns
 * false when the CSS size is not yet usable (panel still measuring).
 */
export function applyCanvasSize(
  canvas: HTMLCanvasElement,
  ctx: CanvasRenderingContext2D,
  cssWidth: number,
  cssHeight: number,
  dpr: number,
): boolean {
  if (cssWidth <= 0 || cssHeight <= 0) return false;
  const w = Math.round(cssWidth * dpr);
  const h = Math.round(cssHeight * dpr);
  if (canvas.width !== w) canvas.width = w;
  if (canvas.height !== h) canvas.height = h;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  return true;
}
```

- [ ] **Step 6: Run the render tests**

Run: `cd ui && npx vitest run src/render`
Expected: PASS (palette + canvas + Tasks 1–2 + existing Plan-2 render tests)

- [ ] **Step 7: Typecheck** (the `Palette` change must not break Plan-2 consumers)

Run: `cd ui && npm run typecheck`
Expected: clean

- [ ] **Step 8: Commit**

```bash
git add ui/src/render/palette.ts ui/src/render/palette.test.ts ui/src/render/canvas.ts ui/src/render/canvas.test.ts
git commit -m "feat(ui/render): ladder/tape palette tokens (both variants) + DPR canvas sizing helper"
```

---

## Task 4: Golden-image harness (node-canvas + strict pixel-diff)

**Files:**
- Modify: `ui/package.json` (devDependencies + scripts)
- Modify: `ui/.gitignore`
- Create: `ui/test/golden/harness.ts`
- Create: `ui/test/golden/fonts/IBMPlexMono-Regular.ttf` + `ui/test/golden/fonts/OFL.txt` (checked in)
- Test: `ui/test/golden/harness.golden.test.ts`

**Interfaces:**
- Consumes: `getPalette`, `FONTS` from `ui/src/render/palette.ts`.
- Produces: `renderScene(width: number, height: number, paint: (ctx: CanvasRenderingContext2D) => void): PNG` and `expectGolden(name: string, png: PNG): void` — consumed by Tasks 6 and 8. Goldens live in `ui/test/golden/goldens/<name>.png`; every run writes `ui/test/golden/__output__/<name>.png` (+ `<name>.diff.png` on mismatch).

- [ ] **Step 1: Install devDependencies**

Run: `cd ui && npm install -D canvas@^2.11.2 pixelmatch@^5.3.0 pngjs@^7.0.0 @types/node @types/pixelmatch @types/pngjs`
Expected: clean install (canvas ships darwin-arm64 prebuilds). If the prebuild is missing for the local Node version and a source build starts and fails, run `brew install pkg-config cairo pango libpng jpeg giflib librsvg` and retry.

Note: `canvas@^2.11.2` (not v3) is deliberate — it matches jsdom 24's optional peer range, so jsdom's `<canvas>.getContext("2d")` starts returning a real context, which the Task 9/10 component tests rely on.

- [ ] **Step 2: Run the existing suite** (installing `canvas` upgrades jsdom canvas behavior — catch any surprise now, before new code lands)

Run: `cd ui && npm test`
Expected: PASS. If a pre-existing test breaks because `getContext` now returns a real context instead of null, fix that test in this task and note it in the commit.

- [ ] **Step 3: Check in the font** (OFL-licensed; deterministic glyph metrics for goldens)

```bash
mkdir -p ui/test/golden/fonts
curl -sL "https://github.com/google/fonts/raw/main/ofl/ibmplexmono/IBMPlexMono-Regular.ttf" \
  -o ui/test/golden/fonts/IBMPlexMono-Regular.ttf
curl -sL "https://github.com/google/fonts/raw/main/ofl/ibmplexmono/OFL.txt" \
  -o ui/test/golden/fonts/OFL.txt
file ui/test/golden/fonts/IBMPlexMono-Regular.ttf   # must say "TrueType Font data"
```

(Verified 2026-07-05: this URL serves a valid 135 KB TrueType file.)

- [ ] **Step 4: Add scripts and gitignore entries**

In `ui/package.json` scripts:

```json
"test:golden": "vitest run test/golden",
"test:golden:update": "UPDATE_GOLDENS=1 vitest run test/golden"
```

In `ui/.gitignore` append:

```
test/golden/__output__/
```

- [ ] **Step 5: Write the harness**

```ts
// ui/test/golden/harness.ts
// Golden-image harness — the shape of wickplot's CanvasChartSampleRenderTest
// (fixture states → render the real painter offscreen at 2× → PNGs to a
// samples dir for eyeballing), upgraded from its size-only assertion to a
// strict pixel-diff against checked-in goldens.
import { createCanvas, registerFont } from "canvas";
import { PNG } from "pngjs";
import pixelmatch from "pixelmatch";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const GOLDEN_DIR = join(here, "goldens");
const OUTPUT_DIR = join(here, "__output__");
const SCALE = 2; // render at 2× like wickplot's harness; goldens are HiDPI

// Register the app's real mono face before any canvas exists so text metrics
// are deterministic on every machine — node-canvas resolves the quoted
// "IBM Plex Mono" in FONTS.mono against this registration.
registerFont(join(here, "fonts", "IBMPlexMono-Regular.ttf"), { family: "IBM Plex Mono" });

/** Render a painter offscreen at 2× in CSS-pixel coordinates and decode to PNG. */
export function renderScene(
  width: number,
  height: number,
  paint: (ctx: CanvasRenderingContext2D) => void,
): PNG {
  const canvas = createCanvas(width * SCALE, height * SCALE);
  const ctx = canvas.getContext("2d") as unknown as CanvasRenderingContext2D;
  ctx.scale(SCALE, SCALE);
  paint(ctx);
  return PNG.sync.read(canvas.toBuffer("image/png"));
}

/**
 * Strict pixel-diff against the checked-in golden. Every run writes the
 * current render to __output__/ for eyeballing; failures also write a diff
 * image. UPDATE_GOLDENS=1 (npm run test:golden:update) rewrites the golden
 * instead of asserting — review __output__/ before committing the result.
 */
export function expectGolden(name: string, png: PNG): void {
  mkdirSync(OUTPUT_DIR, { recursive: true });
  const rendered = PNG.sync.write(png);
  writeFileSync(join(OUTPUT_DIR, `${name}.png`), rendered);

  const goldenPath = join(GOLDEN_DIR, `${name}.png`);
  if (process.env.UPDATE_GOLDENS === "1") {
    mkdirSync(GOLDEN_DIR, { recursive: true });
    writeFileSync(goldenPath, rendered);
    return;
  }
  if (!existsSync(goldenPath)) {
    throw new Error(
      `golden missing: ${name}.png — run "npm run test:golden:update", eyeball __output__/${name}.png, commit goldens/${name}.png`,
    );
  }
  const golden = PNG.sync.read(readFileSync(goldenPath));
  if (golden.width !== png.width || golden.height !== png.height) {
    throw new Error(
      `golden size mismatch for ${name}: golden ${golden.width}×${golden.height}, rendered ${png.width}×${png.height}`,
    );
  }
  const diff = new PNG({ width: png.width, height: png.height });
  const differing = pixelmatch(golden.data, png.data, diff.data, png.width, png.height, { threshold: 0.05 });
  if (differing > 0) {
    writeFileSync(join(OUTPUT_DIR, `${name}.diff.png`), PNG.sync.write(diff));
    throw new Error(`golden mismatch for ${name}: ${differing} differing pixels — see __output__/${name}.diff.png`);
  }
}
```

- [ ] **Step 6: Write the harness smoke test**

```ts
// ui/test/golden/harness.golden.test.ts
import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette, FONTS } from "../../src/render/palette";

describe("golden harness", () => {
  it("renders text + fills deterministically with the registered font", () => {
    const p = getPalette("light");
    const png = renderScene(200, 80, (ctx) => {
      ctx.fillStyle = p.bg;
      ctx.fillRect(0, 0, 200, 80);
      ctx.fillStyle = p.up;
      ctx.fillRect(10, 10, 40, 16);
      ctx.fillStyle = p.text;
      ctx.font = `12px ${FONTS.mono}`;
      ctx.textBaseline = "middle";
      ctx.fillText("eTape 3.50 × 1,428", 10, 52);
    });
    expectGolden("harness-smoke", png);
  });
});
```

- [ ] **Step 7: Generate the golden, eyeball it, then verify the assertion path**

```bash
cd ui && npm run test:golden:update   # writes goldens/harness-smoke.png
open test/golden/__output__/harness-smoke.png   # eyeball: light bg, green rect, crisp mono text
npm run test:golden                    # PASS — strict diff against the fresh golden
npm run typecheck                      # harness lives under tsconfig's "test" include — must be clean
```

- [ ] **Step 8: Commit** (goldens are product artifacts — always committed)

```bash
git add ui/package.json ui/package-lock.json ui/.gitignore ui/test/golden
git commit -m "feat(ui/test): golden-image harness — node-canvas render at 2x, strict pixelmatch diff, checked-in IBM Plex Mono"
```

---

## Task 5: Ladder paint-state builder (pure)

**Files:**
- Create: `ui/src/render/ladder/ladderState.ts`
- Test: `ui/src/render/ladder/ladderState.test.ts`

**Interfaces:**
- Consumes: `Book`, `BookLevel`, `TickDirection` from `ui/src/wire/contract.ts`; `Palette` from `../palette`; `priceDecimals` from `../format`.
- Produces (consumed by Tasks 6 and 9):
  - `const LADDER_LEVELS = 10`, `const FLASH_MS = 400`
  - `interface LadderRow { price: number; size: number; cum: number; cumFraction: number }`
  - `interface OrderMark { price: number; side: "buy" | "sell"; qty: number }`
  - `interface TradeFlash { price: number; direction: TickDirection; atMs: number }`
  - `interface LastTrade { price: number; direction: TickDirection }`
  - `interface LadderPaintState { symbol: string; entitled: boolean; asks: LadderRow[]; bids: LadderRow[]; decimals: number; spread: number | null; last: LastTrade | null; flash: TradeFlash | null; orders: OrderMark[]; nowMs: number; width: number; height: number; palette: Palette }`
  - `depthFraction(value: number, max: number): number`
  - `entitledForDepth(symbol: string): boolean`
  - `buildLadderSides(book: Book | undefined): { asks: LadderRow[]; bids: LadderRow[] }`
  - `workingOrderMarks(orders: unknown[], symbol: string): OrderMark[]`
  - `flashAlpha(flash: TradeFlash | null, nowMs: number): number`
  - `buildLadderState(args: { symbol: string; book: Book | undefined; orders: unknown[]; flash: TradeFlash | null; last: LastTrade | null; nowMs: number; width: number; height: number; palette: Palette }): LadderPaintState`

- [ ] **Step 1: Write the failing tests**

```ts
// ui/src/render/ladder/ladderState.test.ts
import { describe, it, expect } from "vitest";
import type { Book } from "../../wire/contract";
import { getPalette } from "../palette";
import {
  buildLadderSides, buildLadderState, depthFraction, entitledForDepth,
  flashAlpha, workingOrderMarks, FLASH_MS, LADDER_LEVELS,
} from "./ladderState";

function book(overrides: Partial<Book> = {}): Book {
  return {
    symbol: "US.AAPL",
    bids: [
      { price: 3.49, size: 300 },
      { price: 3.48, size: 500 },
      { price: 3.47, size: 200 },
    ],
    asks: [
      { price: 3.51, size: 400 },
      { price: 3.52, size: 100 },
    ],
    ts: "2026-07-06T13:35:00Z",
    ...overrides,
  };
}

describe("depthFraction (wickplot volumeToHeight idiom)", () => {
  it("normalizes against the max", () => {
    expect(depthFraction(500, 1000)).toBe(0.5);
  });
  it("guards the zero max", () => {
    expect(depthFraction(500, 0)).toBe(0);
  });
});

describe("entitledForDepth", () => {
  it("US symbols have LV3 depth", () => {
    expect(entitledForDepth("US.AAPL")).toBe(true);
  });
  it("everything else does not", () => {
    expect(entitledForDepth("HK.00700")).toBe(false);
  });
});

describe("buildLadderSides", () => {
  it("accumulates cumulative size per side and normalizes fractions across BOTH sides", () => {
    const { asks, bids } = buildLadderSides(book());
    expect(bids.map((r) => r.cum)).toEqual([300, 800, 1000]); // running sums
    expect(asks.map((r) => r.cum)).toEqual([400, 500]);
    // max cum across both sides is 1000 (bid side) — every fraction is /1000
    expect(bids[2].cumFraction).toBe(1);
    expect(asks[1].cumFraction).toBe(0.5);
  });
  it("caps at LADDER_LEVELS per side", () => {
    const levels = Array.from({ length: 15 }, (_, i) => ({ price: 3.49 - i * 0.01, size: 100 }));
    const { bids } = buildLadderSides(book({ bids: levels }));
    expect(bids).toHaveLength(LADDER_LEVELS);
  });
  it("returns empty sides for no book (never fabricated zeros)", () => {
    const { asks, bids } = buildLadderSides(undefined);
    expect(asks).toEqual([]);
    expect(bids).toEqual([]);
  });
});

describe("workingOrderMarks (tolerant until Plan 5 types exec)", () => {
  const orders = [
    { symbol: "US.AAPL", price: 3.47, side: "Buy", qty: 100, status: "New" },
    { symbol: "US.AAPL", price: 3.53, side: "Short", leavesQty: 50, qty: 80, status: "PartiallyFilled" },
    { symbol: "US.AAPL", price: 3.4, side: "Buy", qty: 10, status: "Filled" },   // terminal — hidden
    { symbol: "US.NVDA", price: 9.0, side: "Buy", qty: 10, status: "New" },      // other symbol — hidden
    { symbol: "US.AAPL", side: "Buy", qty: 10, status: "New" },                  // no price (market) — hidden
    "garbage",                                                                    // malformed — hidden
  ];
  it("keeps working orders for this symbol, prefers leavesQty, maps Short to sell", () => {
    expect(workingOrderMarks(orders, "US.AAPL")).toEqual([
      { price: 3.47, side: "buy", qty: 100 },
      { price: 3.53, side: "sell", qty: 50 },
    ]);
  });
});

describe("flashAlpha", () => {
  it("decays linearly from 1 to 0 over FLASH_MS", () => {
    const flash = { price: 3.51, direction: "BUY" as const, atMs: 1000 };
    expect(flashAlpha(flash, 1000)).toBe(1);
    expect(flashAlpha(flash, 1000 + FLASH_MS / 2)).toBeCloseTo(0.5, 6);
    expect(flashAlpha(flash, 1000 + FLASH_MS)).toBe(0);
    expect(flashAlpha(null, 1000)).toBe(0);
    expect(flashAlpha(flash, 999)).toBe(0); // clock skew guard
  });
});

describe("buildLadderState", () => {
  const palette = getPalette("light");
  const base = { symbol: "US.AAPL", book: book(), orders: [], flash: null, last: null, nowMs: 0, width: 300, height: 486, palette };
  it("derives spread and a uniform decimal count from all visible prices", () => {
    const s = buildLadderState(base);
    expect(s.spread).toBeCloseTo(0.02, 9);
    expect(s.decimals).toBe(2);
  });
  it("has null spread when a side is empty", () => {
    const s = buildLadderState({ ...base, book: book({ asks: [] }) });
    expect(s.spread).toBeNull();
  });
  it("drops the book entirely for non-entitled symbols", () => {
    const s = buildLadderState({ ...base, symbol: "HK.00700" });
    expect(s.entitled).toBe(false);
    expect(s.asks).toEqual([]);
    expect(s.bids).toEqual([]);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npx vitest run src/render/ladder`
Expected: FAIL — `Cannot find module './ladderState'`

- [ ] **Step 3: Write the implementation**

```ts
// ui/src/render/ladder/ladderState.ts
// Pure paint-state math for the L2 ladder. No DOM, no clocks — nowMs and the
// palette arrive in the state so painting is deterministic (goldens).
import type { Book, BookLevel, TickDirection } from "../../wire/contract";
import type { Palette } from "../palette";
import { priceDecimals } from "../format";

export const LADDER_LEVELS = 10;
export const FLASH_MS = 400;

export interface LadderRow {
  price: number;
  size: number;
  cum: number;
  cumFraction: number;
}

export interface OrderMark {
  price: number;
  side: "buy" | "sell";
  qty: number;
}

export interface TradeFlash {
  price: number;
  direction: TickDirection;
  atMs: number;
}

export interface LastTrade {
  price: number;
  direction: TickDirection;
}

export interface LadderPaintState {
  symbol: string;
  entitled: boolean;
  /** Best-first: asks[0] = best ask, bids[0] = best bid. Empty when no book yet. */
  asks: LadderRow[];
  bids: LadderRow[];
  decimals: number;
  spread: number | null;
  last: LastTrade | null;
  flash: TradeFlash | null;
  orders: OrderMark[];
  nowMs: number;
  width: number;
  height: number;
  palette: Palette;
}

/** The volumeToHeight normalization idiom from wickplot's ChartViewport: value/max with a zero-max guard. */
export function depthFraction(value: number, max: number): number {
  return max <= 0 ? 0 : value / max;
}

/** Full-depth order book is a US LV3 entitlement (CLAUDE.md scope); every other market renders the no-depth state. */
export function entitledForDepth(symbol: string): boolean {
  return symbol.startsWith("US.");
}

function accumulate(levels: BookLevel[]): LadderRow[] {
  let cum = 0;
  return levels.slice(0, LADDER_LEVELS).map((l) => {
    cum += l.size;
    return { price: l.price, size: l.size, cum, cumFraction: 0 };
  });
}

/** Book sides (best-first, as delivered) → ladder rows with cumulative sums normalized across BOTH sides. */
export function buildLadderSides(book: Book | undefined): { asks: LadderRow[]; bids: LadderRow[] } {
  const asks = accumulate(book?.asks ?? []);
  const bids = accumulate(book?.bids ?? []);
  const maxCum = Math.max(asks.at(-1)?.cum ?? 0, bids.at(-1)?.cum ?? 0);
  for (const r of asks) r.cumFraction = depthFraction(r.cum, maxCum);
  for (const r of bids) r.cumFraction = depthFraction(r.cum, maxCum);
  return { asks, bids };
}

// Plan 5 owns the typed Order + the real 9-state lifecycle; until then ExecStore
// rows are unknown and this set is the conservative "still working" projection.
const WORKING_STATUSES = new Set(["PendingNew", "New", "PartiallyFilled", "Replacing", "PendingCancel"]);

/**
 * Tolerant, display-only projection of ExecStore's untyped order rows: an
 * order marks the ladder iff it names this symbol, carries a positive price
 * and remaining quantity, and is in a working status. Sell/Short → sell.
 */
export function workingOrderMarks(orders: unknown[], symbol: string): OrderMark[] {
  const marks: OrderMark[] = [];
  for (const o of orders) {
    if (typeof o !== "object" || o === null) continue;
    const r = o as Record<string, unknown>;
    if (r.symbol !== symbol) continue;
    if (typeof r.status !== "string" || !WORKING_STATUSES.has(r.status)) continue;
    if (typeof r.price !== "number" || r.price <= 0) continue;
    const qty = typeof r.leavesQty === "number" ? r.leavesQty : typeof r.qty === "number" ? r.qty : 0;
    if (qty <= 0) continue;
    const side = typeof r.side === "string" && r.side.toLowerCase().startsWith("s") ? "sell" : "buy";
    marks.push({ price: r.price, side, qty });
  }
  return marks;
}

/** 1 at the moment of the trade, linear to 0 at FLASH_MS. 0 for no flash or a skewed clock. */
export function flashAlpha(flash: TradeFlash | null, nowMs: number): number {
  if (!flash) return 0;
  const age = nowMs - flash.atMs;
  if (age < 0 || age >= FLASH_MS) return 0;
  return 1 - age / FLASH_MS;
}

export function buildLadderState(args: {
  symbol: string;
  book: Book | undefined;
  orders: unknown[];
  flash: TradeFlash | null;
  last: LastTrade | null;
  nowMs: number;
  width: number;
  height: number;
  palette: Palette;
}): LadderPaintState {
  const entitled = entitledForDepth(args.symbol);
  const { asks, bids } = buildLadderSides(entitled ? args.book : undefined);
  const prices = [...asks, ...bids].map((r) => r.price);
  if (args.last) prices.push(args.last.price);
  const spread = asks.length > 0 && bids.length > 0 ? asks[0].price - bids[0].price : null;
  return {
    symbol: args.symbol,
    entitled,
    asks,
    bids,
    decimals: priceDecimals(prices),
    spread,
    last: args.last,
    flash: args.flash,
    orders: workingOrderMarks(args.orders, args.symbol),
    nowMs: args.nowMs,
    width: args.width,
    height: args.height,
    palette: args.palette,
  };
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/render/ladder`
Expected: PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/ladder
git commit -m "feat(ui/render): pure ladder paint-state builder — depth normalization, tolerant order marks, flash decay, entitlement"
```

---

## Task 6: Ladder painter + goldens

**Files:**
- Create: `ui/src/render/ladder/paintLadder.ts`
- Test: `ui/test/golden/ladder.golden.test.ts`
- Create (generated): `ui/test/golden/goldens/ladder-{full,empty,flash,noentitle}-{light,dark}.png` (8 files)

**Interfaces:**
- Consumes: everything Task 5 produces; `FONTS` from `../palette`; `formatPrice`, `formatSize` from `../format`; the Task-4 harness.
- Produces: `const LADDER_ROW_H = 22`, `paintLadder(ctx: CanvasRenderingContext2D, s: LadderPaintState): void` — consumed by `LadderPanel` (Task 9). Fixed geometry: 20 px header + 10 ask rows + 26 px center row + 10 bid rows = 486 px at full height; content below the canvas clips.

- [ ] **Step 1: Write the painter**

```ts
// ui/src/render/ladder/paintLadder.ts
// Pure painter: paint(ctx, state). Rows are indexed by book level —
// y = rowIndex × LADDER_ROW_H — no viewport/window classes (Plan-1 roadmap).
import { FONTS } from "../palette";
import { formatPrice, formatSize } from "../format";
import { flashAlpha, LADDER_LEVELS, type LadderPaintState, type LadderRow } from "./ladderState";

export const LADDER_ROW_H = 22;
const HEADER_H = 20;
const CENTER_H = 26;
const GUTTER_W = 14; // order-mark gutter on the left edge
const PAD = 6;

const priceRight = (w: number): number => GUTTER_W + (w - GUTTER_W) * 0.32;
const sizeRight = (w: number): number => GUTTER_W + (w - GUTTER_W) * 0.65;

export function paintLadder(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, s.width, s.height);

  if (!s.entitled) {
    drawCenteredNote(ctx, s, "no depth entitlement", `${s.symbol} — full order book is US-only (LV3)`);
    return;
  }
  drawHeader(ctx, s);
  if (s.asks.length === 0 && s.bids.length === 0) {
    drawCenteredNote(ctx, s, "waiting for depth…", s.symbol);
    return;
  }

  const alpha = flashAlpha(s.flash, s.nowMs);
  // Ask block: worst ask at the top, best ask directly above the center row.
  for (let i = 0; i < LADDER_LEVELS; i++) {
    const row = s.asks[LADDER_LEVELS - 1 - i];
    if (row) drawRow(ctx, s, row, HEADER_H + i * LADDER_ROW_H, "ask", alpha);
  }
  drawCenterRow(ctx, s);
  const bidTop = HEADER_H + LADDER_LEVELS * LADDER_ROW_H + CENTER_H;
  for (let i = 0; i < LADDER_LEVELS; i++) {
    const row = s.bids[i];
    if (row) drawRow(ctx, s, row, bidTop + i * LADDER_ROW_H, "bid", alpha);
  }
}

function drawRow(
  ctx: CanvasRenderingContext2D,
  s: LadderPaintState,
  row: LadderRow,
  y: number,
  side: "ask" | "bid",
  alpha: number,
): void {
  const p = s.palette;
  const w = s.width;

  // cumulative depth bar, anchored to the right edge
  const barW = row.cumFraction * (w - GUTTER_W);
  ctx.fillStyle = side === "ask" ? p.depthAsk : p.depthBid;
  ctx.fillRect(w - barW, y, barW, LADDER_ROW_H);

  // last-trade flash behind the matching price row, decayed by age
  if (alpha > 0 && s.flash && s.flash.price === row.price) {
    ctx.globalAlpha = alpha;
    ctx.fillStyle =
      s.flash.direction === "BUY" ? p.flashBuy : s.flash.direction === "SELL" ? p.flashSell : p.flashNeutral;
    ctx.fillRect(GUTTER_W, y, w - GUTTER_W, LADDER_ROW_H);
    ctx.globalAlpha = 1;
  }

  ctx.font = `11px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  ctx.textAlign = "right";
  const midY = y + LADDER_ROW_H / 2;
  ctx.fillStyle = side === "ask" ? p.down : p.up;
  ctx.fillText(formatPrice(row.price, s.decimals), priceRight(w), midY);
  ctx.fillStyle = p.text;
  ctx.fillText(formatSize(row.size), sizeRight(w), midY);
  ctx.fillStyle = p.textMuted;
  ctx.fillText(formatSize(row.cum), w - PAD, midY);

  // display-only working-order marks: gutter triangle + row outline + remaining qty
  const marks = s.orders.filter((o) => o.price === row.price);
  if (marks.length > 0) {
    ctx.strokeStyle = p.orderMark;
    ctx.strokeRect(GUTTER_W + 0.5, y + 0.5, w - GUTTER_W - 1, LADDER_ROW_H - 1);
    ctx.fillStyle = p.orderMark;
    ctx.beginPath();
    ctx.moveTo(3, midY - 4);
    ctx.lineTo(3, midY + 4);
    ctx.lineTo(10, midY);
    ctx.closePath();
    ctx.fill();
    ctx.textAlign = "left";
    ctx.font = `9px ${FONTS.mono}`;
    ctx.fillText(formatSize(marks.reduce((q, m) => q + m.qty, 0)), GUTTER_W + 2, y + 6);
  }
}

function drawHeader(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  ctx.fillStyle = p.surface;
  ctx.fillRect(0, 0, s.width, HEADER_H);
  ctx.strokeStyle = p.border;
  ctx.beginPath();
  ctx.moveTo(0, HEADER_H - 0.5);
  ctx.lineTo(s.width, HEADER_H - 0.5);
  ctx.stroke();
  ctx.font = `10px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  ctx.textAlign = "right";
  ctx.fillStyle = p.textMuted;
  const midY = HEADER_H / 2;
  ctx.fillText("PRICE", priceRight(s.width), midY);
  ctx.fillText("SIZE", sizeRight(s.width), midY);
  ctx.fillText("CUM", s.width - PAD, midY);
}

function drawCenterRow(ctx: CanvasRenderingContext2D, s: LadderPaintState): void {
  const p = s.palette;
  const y = HEADER_H + LADDER_LEVELS * LADDER_ROW_H;
  ctx.fillStyle = p.surface;
  ctx.fillRect(0, y, s.width, CENTER_H);
  ctx.textBaseline = "middle";
  const midY = y + CENTER_H / 2;
  if (s.last) {
    ctx.font = `bold 13px ${FONTS.mono}`;
    ctx.textAlign = "left";
    ctx.fillStyle = s.last.direction === "BUY" ? p.up : s.last.direction === "SELL" ? p.down : p.neutral;
    ctx.fillText(formatPrice(s.last.price, s.decimals), GUTTER_W + 2, midY);
  }
  if (s.spread !== null) {
    ctx.font = `10px ${FONTS.mono}`;
    ctx.textAlign = "right";
    ctx.fillStyle = p.textMuted;
    ctx.fillText(`Δ ${formatPrice(s.spread, s.decimals)}`, s.width - PAD, midY);
  }
}

function drawCenteredNote(ctx: CanvasRenderingContext2D, s: LadderPaintState, title: string, sub: string): void {
  const p = s.palette;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillStyle = p.textMuted;
  ctx.font = `12px ${FONTS.mono}`;
  ctx.fillText(title, s.width / 2, s.height / 2 - 10);
  ctx.font = `10px ${FONTS.mono}`;
  ctx.fillText(sub, s.width / 2, s.height / 2 + 10);
}
```

- [ ] **Step 2: Write the golden tests** (the four roadmap-mandated ladder states × both variants)

```ts
// ui/test/golden/ladder.golden.test.ts
import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette } from "../../src/render/palette";
import type { Book } from "../../src/wire/contract";
import { buildLadderState, FLASH_MS } from "../../src/render/ladder/ladderState";
import { paintLadder } from "../../src/render/ladder/paintLadder";

const W = 300;
const H = 486; // header 20 + 10×22 + center 26 + 10×22
const NOW = 1_000_000; // fixed clock — flash age is NOW - atMs

// Deterministic fixture generators (no Math.random — multiplicative patterns).
function fullBook(): Book {
  return {
    symbol: "US.AAPL",
    bids: Array.from({ length: 10 }, (_, i) => ({ price: 3.49 - i * 0.01, size: 300 + ((i * 137) % 900) })),
    asks: Array.from({ length: 10 }, (_, i) => ({ price: 3.51 + i * 0.01, size: 250 + ((i * 211) % 1100) })),
    ts: "2026-07-06T13:35:00Z",
  };
}

// Order prices must land EXACTLY on generated level prices (marks match with
// ===): 3.49 - 2*0.01 === 3.47 and 3.51 + 2*0.01 === 3.53 hold in IEEE-754 —
// re-verify exact equality if you change the fixture's step or order prices.
const workingOrders = [
  { symbol: "US.AAPL", price: 3.47, side: "Buy", qty: 100, status: "New" },
  { symbol: "US.AAPL", price: 3.53, side: "Short", leavesQty: 50, qty: 80, status: "PartiallyFilled" },
];

describe("paintLadder goldens", () => {
  for (const mode of ["light", "dark"] as const) {
    const palette = getPalette(mode);
    const base = { orders: [] as unknown[], flash: null, last: null, nowMs: NOW, width: W, height: H, palette };

    it(`full book with working-order marks — ${mode}`, () => {
      const s = buildLadderState({
        ...base, symbol: "US.AAPL", book: fullBook(), orders: workingOrders,
        last: { price: 3.51, direction: "BUY" },
      });
      expectGolden(`ladder-full-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });

    it(`empty book (waiting for depth) — ${mode}`, () => {
      const s = buildLadderState({ ...base, symbol: "US.AAPL", book: undefined });
      expectGolden(`ladder-empty-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });

    it(`last-trade flash mid-decay — ${mode}`, () => {
      const s = buildLadderState({
        ...base, symbol: "US.AAPL", book: fullBook(),
        last: { price: 3.51, direction: "BUY" },
        flash: { price: 3.51, direction: "BUY", atMs: NOW - FLASH_MS / 2 }, // alpha 0.5
      });
      expectGolden(`ladder-flash-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });

    it(`no depth entitlement (non-US) — ${mode}`, () => {
      const s = buildLadderState({ ...base, symbol: "HK.00700", book: undefined });
      expectGolden(`ladder-noentitle-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });
  }
});
```

- [ ] **Step 3: Generate, eyeball, verify**

```bash
cd ui && npm run test:golden:update
open test/golden/__output__     # eyeball all 8 ladder PNGs in Finder:
#  - full: red asks above / green bids below, depth bars growing with cum, amber
#    outline + triangle + qty on 3.47 and 3.53, bold 3.51 + "Δ 0.02" center row
#  - empty: header + centered "waiting for depth…"
#  - flash: half-strength green band on the 3.51 ask row
#  - noentitle: centered "no depth entitlement / HK.00700 — full order book is US-only (LV3)"
npm run test:golden             # PASS against the fresh goldens
npm run typecheck && npm test   # everything else still green
```

- [ ] **Step 4: Commit**

```bash
git add ui/src/render/ladder/paintLadder.ts ui/test/golden/ladder.golden.test.ts ui/test/golden/goldens
git commit -m "feat(ui/render): L2 ladder painter + 8 goldens (full/empty/flash/no-entitlement x light/dark)"
```

---

## Task 7: TapeRing sequence tracking + tape paint-state builder (pure)

**Files:**
- Modify: `ui/src/data/TapeRing.ts`
- Modify: `ui/src/data/TapeRing.test.ts`
- Create: `ui/src/render/tape/tapeState.ts`
- Test: `ui/src/render/tape/tapeState.test.ts`

**Interfaces:**
- Consumes: `Tick`, `TickDirection` from `wire/contract`; `formatPrice`/`formatSize`/`formatTapeTime`/`priceDecimals` from `../format`; `Palette` from `../palette`.
- Produces (consumed by Tasks 8–10 and `LadderPanel` in Task 9):
  - On `TapeRing`: `lastSeq(): number` (1-based seq of the newest tick this generation; 0 when empty), `oldestSeq(): number` (= `lastSeq() - size() + 1`), `generation(): number` (bumped on every snapshot rebuild), `tickBySeq(s: number): Tick | undefined`.
  - In `tapeState.ts`: `const TAPE_ROW_H = 18`, `interface TapeSource { lastSeq(): number; oldestSeq(): number; generation(): number; tickBySeq(s: number): Tick | undefined }` (TapeRing satisfies it structurally), `interface TapeView { anchorSeq: number | null; generation: number }`, `interface TapeRow { seq: number; time: string; price: string; size: string; direction: TickDirection }`, `interface TapePaintState { rows: TapeRow[]; paused: boolean; width: number; height: number; palette: Palette }`, `liveView(src: TapeSource): TapeView`, `buildTapeRows(src: TapeSource, view: TapeView, opts: { symbol: string; minSize: number; maxRows: number }): { rows: TapeRow[]; paused: boolean }`, `adjustAnchor(src: TapeSource, view: TapeView, deltaRows: number, opts: { symbol: string; minSize: number }): TapeView`.

- [ ] **Step 1: Write the failing TapeRing tests** (append to `ui/src/data/TapeRing.test.ts` as a new self-contained `describe` block — leave every pre-existing test untouched and passing; if the file already has equivalent tick/message helpers, reuse them instead of duplicating)

```ts
describe("sequence tracking (Plan 3 pause anchoring)", () => {
  // Named to avoid shadowing the file's existing top-level snapshot helper.
  const seqTick = (n: number): Tick =>
    ({ symbol: "US.AAPL", price: 3.5, size: n, direction: "BUY", ts: `2026-07-06T13:30:0${n % 10}Z` });
  const seqSnap = (ticks: Tick[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.tape", payload: ticks });
  const seqDel = (ticks: Tick[]): DeltaMsg => ({ kind: "delta", topic: "md.tape", payload: ticks });

  it("numbers ticks monotonically and exposes the retained seq window", () => {
    const ring = new TapeRing(3);
    ring.apply(seqSnap([seqTick(1), seqTick(2)]));            // seqs 1, 2
    ring.apply(seqDel([seqTick(3), seqTick(4), seqTick(5)])); // seqs 3, 4, 5 — capacity 3 retains 3..5
    expect(ring.lastSeq()).toBe(5);
    expect(ring.oldestSeq()).toBe(3);
    expect(ring.tickBySeq(4)).toEqual(seqTick(4));
    expect(ring.tickBySeq(2)).toBeUndefined(); // overwritten
    expect(ring.tickBySeq(6)).toBeUndefined(); // not yet appended
  });

  it("bumps the generation and restarts seq on snapshot rebuild (reconnect)", () => {
    const ring = new TapeRing(8);
    ring.apply(seqSnap([seqTick(1), seqTick(2)]));
    const g1 = ring.generation();
    ring.apply(seqSnap([seqTick(3)]));
    expect(ring.generation()).toBe(g1 + 1);
    expect(ring.lastSeq()).toBe(1);
    expect(ring.tickBySeq(1)).toEqual(seqTick(3));
  });

  it("reports an empty seq window before any ticks", () => {
    const ring = new TapeRing(8);
    expect(ring.lastSeq()).toBe(0);
    expect(ring.oldestSeq()).toBe(1); // empty range: oldest > last
  });
});
```

(Add `Tick`, `SnapshotMsg`, `DeltaMsg` to the file's contract imports if not already there.)

- [ ] **Step 2: Run to verify the new tests fail**

Run: `cd ui && npx vitest run src/data/TapeRing.test.ts`
Expected: new tests FAIL (`lastSeq is not a function`); old tests PASS

- [ ] **Step 3: Extend `TapeRing`** (surgical additions — existing fields/methods unchanged)

Add two private fields and touch only the `apply` body:

```ts
  private seq = 0; // total ticks appended this generation — the newest tick's 1-based seq
  private gen = 0; // bumped on snapshot rebuild; anchors into an old generation are invalid

  apply(m: SnapshotMsg | DeltaMsg): void {
    const ticks = m.payload as Tick[];
    if (m.kind === "snapshot") {
      this.head = 0;
      this.count = 0;
      this.seq = 0;
      this.gen++;
    }
    for (const t of ticks) {
      this.buf[this.head] = t;
      this.head = (this.head + 1) % this.capacity;
      if (this.count < this.capacity) this.count++;
      this.seq++;
    }
    this.markDirty();
  }
```

Append the accessors:

```ts
  /** 1-based seq of the newest retained tick this generation; 0 when empty. */
  lastSeq(): number {
    return this.seq;
  }

  /** Seq of the oldest retained tick; lastSeq()+1 when empty (an empty range). */
  oldestSeq(): number {
    return this.seq - this.count + 1;
  }

  /** Bumped whenever a snapshot rebuilds the ring (reconnect re-sync). */
  generation(): number {
    return this.gen;
  }

  /** Tick by seq, or undefined once overwritten / never appended. */
  tickBySeq(s: number): Tick | undefined {
    if (s < this.oldestSeq() || s > this.seq) return undefined;
    return this.at(s - this.oldestSeq());
  }
```

Run: `cd ui && npx vitest run src/data/TapeRing.test.ts` → all PASS.

- [ ] **Step 4: Write the failing tapeState tests**

```ts
// ui/src/render/tape/tapeState.test.ts
import { describe, it, expect } from "vitest";
import type { Tick } from "../../wire/contract";
import { adjustAnchor, buildTapeRows, liveView, type TapeSource, type TapeView } from "./tapeState";

function mkTick(i: number, over: Partial<Tick> = {}): Tick {
  return {
    symbol: "US.AAPL",
    price: 3.5 + ((i % 5) - 2) * 0.01,
    size: 100 * (1 + (i % 3)), // 100 / 200 / 300
    direction: (["BUY", "SELL", "NEUTRAL"] as const)[i % 3],
    ts: new Date(Date.UTC(2026, 6, 6, 13, 30, i)).toISOString(),
    ...over,
  };
}

/** Array-backed TapeSource: tick k (1-based) has seq k. */
function srcFrom(ticks: Tick[], generation = 1): TapeSource {
  return {
    lastSeq: () => ticks.length,
    oldestSeq: () => 1,
    generation: () => generation,
    tickBySeq: (s) => (s >= 1 && s <= ticks.length ? ticks[s - 1] : undefined),
  };
}

const ticks = Array.from({ length: 30 }, (_, i) => mkTick(i + 1));
const src = srcFrom(ticks);

describe("buildTapeRows", () => {
  it("live view returns the newest rows, newest first", () => {
    const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 5 });
    expect(paused).toBe(false);
    expect(rows).toHaveLength(5);
    expect(rows.map((r) => r.seq)).toEqual([30, 29, 28, 27, 26]);
  });

  it("applies the min-size filter", () => {
    const { rows } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 300, maxRows: 50 });
    // size 300 hits every i where i % 3 === 2 → seqs 2, 5, 8, ..., 29 → 10 ticks
    expect(rows).toHaveLength(10);
    expect(rows.every((r) => r.size === "300")).toBe(true);
  });

  it("filters to the panel's symbol (the ring is shared per window)", () => {
    const mixed = srcFrom([mkTick(1), mkTick(2, { symbol: "US.NVDA" }), mkTick(3)]);
    const { rows } = buildTapeRows(mixed, liveView(mixed), { symbol: "US.AAPL", minSize: 0, maxRows: 10 });
    expect(rows.map((r) => r.seq)).toEqual([3, 1]);
  });

  it("an anchored view is paused and stays put", () => {
    const view: TapeView = { anchorSeq: 20, generation: 1 };
    const { rows, paused } = buildTapeRows(src, view, { symbol: "US.AAPL", minSize: 0, maxRows: 3 });
    expect(paused).toBe(true);
    expect(rows.map((r) => r.seq)).toEqual([20, 19, 18]);
  });

  it("a stale-generation anchor renders live (reconnect honesty)", () => {
    const view: TapeView = { anchorSeq: 20, generation: 0 };
    const { paused, rows } = buildTapeRows(src, view, { symbol: "US.AAPL", minSize: 0, maxRows: 3 });
    expect(paused).toBe(false);
    expect(rows[0].seq).toBe(30);
  });

  it("formats rows: ET time, uniform decimals, grouped sizes", () => {
    const one = srcFrom([mkTick(1, { price: 3.5, size: 1428, ts: "2026-07-06T13:30:05Z" })]);
    const { rows } = buildTapeRows(one, liveView(one), { symbol: "US.AAPL", minSize: 0, maxRows: 1 });
    expect(rows[0]).toMatchObject({ time: "09:30:05", price: "3.50", size: "1,428" });
  });

  it("is empty (not crashing) on an empty ring", () => {
    const empty = srcFrom([]);
    const { rows, paused } = buildTapeRows(empty, liveView(empty), { symbol: "US.AAPL", minSize: 0, maxRows: 5 });
    expect(rows).toEqual([]);
    expect(paused).toBe(false);
  });
});

describe("adjustAnchor", () => {
  const opts = { symbol: "US.AAPL", minSize: 0 };

  it("scrolling up from live pauses N rows back", () => {
    const v = adjustAnchor(src, liveView(src), -3, opts);
    expect(v).toEqual({ anchorSeq: 27, generation: 1 });
  });

  it("scrolling down toward the newest tick resumes live", () => {
    const v = adjustAnchor(src, { anchorSeq: 28, generation: 1 }, 5, opts);
    expect(v.anchorSeq).toBeNull();
  });

  it("steps in FILTERED row space so one wheel row is one on-screen row", () => {
    // minSize 300 keeps seqs 2, 5, 8, ..., 29; from live, 2 rows up lands on 26
    const v = adjustAnchor(src, liveView(src), -2, { symbol: "US.AAPL", minSize: 300 });
    expect(v.anchorSeq).toBe(26);
  });

  it("clamps at the retained tail instead of walking off", () => {
    const v = adjustAnchor(src, { anchorSeq: 3, generation: 1 }, -100, opts);
    expect(v.anchorSeq).toBe(1);
  });

  it("treats a stale-generation anchor as live before applying the delta", () => {
    const v = adjustAnchor(src, { anchorSeq: 5, generation: 0 }, -1, opts);
    expect(v).toEqual({ anchorSeq: 29, generation: 1 });
  });

  it("stays live on an empty ring", () => {
    const empty = srcFrom([]);
    expect(adjustAnchor(empty, liveView(empty), -3, opts).anchorSeq).toBeNull();
  });
});
```

- [ ] **Step 5: Run to verify they fail**

Run: `cd ui && npx vitest run src/render/tape`
Expected: FAIL — `Cannot find module './tapeState'`

- [ ] **Step 6: Write the implementation**

```ts
// ui/src/render/tape/tapeState.ts
// Pure view math for the time & sales tape. Rows are indexed by ring seq —
// y = rowIndex × TAPE_ROW_H — no viewport classes (Plan-1 roadmap). The pause
// anchor is a (seq, generation) pair: seqs are stable while ticks stream, and
// a generation bump (snapshot rebuild on reconnect) invalidates the anchor so
// a stale frozen view is never rendered as if it were still meaningful.
import type { Tick, TickDirection } from "../../wire/contract";
import type { Palette } from "../palette";
import { formatPrice, formatSize, formatTapeTime, priceDecimals } from "../format";

export const TAPE_ROW_H = 18;

/** What the tape needs from TapeRing (satisfied structurally; tests use plain fakes). */
export interface TapeSource {
  lastSeq(): number;
  oldestSeq(): number;
  generation(): number;
  tickBySeq(s: number): Tick | undefined;
}

export interface TapeView {
  anchorSeq: number | null; // seq of the top visible row; null = following live
  generation: number;
}

export interface TapeRow {
  seq: number;
  time: string;
  price: string;
  size: string;
  direction: TickDirection;
}

export interface TapePaintState {
  rows: TapeRow[]; // newest first — rows[0] is the top row
  paused: boolean;
  width: number;
  height: number;
  palette: Palette;
}

export function liveView(src: TapeSource): TapeView {
  return { anchorSeq: null, generation: src.generation() };
}

export function buildTapeRows(
  src: TapeSource,
  view: TapeView,
  opts: { symbol: string; minSize: number; maxRows: number },
): { rows: TapeRow[]; paused: boolean } {
  const last = src.lastSeq();
  const anchorValid = view.anchorSeq !== null && view.generation === src.generation() && view.anchorSeq < last;
  // An anchor that fell off the ring tail clamps to the oldest retained tick.
  const start = Math.max(anchorValid ? (view.anchorSeq as number) : last, src.oldestSeq());
  const raw: Tick[] = [];
  const seqs: number[] = [];
  for (let s = start; s >= src.oldestSeq() && raw.length < opts.maxRows; s--) {
    const t = src.tickBySeq(s);
    if (!t || t.symbol !== opts.symbol) continue;
    if (t.size < opts.minSize) continue;
    raw.push(t);
    seqs.push(s);
  }
  const decimals = priceDecimals(raw.map((t) => t.price));
  const rows = raw.map((t, i) => ({
    seq: seqs[i],
    time: formatTapeTime(t.ts),
    price: formatPrice(t.price, decimals),
    size: formatSize(t.size),
    direction: t.direction,
  }));
  return { rows, paused: anchorValid };
}

/**
 * Move the view by deltaRows visible rows (negative = older). Steps through
 * ticks matching the symbol + filter so one wheel row always moves one
 * on-screen row regardless of filter density. Reaching the live edge resumes
 * following; hitting the retained tail clamps to the oldest match.
 */
export function adjustAnchor(
  src: TapeSource,
  view: TapeView,
  deltaRows: number,
  opts: { symbol: string; minSize: number },
): TapeView {
  const gen = src.generation();
  const last = src.lastSeq();
  const oldest = src.oldestSeq();
  let seq = view.anchorSeq !== null && view.generation === gen ? view.anchorSeq : last;
  const step = deltaRows < 0 ? -1 : 1;
  let remaining = Math.abs(deltaRows);
  while (remaining > 0) {
    let q = seq + step;
    while (q >= oldest && q <= last) {
      const t = src.tickBySeq(q);
      if (t && t.symbol === opts.symbol && t.size >= opts.minSize) break;
      q += step;
    }
    if (q < oldest) break; // tail — stay on the oldest match found so far
    if (q >= last) return { anchorSeq: null, generation: gen }; // live edge — resume
    seq = q;
    remaining--;
  }
  if (seq >= last) return { anchorSeq: null, generation: gen };
  return { anchorSeq: seq, generation: gen };
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd ui && npx vitest run src/render/tape src/data/TapeRing.test.ts`
Expected: PASS (13 new + existing)

- [ ] **Step 8: Commit**

```bash
git add ui/src/data/TapeRing.ts ui/src/data/TapeRing.test.ts ui/src/render/tape
git commit -m "feat(ui): TapeRing seq/generation tracking + pure tape view math (filter, pause anchor, filtered-space scroll)"
```

---

## Task 8: Tape painter + goldens

**Files:**
- Create: `ui/src/render/tape/paintTape.ts`
- Test: `ui/test/golden/tape.golden.test.ts`
- Create (generated): `ui/test/golden/goldens/tape-{live,filtered,paused}-{light,dark}.png` (6 files)

**Interfaces:**
- Consumes: `TapePaintState`, `TAPE_ROW_H`, `buildTapeRows`, `liveView` from Task 7; `FONTS` from `../palette`; the Task-4 harness.
- Produces: `paintTape(ctx: CanvasRenderingContext2D, s: TapePaintState): void` — consumed by `TapePanel` (Task 10).

- [ ] **Step 1: Write the painter**

```ts
// ui/src/render/tape/paintTape.ts
// Pure painter: paint(ctx, state). Newest print on top; y = rowIndex × TAPE_ROW_H.
import { FONTS } from "../palette";
import { TAPE_ROW_H, type TapePaintState } from "./tapeState";

const PAD = 6;

export function paintTape(ctx: CanvasRenderingContext2D, s: TapePaintState): void {
  const p = s.palette;
  ctx.fillStyle = p.bg;
  ctx.fillRect(0, 0, s.width, s.height);

  if (s.rows.length === 0) {
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillStyle = p.textMuted;
    ctx.font = `11px ${FONTS.mono}`;
    ctx.fillText("no prints yet", s.width / 2, s.height / 2);
    return;
  }

  ctx.font = `11px ${FONTS.mono}`;
  ctx.textBaseline = "middle";
  for (let i = 0; i < s.rows.length; i++) {
    const top = i * TAPE_ROW_H;
    if (top > s.height) break;
    const r = s.rows[i];
    const midY = top + TAPE_ROW_H / 2;
    const dirColor = r.direction === "BUY" ? p.up : r.direction === "SELL" ? p.down : p.neutral;
    ctx.fillStyle = p.textMuted;
    ctx.textAlign = "left";
    ctx.fillText(r.time, PAD, midY);
    ctx.fillStyle = dirColor;
    ctx.textAlign = "right";
    ctx.fillText(r.price, s.width * 0.68, midY);
    ctx.fillText(r.size, s.width - PAD, midY);
  }

  // honesty: a paused tape is visibly not live (the chrome pill is the control;
  // this strip marks the surface itself)
  if (s.paused) {
    ctx.fillStyle = p.warn;
    ctx.fillRect(0, 0, s.width, 2);
  }
}
```

- [ ] **Step 2: Write the golden tests**

```ts
// ui/test/golden/tape.golden.test.ts
import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette } from "../../src/render/palette";
import type { Tick } from "../../src/wire/contract";
import { buildTapeRows, liveView, type TapeSource } from "../../src/render/tape/tapeState";
import { paintTape } from "../../src/render/tape/paintTape";

const W = 260;
const H = 360; // 20 rows × 18

function mkTick(i: number): Tick {
  return {
    symbol: "US.AAPL",
    price: 3.5 + ((i % 5) - 2) * 0.01,
    size: 50 + ((i * 173) % 950),
    direction: (["BUY", "SELL", "NEUTRAL", "BUY", "SELL", "BUY", "BUY", "SELL", "NEUTRAL", "BUY"] as const)[i % 10],
    ts: new Date(Date.UTC(2026, 6, 6, 13, 30, i * 2)).toISOString(),
  };
}

const ticks = Array.from({ length: 30 }, (_, i) => mkTick(i + 1));
const src: TapeSource = {
  lastSeq: () => ticks.length,
  oldestSeq: () => 1,
  generation: () => 1,
  tickBySeq: (s) => (s >= 1 && s <= ticks.length ? ticks[s - 1] : undefined),
};

describe("paintTape goldens", () => {
  for (const mode of ["light", "dark"] as const) {
    const palette = getPalette(mode);

    it(`live tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tape-live-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    it(`min-size-filtered tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 500, maxRows: 20 });
      expectGolden(`tape-filtered-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    it(`paused (scrolled back) tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, { anchorSeq: 24, generation: 1 }, { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tape-paused-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });
  }
});
```

- [ ] **Step 3: Generate, eyeball, verify**

```bash
cd ui && npm run test:golden:update
open test/golden/__output__   # eyeball the 6 tape PNGs:
#  - live: 20 rows, muted ET times, green/red/gray prices+sizes, newest (09:31:00) on top
#  - filtered: fewer rows, every size ≥ 500
#  - paused: top row is seq 24 (09:30:48) with the 2px amber strip across the top
npm run test:golden           # PASS
```

- [ ] **Step 4: Commit**

```bash
git add ui/src/render/tape/paintTape.ts ui/test/golden/tape.golden.test.ts ui/test/golden/goldens
git commit -m "feat(ui/render): time & sales painter + 6 goldens (live/filtered/paused x light/dark)"
```

---

## Task 9: LadderPanel (chrome) + registry + seed

**Files:**
- Create: `ui/src/chrome/panels/LadderPanel.tsx`
- Test: `ui/src/chrome/panels/LadderPanel.test.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (add the `"ladder"` entry)
- Modify: `ui/src/seeds/workspaces.ts` (t-ladder seed settings)

**Interfaces:**
- Consumes: `PanelProps` from `./registry`; `useTheme` from `../ThemeProvider`; `applyCanvasSize` (Task 3); `buildLadderState`, `flashAlpha`, `TradeFlash`, `LastTrade` (Task 5); `paintLadder` (Task 6); `stores.book.getRev()/get()`, `stores.tape` seq API (Task 7), `stores.exec` (ReactStore); `linkGroups.symbolFor/subscribe`.
- Produces: registry entry `"ladder": { component: LadderPanel, topics: ["md.book", "md.tape", "exec.orders"] }`. Trading seed `t-ladder` gains `settings: { symbol: "US.AAPL" }` (the pinned fallback when its group has no focus yet).

- [ ] **Step 1: Write the failing component test** (mirrors `ChartPanel.test.tsx`'s setup style)

```tsx
// ui/src/chrome/panels/LadderPanel.test.tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LadderPanel } from "./LadderPanel";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf, type Surface } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";

beforeEach(() => {
  vi.clearAllMocks();
  cleanup();
});

function renderLadder(settings: Record<string, unknown> = { symbol: "US.AAPL" }) {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  let surface: Surface | undefined;
  const off = vi.fn();
  vi.spyOn(scheduler, "register").mockImplementation((s: Surface) => {
    surface = s;
    return off;
  });
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const config = { id: "t-ladder", panelId: "ladder", group: "green" as const, settings };
  const utils = render(
    <ThemeProvider>
      <LadderPanel config={config} stores={stores} scheduler={scheduler} width={300} height={480}
        linkGroups={linkGroups} commands={{ sendCommand: vi.fn(async () => ({ status: "accepted" })) }}
        onConfigChange={vi.fn()} />
    </ThemeProvider>,
  );
  return { ...utils, stores, linkGroups, surface: () => surface!, off };
}

describe("LadderPanel", () => {
  it("registers one surface and unregisters it on unmount", () => {
    const { surface, off, unmount } = renderLadder();
    expect(surface().id).toBe("ladder:t-ladder");
    unmount();
    expect(off).toHaveBeenCalledTimes(1);
  });

  it("is dirty after a book update and paints without throwing", () => {
    const { stores, surface } = renderLadder();
    surface().isDirty(); // baseline the rev cursors
    stores.book.apply({
      kind: "snapshot", topic: "md.book",
      payload: { symbol: "US.AAPL", bids: [{ price: 3.49, size: 300 }], asks: [{ price: 3.51, size: 400 }], ts: "t" },
    });
    expect(surface().isDirty()).toBe(true);
    expect(() => surface().paint()).not.toThrow();
  });

  it("paints the no-entitlement state for non-US symbols without throwing", () => {
    const { surface, linkGroups } = renderLadder();
    linkGroups.focus("green", "HK.00700");
    expect(surface().isDirty()).toBe(true);
    expect(() => surface().paint()).not.toThrow();
  });

  it("repaints when exec orders change (marks are display-only but live)", () => {
    const { stores, surface } = renderLadder();
    surface().isDirty();
    stores.exec.apply({ kind: "snapshot", topic: "exec.orders",
      payload: [{ symbol: "US.AAPL", price: 3.49, side: "Buy", qty: 100, status: "New" }] });
    expect(surface().isDirty()).toBe(true);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/LadderPanel.test.tsx`
Expected: FAIL — `Cannot find module './LadderPanel'`

- [ ] **Step 3: Write the panel**

```tsx
// ui/src/chrome/panels/LadderPanel.tsx
// L2 ladder panel: canvas mounted once, painted imperatively by the scheduler.
// High-frequency data (book, ticks) never touches React state; the only React
// here is the mount itself. Store dirtiness is observed via getRev() cursors
// (BookStore/TapeRing are shared across panels — never consumeDirty()).
import { useEffect, useRef } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { applyCanvasSize } from "../../render/canvas";
import { buildLadderState, flashAlpha, type LastTrade, type TradeFlash } from "../../render/ladder/ladderState";
import { paintLadder } from "../../render/ladder/paintLadder";

export function LadderPanel({ config, stores, scheduler, width, height, linkGroups }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const { palette } = useTheme();

  // Refs bridge React-world changes (size, theme) into the paint loop without
  // remounting the surface; forceRef bumps mark the surface dirty.
  const paletteRef = useRef(palette);
  paletteRef.current = palette;
  const sizeRef = useRef({ width, height });
  sizeRef.current = { width, height };
  const forceRef = useRef(0);
  useEffect(() => {
    forceRef.current++;
  }, [width, height, palette]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
    let symbol = linkGroups.symbolFor(config.group) ?? seedSymbol;
    let last: LastTrade | null = null;
    let flash: TradeFlash | null = null;
    let tapeGen = stores.tape.generation();
    let tapeSeq = 0;

    // Seed "last trade" from whatever the shared ring already holds for us.
    const seedLast = (): void => {
      last = null;
      for (let q = stores.tape.lastSeq(); q >= stores.tape.oldestSeq(); q--) {
        const t = stores.tape.tickBySeq(q);
        if (t && t.symbol === symbol) {
          last = { price: t.price, direction: t.direction };
          break;
        }
      }
      tapeSeq = stores.tape.lastSeq();
    };
    seedLast();

    const offLink = linkGroups.subscribe(() => {
      const next = linkGroups.symbolFor(config.group) ?? seedSymbol;
      if (next !== symbol) {
        symbol = next;
        flash = null;
        seedLast();
        forceRef.current++;
      }
    });

    let orders: unknown[] = stores.exec.getSnapshot().orders;
    const offExec = stores.exec.subscribe(() => {
      orders = stores.exec.getSnapshot().orders;
      forceRef.current++;
    });

    let lastBookRev = -1;
    let lastTapeRev = -1;
    let lastForce = -1;
    const off = scheduler.register({
      id: `ladder:${config.id}`,
      isDirty: () => {
        const bookRev = stores.book.getRev();
        const tapeRev = stores.tape.getRev();
        const changed = bookRev !== lastBookRev || tapeRev !== lastTapeRev || forceRef.current !== lastForce;
        lastBookRev = bookRev;
        lastTapeRev = tapeRev;
        lastForce = forceRef.current;
        // an active flash keeps the surface animating until it decays out
        return changed || flashAlpha(flash, performance.now()) > 0;
      },
      paint: () => {
        // reconnect re-sync rebuilt the ring — reseed instead of walking stale seqs
        if (tapeGen !== stores.tape.generation()) {
          tapeGen = stores.tape.generation();
          flash = null;
          seedLast();
        }
        const tip = stores.tape.lastSeq();
        for (let q = Math.max(tapeSeq + 1, stores.tape.oldestSeq()); q <= tip; q++) {
          const t = stores.tape.tickBySeq(q);
          if (t && t.symbol === symbol) {
            last = { price: t.price, direction: t.direction };
            flash = { price: t.price, direction: t.direction, atMs: performance.now() };
          }
        }
        tapeSeq = tip;

        const { width: w, height: h } = sizeRef.current;
        if (!applyCanvasSize(canvas, ctx, w, h, window.devicePixelRatio || 1)) return;
        paintLadder(ctx, buildLadderState({
          symbol,
          book: stores.book.get(symbol),
          orders,
          flash,
          last,
          nowMs: performance.now(),
          width: w,
          height: h,
          palette: paletteRef.current,
        }));
      },
    });

    return () => {
      off();
      offLink();
      offExec();
    };
  }, [config.id]);

  return <canvas ref={canvasRef} style={{ display: "block", width: "100%", height: "100%" }} />;
}
```

- [ ] **Step 4: Register the panel and seed its symbol**

In `ui/src/chrome/panels/registry.tsx`, add the import and entry (insert the entry directly after `"chart"`, keeping the record grouped by plan):

```tsx
import { LadderPanel } from "./LadderPanel";
```

```tsx
  "ladder": {
    component: LadderPanel,
    topics: ["md.book", "md.tape", "exec.orders"],
  },
```

In `ui/src/seeds/workspaces.ts`, change the trading seed line:

```ts
      { id: "t-ladder", panelId: "ladder", group: "green", settings: { symbol: "US.AAPL" } },
```

- [ ] **Step 5: Run tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/panels/LadderPanel.test.tsx && npm run typecheck`
Expected: PASS (4 tests), clean typecheck

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/panels/LadderPanel.tsx ui/src/chrome/panels/LadderPanel.test.tsx ui/src/chrome/panels/registry.tsx ui/src/seeds/workspaces.ts
git commit -m "feat(ui/chrome): LadderPanel — L2 ladder live in the trading workspace"
```

---

## Task 10: TapePanel (chrome) + registry + seed

**Files:**
- Create: `ui/src/chrome/panels/TapePanel.tsx`
- Test: `ui/src/chrome/panels/TapePanel.test.tsx`
- Modify: `ui/src/chrome/panels/registry.tsx` (add the `"tape"` entry)
- Modify: `ui/src/seeds/workspaces.ts` (t-tape seed settings)

**Interfaces:**
- Consumes: `PanelProps`; `applyCanvasSize` (Task 3); `scrollAccumulate` (Task 2); `buildTapeRows`, `adjustAnchor`, `liveView`, `TAPE_ROW_H`, `TapeView` (Task 7); `paintTape` (Task 8); `stores.tape`; `onConfigChange` for the persisted `minSize`.
- Produces: registry entry `"tape": { component: TapePanel, topics: ["md.tape"] }`. Trading seed `t-tape` gains `settings: { symbol: "US.AAPL", minSize: 0 }`. Panel chrome: a 26 px header row (min-size input + paused pill/jump-to-live button — low-rate React state, user-event driven) above the canvas.

- [ ] **Step 1: Write the failing component test**

```tsx
// ui/src/chrome/panels/TapePanel.test.tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { TapePanel } from "./TapePanel";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf, type Surface } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { Tick } from "../../wire/contract";

beforeEach(() => {
  vi.clearAllMocks();
  cleanup();
});

function mkTick(i: number): Tick {
  return { symbol: "US.AAPL", price: 3.5, size: 100 + i, direction: "BUY", ts: "2026-07-06T13:30:00Z" };
}

function renderTape() {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  let surface: Surface | undefined;
  const off = vi.fn();
  vi.spyOn(scheduler, "register").mockImplementation((s: Surface) => {
    surface = s;
    return off;
  });
  const onConfigChange = vi.fn();
  const config = { id: "t-tape", panelId: "tape", group: "green" as const, settings: { symbol: "US.AAPL", minSize: 0 } };
  const utils = render(
    <ThemeProvider>
      <TapePanel config={config} stores={stores} scheduler={scheduler} width={260} height={400}
        linkGroups={new LinkGroups(new BroadcastChannelBus(), () => {})}
        commands={{ sendCommand: vi.fn(async () => ({ status: "accepted" })) }}
        onConfigChange={onConfigChange} />
    </ThemeProvider>,
  );
  const canvas = utils.container.querySelector("canvas")!;
  return { ...utils, stores, canvas, surface: () => surface!, off, onConfigChange };
}

describe("TapePanel", () => {
  it("registers one surface and unregisters on unmount", () => {
    const { surface, off, unmount } = renderTape();
    expect(surface().id).toBe("tape:t-tape");
    unmount();
    expect(off).toHaveBeenCalledTimes(1);
  });

  it("persists the min-size filter through onConfigChange", () => {
    const { onConfigChange } = renderTape();
    fireEvent.change(screen.getByLabelText(/min size/i), { target: { value: "250" } });
    expect(onConfigChange).toHaveBeenCalledWith({ symbol: "US.AAPL", minSize: 250 });
  });

  it("wheel-up pauses (pill appears); jump to live resumes", () => {
    const { stores, canvas } = renderTape();
    stores.tape.apply({ kind: "snapshot", topic: "md.tape",
      payload: Array.from({ length: 30 }, (_, i) => mkTick(i)) });
    expect(screen.queryByText(/jump to live/i)).toBeNull();
    fireEvent.wheel(canvas, { deltaY: -54 }); // 3 rows up at TAPE_ROW_H = 18
    expect(screen.getByText(/jump to live/i)).toBeTruthy();
    fireEvent.click(screen.getByText(/jump to live/i));
    expect(screen.queryByText(/jump to live/i)).toBeNull();
  });

  it("paints without throwing on an empty ring", () => {
    const { surface } = renderTape();
    expect(() => surface().paint()).not.toThrow();
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run src/chrome/panels/TapePanel.test.tsx`
Expected: FAIL — `Cannot find module './TapePanel'`

- [ ] **Step 3: Write the panel**

```tsx
// ui/src/chrome/panels/TapePanel.tsx
// Time & sales panel: canvas tape over the shared TapeRing. The min-size input
// and paused pill are low-rate user-event React state (allowed); tick flow
// itself never touches React. Wheel scrolling pauses; the anchor is a
// (seq, generation) pair so a reconnect re-sync honestly drops the pause.
import { useEffect, useRef, useState } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { applyCanvasSize } from "../../render/canvas";
import { scrollAccumulate } from "../../render/scroll";
import { FONTS } from "../../render/palette";
import {
  adjustAnchor, buildTapeRows, liveView, TAPE_ROW_H, type TapeView,
} from "../../render/tape/tapeState";
import { paintTape } from "../../render/tape/paintTape";

const HEADER_H = 26;

export function TapePanel({ config, stores, scheduler, width, height, linkGroups, onConfigChange }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const { palette } = useTheme();
  const [paused, setPaused] = useState(false);
  const [minSize, setMinSize] = useState<number>(
    typeof config.settings.minSize === "number" ? config.settings.minSize : 0,
  );

  const paletteRef = useRef(palette);
  paletteRef.current = palette;
  const sizeRef = useRef({ width, height });
  sizeRef.current = { width, height };
  const minSizeRef = useRef(minSize);
  minSizeRef.current = minSize;
  const viewRef = useRef<TapeView>({ anchorSeq: null, generation: 0 });
  const remainderRef = useRef(0);
  const symbolRef = useRef("");
  const forceRef = useRef(0);
  useEffect(() => {
    forceRef.current++;
  }, [width, height, palette, minSize]);

  const jumpToLive = (): void => {
    viewRef.current = liveView(stores.tape);
    remainderRef.current = 0;
    setPaused(false);
    forceRef.current++;
  };

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const seedSymbol = typeof config.settings.symbol === "string" ? config.settings.symbol : "US.AAPL";
    symbolRef.current = linkGroups.symbolFor(config.group) ?? seedSymbol;
    const offLink = linkGroups.subscribe(() => {
      const next = linkGroups.symbolFor(config.group) ?? seedSymbol;
      if (next !== symbolRef.current) {
        symbolRef.current = next;
        jumpToLive();
      }
    });

    // Native non-passive listener: React attaches wheel passively at the root,
    // which would ignore preventDefault and let the page scroll.
    const onWheel = (e: WheelEvent): void => {
      e.preventDefault();
      const acc = scrollAccumulate(remainderRef.current, e.deltaY, TAPE_ROW_H);
      remainderRef.current = acc.remainder;
      if (acc.rows === 0) return;
      // wheel up (deltaY < 0) → negative rows → older; wheel down → toward live
      viewRef.current = adjustAnchor(stores.tape, viewRef.current, acc.rows, {
        symbol: symbolRef.current,
        minSize: minSizeRef.current,
      });
      setPaused(viewRef.current.anchorSeq !== null); // low-rate: one per wheel event
      forceRef.current++;
    };
    canvas.addEventListener("wheel", onWheel, { passive: false });

    let lastTapeRev = -1;
    let lastForce = -1;
    const off = scheduler.register({
      id: `tape:${config.id}`,
      isDirty: () => {
        const rev = stores.tape.getRev();
        const changed = rev !== lastTapeRev || forceRef.current !== lastForce;
        lastTapeRev = rev;
        lastForce = forceRef.current;
        return changed;
      },
      paint: () => {
        // reconnect rebuilt the ring while paused → the anchor is meaningless;
        // resume live and drop the pill (once per reconnect — low-rate setState)
        if (viewRef.current.anchorSeq !== null && viewRef.current.generation !== stores.tape.generation()) {
          viewRef.current = liveView(stores.tape);
          setPaused(false);
        }
        const { width: w, height: h } = sizeRef.current;
        const canvasH = h - HEADER_H;
        if (!applyCanvasSize(canvas, ctx, w, canvasH, window.devicePixelRatio || 1)) return;
        const { rows, paused: p } = buildTapeRows(stores.tape, viewRef.current, {
          symbol: symbolRef.current,
          minSize: minSizeRef.current,
          maxRows: Math.ceil(canvasH / TAPE_ROW_H) + 1,
        });
        paintTape(ctx, { rows, paused: p, width: w, height: canvasH, palette: paletteRef.current });
      },
    });

    return () => {
      off();
      offLink();
      canvas.removeEventListener("wheel", onWheel);
    };
  }, [config.id]);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div
        style={{
          display: "flex", alignItems: "center", gap: 8, height: HEADER_H, padding: "0 6px",
          background: palette.surface, borderBottom: `1px solid ${palette.border}`,
          fontSize: 11, fontFamily: FONTS.sans, color: palette.textMuted, flex: "none",
        }}
      >
        <label>
          min size{" "}
          <input
            type="number" min={0} value={minSize} style={{ width: 64 }}
            onChange={(e) => {
              const v = Math.max(0, Number(e.target.value) || 0);
              setMinSize(v);
              onConfigChange({ ...config.settings, minSize: v });
            }}
          />
        </label>
        {paused && (
          <button
            onClick={jumpToLive}
            style={{ marginLeft: "auto", color: palette.warn, background: "none", border: "none", cursor: "pointer", fontSize: 11 }}
          >
            ⏸ paused — jump to live
          </button>
        )}
      </div>
      <canvas ref={canvasRef} style={{ display: "block", flex: 1, minHeight: 0 }} />
    </div>
  );
}
```

- [ ] **Step 4: Register the panel and seed its settings**

In `ui/src/chrome/panels/registry.tsx` (insert the entry directly after `"ladder"`):

```tsx
import { TapePanel } from "./TapePanel";
```

```tsx
  "tape": {
    component: TapePanel,
    topics: ["md.tape"],
  },
```

In `ui/src/seeds/workspaces.ts`, change the trading seed line:

```ts
      { id: "t-tape", panelId: "tape", group: "green", settings: { symbol: "US.AAPL", minSize: 0 } },
```

- [ ] **Step 5: Run tests + typecheck**

Run: `cd ui && npx vitest run src/chrome/panels/TapePanel.test.tsx && npm run typecheck`
Expected: PASS (4 tests), clean typecheck

- [ ] **Step 6: Commit**

```bash
git add ui/src/chrome/panels/TapePanel.tsx ui/src/chrome/panels/TapePanel.test.tsx ui/src/chrome/panels/registry.tsx ui/src/seeds/workspaces.ts
git commit -m "feat(ui/chrome): TapePanel — time & sales with min-size filter and pause-on-scroll"
```

---

## Task 11: Dev fixture + mock-engine wiring + dev-app verification

**Files:**
- Create: `ui/fixtures/ladder-tape.json`
- Modify: `ui/mock-engine/run.ts` (fixture-selection comment only)

**Interfaces:**
- Consumes: the mock engine's `Fixture` shape (`{ snapshots: [{topic, key?, payload}], deltas: [{afterMs, topic, key?, payload}] }`) and the wire payloads `Book`, `Tick[]`, plus an `exec.orders` snapshot array (untyped rows, matching `workingOrderMarks`' tolerant reads).
- Produces: a replayable dev session for both new panels.

- [ ] **Step 1: Write the fixture**

```json
{
  "snapshots": [
    { "topic": "md.book", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "ts": "2026-07-06T13:30:00Z",
      "bids": [
        { "price": 3.49, "size": 300 }, { "price": 3.48, "size": 650 }, { "price": 3.47, "size": 420 },
        { "price": 3.46, "size": 900 }, { "price": 3.45, "size": 380 }, { "price": 3.44, "size": 760 },
        { "price": 3.43, "size": 210 }, { "price": 3.42, "size": 1200 }, { "price": 3.41, "size": 540 },
        { "price": 3.40, "size": 800 }
      ],
      "asks": [
        { "price": 3.51, "size": 400 }, { "price": 3.52, "size": 260 }, { "price": 3.53, "size": 720 },
        { "price": 3.54, "size": 310 }, { "price": 3.55, "size": 980 }, { "price": 3.56, "size": 450 },
        { "price": 3.57, "size": 180 }, { "price": 3.58, "size": 1100 }, { "price": 3.59, "size": 620 },
        { "price": 3.60, "size": 340 }
      ] } },
    { "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.50, "size": 120, "direction": "BUY",     "ts": "2026-07-06T13:29:58.120Z" },
      { "symbol": "US.AAPL", "price": 3.50, "size": 45,  "direction": "NEUTRAL", "ts": "2026-07-06T13:29:58.640Z" },
      { "symbol": "US.AAPL", "price": 3.49, "size": 600, "direction": "SELL",    "ts": "2026-07-06T13:29:59.010Z" },
      { "symbol": "US.AAPL", "price": 3.51, "size": 250, "direction": "BUY",     "ts": "2026-07-06T13:29:59.480Z" },
      { "symbol": "US.AAPL", "price": 3.51, "size": 1400, "direction": "BUY",    "ts": "2026-07-06T13:29:59.720Z" },
      { "symbol": "US.AAPL", "price": 3.49, "size": 80,  "direction": "SELL",    "ts": "2026-07-06T13:29:59.910Z" }
    ] },
    { "topic": "exec.orders", "payload": [
      { "id": "o-1", "symbol": "US.AAPL", "side": "Buy",   "price": 3.47, "qty": 100, "status": "New" },
      { "id": "o-2", "symbol": "US.AAPL", "side": "Short", "price": 3.55, "qty": 80, "leavesQty": 50, "status": "PartiallyFilled" }
    ] }
  ],
  "deltas": [
    { "afterMs": 250,  "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.51, "size": 300, "direction": "BUY", "ts": "2026-07-06T13:30:00.250Z" } ] },
    { "afterMs": 400,  "topic": "md.book", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "ts": "2026-07-06T13:30:00.400Z",
      "bids": [
        { "price": 3.49, "size": 260 }, { "price": 3.48, "size": 700 }, { "price": 3.47, "size": 420 },
        { "price": 3.46, "size": 880 }, { "price": 3.45, "size": 380 }, { "price": 3.44, "size": 760 },
        { "price": 3.43, "size": 260 }, { "price": 3.42, "size": 1150 }, { "price": 3.41, "size": 540 },
        { "price": 3.40, "size": 800 } ],
      "asks": [
        { "price": 3.51, "size": 120 }, { "price": 3.52, "size": 300 }, { "price": 3.53, "size": 720 },
        { "price": 3.54, "size": 310 }, { "price": 3.55, "size": 940 }, { "price": 3.56, "size": 470 },
        { "price": 3.57, "size": 180 }, { "price": 3.58, "size": 1100 }, { "price": 3.59, "size": 600 },
        { "price": 3.60, "size": 340 } ] } },
    { "afterMs": 600,  "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.51, "size": 55,  "direction": "BUY",     "ts": "2026-07-06T13:30:00.600Z" },
      { "symbol": "US.AAPL", "price": 3.50, "size": 900, "direction": "SELL",    "ts": "2026-07-06T13:30:00.610Z" } ] },
    { "afterMs": 900,  "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.49, "size": 150, "direction": "SELL",    "ts": "2026-07-06T13:30:00.900Z" } ] },
    { "afterMs": 1200, "topic": "md.book", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "ts": "2026-07-06T13:30:01.200Z",
      "bids": [
        { "price": 3.50, "size": 200 }, { "price": 3.49, "size": 340 }, { "price": 3.48, "size": 700 },
        { "price": 3.47, "size": 420 }, { "price": 3.46, "size": 880 }, { "price": 3.45, "size": 420 },
        { "price": 3.44, "size": 760 }, { "price": 3.43, "size": 260 }, { "price": 3.42, "size": 1150 },
        { "price": 3.41, "size": 540 } ],
      "asks": [
        { "price": 3.51, "size": 90 },  { "price": 3.52, "size": 320 }, { "price": 3.53, "size": 700 },
        { "price": 3.54, "size": 330 }, { "price": 3.55, "size": 940 }, { "price": 3.56, "size": 470 },
        { "price": 3.57, "size": 200 }, { "price": 3.58, "size": 1080 }, { "price": 3.59, "size": 600 },
        { "price": 3.60, "size": 360 } ] } },
    { "afterMs": 1500, "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.51, "size": 2200, "direction": "BUY",    "ts": "2026-07-06T13:30:01.500Z" },
      { "symbol": "US.AAPL", "price": 3.51, "size": 60,   "direction": "NEUTRAL","ts": "2026-07-06T13:30:01.530Z" } ] },
    { "afterMs": 1900, "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.50, "size": 480, "direction": "SELL",    "ts": "2026-07-06T13:30:01.900Z" } ] },
    { "afterMs": 2300, "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.52, "size": 130, "direction": "BUY",     "ts": "2026-07-06T13:30:02.300Z" },
      { "symbol": "US.AAPL", "price": 3.52, "size": 75,  "direction": "BUY",     "ts": "2026-07-06T13:30:02.340Z" } ] },
    { "afterMs": 2700, "topic": "md.book", "key": "US.AAPL", "payload": { "symbol": "US.AAPL", "ts": "2026-07-06T13:30:02.700Z",
      "bids": [
        { "price": 3.50, "size": 320 }, { "price": 3.49, "size": 340 }, { "price": 3.48, "size": 640 },
        { "price": 3.47, "size": 460 }, { "price": 3.46, "size": 880 }, { "price": 3.45, "size": 420 },
        { "price": 3.44, "size": 720 }, { "price": 3.43, "size": 260 }, { "price": 3.42, "size": 1150 },
        { "price": 3.41, "size": 560 } ],
      "asks": [
        { "price": 3.52, "size": 280 }, { "price": 3.53, "size": 700 }, { "price": 3.54, "size": 330 },
        { "price": 3.55, "size": 900 }, { "price": 3.56, "size": 470 }, { "price": 3.57, "size": 200 },
        { "price": 3.58, "size": 1080 }, { "price": 3.59, "size": 620 }, { "price": 3.60, "size": 360 },
        { "price": 3.61, "size": 240 } ] } },
    { "afterMs": 3100, "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.52, "size": 1050, "direction": "BUY",    "ts": "2026-07-06T13:30:03.100Z" } ] },
    { "afterMs": 3500, "topic": "md.tape", "key": "US.AAPL", "payload": [
      { "symbol": "US.AAPL", "price": 3.51, "size": 90,  "direction": "SELL",    "ts": "2026-07-06T13:30:03.500Z" },
      { "symbol": "US.AAPL", "price": 3.51, "size": 40,  "direction": "NEUTRAL", "ts": "2026-07-06T13:30:03.540Z" },
      { "symbol": "US.AAPL", "price": 3.52, "size": 610, "direction": "BUY",     "ts": "2026-07-06T13:30:03.580Z" } ] }
  ]
}
```

- [ ] **Step 2: Document the fixture in `ui/mock-engine/run.ts`**

Extend the fixture-selection comment block (which already lists `chart-session`):

```ts
//   npm run mock-engine -- ladder-tape        (L2 book + tape + working orders, Plan 3)
```

- [ ] **Step 3: Dev-app verification checklist** (two terminals)

```bash
cd ui && npm run mock-engine -- ladder-tape    # terminal 1
cd ui && npm run dev                            # terminal 2
```

Open `http://localhost:5173/?workspace=trading` and verify:

- [ ] Ladder panel paints: red asks above / green bids below, PRICE/SIZE/CUM columns, depth bars widening with cumulative size, bold last price + spread in the center row.
- [ ] Amber order marks (outline + triangle + qty) on the 3.47 bid row and 3.55 ask row.
- [ ] Trades flash their price row and the flash decays (~0.4 s), and the center-row last price updates.
- [ ] Tape panel scrolls newest-on-top with green/red/gray rows and ET times.
- [ ] Set min size 500 → small prints disappear. (The debounced `SetConfig` fires — but the mock engine acks without persisting, and its `GetConfig` never returns a value, so the setting does **not** survive a browser reload in this dev flow. That's the mock's limitation, not a bug: persistence is covered by the WorkspaceStore unit tests and becomes real when the Go engine's config store lands.)
- [ ] Wheel up on the tape → amber 2 px strip + "⏸ paused — jump to live" pill; new prints do NOT move the frozen rows; wheel back down (or click the pill) → live again.
- [ ] Type `NVDA` in the green group's symbol box in the workspace header → ladder shows "waiting for depth…" for `US.NVDA` (honest — the fixture only carries AAPL), tape empties; type `AAPL` back → both repopulate.
- [ ] Toggle dark mode → both panels repaint on the dark palette immediately.
- [ ] Kill the mock engine mid-pause → reconnect overlay; restart it → tape resumes LIVE (pause dropped, no stale anchor), ladder rebuilds.
- [ ] Charts show their cold/waiting state (expected — this fixture has no `md.bars`; use `chart-session` for chart work).

- [ ] **Step 4: Commit**

```bash
git add ui/fixtures/ladder-tape.json ui/mock-engine/run.ts
git commit -m "feat(ui/mock): ladder-tape fixture — book/tape/working-orders replay for the trading workspace"
```

---

## Task 12: Integration sweep + plan close-out

**Files:**
- Modify: `ui/src/chrome/panels/registry.tsx` (comment only)
- Possibly touched: anything the sweep finds.

- [ ] **Step 1: Update the registry's plan ledger comment** — it currently says Plans 3–5 owe `ladder / tape / scanner / movers / news / account-bar / positions / open-orders / order-ticket`; rewrite to reflect that Plan 3 delivered ladder + tape and Plans 4–5 owe the rest.

- [ ] **Step 2: Full verification**

```bash
cd ui && npm run typecheck && npm run lint && npm test && npm run test:golden
```

Expected: all clean. `npm test` includes the golden tests (they live under the default vitest include); `test:golden` re-runs them alone to confirm the strict-diff path.

- [ ] **Step 3: Self-review against the roadmap scope** (Plan-1 roadmap item 3) — confirm each is true, fix anything that isn't:

- [ ] Ladder: 10 levels/side, price/size/cumulative, last-trade flash, working-order marks display-only, non-US no-depth state.
- [ ] Tape: over `TapeRing`, BUY/SELL/NEUTRAL coloring, min-size filter, pause-on-scroll with auto-resume on jump-to-live (and on scroll-back-to-live, and honestly on reconnect).
- [ ] Wickplot ports present and nothing else ported: `scrollAccumulate` (+ table tests), `depthFraction` idiom, `axisDecimals`, golden-harness shape upgraded to strict pixel-diff.
- [ ] No viewport/window classes — `y = rowIndex × rowHeight` everywhere.
- [ ] Every color in the new painters/chrome comes from `palette.ts`; goldens cover light + dark for all five mandated fixture states.
- [ ] High-frequency data never in React state; shared stores observed via `getRev()` cursors.

- [ ] **Step 4: Commit + hand off per the repo's finishing flow** (merge/PR decision belongs to Earl)

```bash
git add -A
git commit -m "chore(ui): Plan 3 integration sweep — registry ledger, close-out"
```

---

## Self-Review (run after drafting — completed 2026-07-05)

1. **Spec coverage:** ui-design §Panels L2-ladder line → Tasks 5/6/9 (levels, price/size/cumulative, flash, order marks, entitlement state); §Panels time&sales line → Tasks 7/8/10 (ring, coloring, filter, pause/resume); §Testing golden-image bullet → Task 4; roadmap item-3 wickplot ports → Tasks 1/2/4/5; roadmap golden fixture states → Tasks 6/8; §Error handling honesty rows → empty-book/no-entitlement/paused-strip/reconnect-resume behaviors in Tasks 5–10.
2. **Placeholder scan:** every code step carries complete code; the two "match the existing file's idioms" notes (palette.test.ts import style, TapeRing.test.ts tick helpers) point at concrete existing files the implementer reads, not TBDs.
3. **Type consistency:** `LadderPaintState`/`TapePaintState` fields, `TapeSource` methods, and panel imports were cross-checked across Tasks 5→6→9 and 7→8→10; `applyCanvasSize` and `scrollAccumulate` signatures match at every call site; registry topic names use only `TopicName` members that exist in `wire/contract.ts`.
