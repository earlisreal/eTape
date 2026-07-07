# Chart Drawing Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add hand-rolled chart drawing tools (horizontal line, horizontal ray, trendline, ray, rectangle, + transient price-range measure) to eTape charts, symbol-keyed and synced across panels/windows, persisted via the engine's generic config KV.

**Architecture:** Five new greenfield modules under `ui/src/render/chart/drawings/` (model, geometry, store, primitive, interaction) plus a `DrawingRail.tsx` chrome overlay. Rendering rides one `DrawingsPrimitive` per chart attached to the candle series (`zOrder: "top"`), following the existing `DiamondFillPrimitive`/`SessionShadingPrimitive` pattern. A shared `DrawingStore` (extends `PaintStore`) holds symbol-keyed drawings, syncs cross-window over `BroadcastChannel("etape.drawings")` mirroring `linkGroups.ts`, and persists per-symbol via debounced `SetConfig` mirroring `WorkspaceStore`. A plain `DrawingInteraction` class drives a pointer/key state machine, converting px↔`{timeMs, price}` through `ChartApiFacade`.

**Tech Stack:** TypeScript + React + Vite; `lightweight-charts@^5.2.0` primitives API; Vitest (`threads` pool default, `forks` for real-canvas files); `@testing-library/react` for chrome components.

## Global Constraints

- **UI-only. Zero engine changes.** No new wire messages. Drawings persist through the existing generic `GetConfig`/`SetConfig` KV commands, one key per symbol: `drawings.<SYMBOL>`.
- **High-frequency data never flows through React state.** Drawings render on the canvas via the primitive, coalesced to one repaint per rAF tick through the existing `Scheduler`. Active tool / magnet toggle are low-rate chrome — React state is allowed for those only.
- **`lightweight-charts` type imports are type-only, from the bare package** `"lightweight-charts"` (never a subpath). Reuse the repo convention `type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];` — do NOT import `fancy-canvas`'s `CanvasRenderingTarget2D` directly.
- **No per-drawing style in v1.** Each kind renders in one fixed style derived from the resolved `Palette` object (canvas painters receive a `Palette`, never read CSS vars). Theme switches restyle drawings automatically; the store holds no colors.
- **The measure tool is transient UI state** — never a `Drawing`, never stored, never synced.
- **Symbol-keyed, not group-keyed.** Any chart showing a symbol shows its drawings; group sync falls out of shared symbols.
- **Load never blocks rendering.** Malformed/absent persisted data → empty list; bad entries are dropped (count logged), never crash a chart. Save failure → toast with the engine's reason; in-memory drawings stay authoritative; dirty state retries on the next mutation/debounce tick.
- **Single-window mode fully works** — cross-window sync is layered on the store, never a dependency (no `BroadcastChannel` → the feature still works).
- **TDD, DRY, YAGNI, frequent commits.** One deliverable per task, each independently testable.
- **Bar time is `bucketStart` (ISO 8601 string).** Convert to epoch ms with `Date.parse(bar.bucketStart)`; LWC seconds = `Math.floor(Date.parse(bucketStart) / 1000)`. Anchor `timeMs` is epoch ms.
- **Commit messages: body only. Never add a `Co-Authored-By:` / "Generated with" / AI-attribution trailer** (Earl's global rule).
- **Canvas tests:** primitive tests use a fake `DrawTarget` + fake 2D context (no real canvas) and stay on the default `threads` pool. If any test mounts a real `<canvas>`, add its glob to `poolMatchGlobs` as `"forks"` (node-canvas/vitest fork-pool quirk) and run canvas smoke tests on the **main checkout, not a worktree**.

---

## Phasing & gating

This plan has two phases. **Phase 1 (Tasks 1–6)** is fully greenfield: it touches only new files under `drawings/`, plus additive edits to `ui/src/data/registry.ts` and `ui/test/fakes.ts`. It has **zero dependency on `ChartPanel.tsx` internals or Daylight Ledger tokens** and can execute now, even in parallel with the Daylight Ledger redesign execution.

**Phase 2 (Tasks 7–9)** wires the layer into `ChartPanel`/`App.tsx` and adds the `DrawingRail` styled with Daylight Ledger tokens. It is **gated on the redesign landing on `main`** (its `ui/src/chrome/cssVars.ts` + rewritten `ui/src/global.css` with `.btn`/`.ctl`/`.popover` classes and `--accent`/`--border-strong` vars must exist). Task 7 begins with an explicit **re-verify step** — confirm `ChartPanel.tsx`'s host div structure and `ChartApiFacade` shape have not drifted from this plan's assumptions before editing.

Recon confirmed the redesign does NOT rename/move `ChartPanel.tsx` or `ChartControls.tsx`, does NOT touch the facade/controller/primitive layer, and leaves the chart host div (`<div ref={hostRef} style={{flex:1, minHeight:0, position:"relative"}}/>`) unchanged — so Phase 2's coupling is limited to the rail's CSS classes and fitting under the new ledger header + controls row.

---

## File Structure

**New files (Phase 1):**
- `ui/src/render/chart/drawings/model.ts` — `DrawingKind`, `Anchor`, `Drawing` types + validation. One responsibility: the data shape and its load-time validator.
- `ui/src/render/chart/drawings/model.test.ts`
- `ui/src/render/chart/drawings/geometry.ts` — pure math: time↔fractional-logical interpolation/extrapolation, `timeframeToMs`, hit-test primitives, magnet snapping, ray extension. No LWC, no DOM.
- `ui/src/render/chart/drawings/geometry.test.ts`
- `ui/src/render/chart/drawings/store.ts` — `DrawingStore` (extends `PaintStore`), `DrawingBus`/`BroadcastChannelDrawingBus`, `DrawingMsg`, persistence + echo-guard + lazy load.
- `ui/src/render/chart/drawings/store.test.ts`
- `ui/src/render/chart/drawings/primitive.ts` — `DrawingsPrimitive` (`ISeriesPrimitive<Time>`) renderer.
- `ui/src/render/chart/drawings/primitive.test.ts`
- `ui/src/render/chart/drawings/interaction.ts` — `DrawingInteraction` pointer/key state machine + `DrawingFacade` subset interface + `Tool` type.
- `ui/src/render/chart/drawings/interaction.test.ts`

**New files (Phase 2):**
- `ui/src/chrome/panels/DrawingRail.tsx` — React overlay rail.
- `ui/src/chrome/panels/DrawingRail.test.tsx`

**Modified files:**
- `ui/src/data/registry.ts` — add `drawings: DrawingStore` to `Stores` + `makeStores()` (Phase 1, Task 4).
- `ui/test/fakes.ts` — add `FakeDrawingBus` + `FakeDrawingBusHub` (Phase 1, Task 5).
- `ui/src/render/chart/ChartApiFacade.ts` — add `logicalToCoordinate`, `coordinateToLogical`, `coordinateToPrice`, `setPanZoomEnabled` (Phase 2, Task 7).
- `ui/src/render/chart/ChartController.test.ts` — extend `fakeFacade()` with the 4 new no-op methods (Phase 2, Task 7).
- `ui/src/chrome/panels/ChartPanel.tsx` — attach primitive in `makeFacade`, wire paint-loop `drawingsRev`, `ensureLoaded` on symbol apply, instantiate `DrawingInteraction`, render `DrawingRail` (Phase 2, Tasks 7 & 9).
- `ui/src/chrome/panels/ChartPanel.test.tsx` — extend the mocked chart with the new coordinate methods; add the two-panel-one-store integration test (Phase 2, Task 7).
- `ui/src/App.tsx` — `stores.drawings.connect(...)` + a `DrawingsToastBridge` inside `ToastProvider` (Phase 2, Task 7).

---

## Phase 1 — Greenfield modules

### Task 1: Data model + validation

**Files:**
- Create: `ui/src/render/chart/drawings/model.ts`
- Test: `ui/src/render/chart/drawings/model.test.ts`

**Interfaces:**
- Consumes: nothing (leaf module).
- Produces:
  - `type DrawingKind = "hline" | "hray" | "trendline" | "ray" | "rect"`
  - `interface Anchor { timeMs: number; price: number }`
  - `interface Drawing { id: string; symbol: string; kind: DrawingKind; anchors: Anchor[]; createdMs: number; updatedMs: number }`
  - `function anchorCount(kind: DrawingKind): 1 | 2`
  - `function isValidDrawing(x: unknown): x is Drawing`
  - `function validateDrawings(raw: unknown): Drawing[]` (drops invalid entries, `console.warn`s the dropped count)

- [ ] **Step 1: Write the failing test**

Create `ui/src/render/chart/drawings/model.test.ts`:

```ts
import { describe, it, expect, vi } from "vitest";
import { anchorCount, isValidDrawing, validateDrawings, type Drawing } from "./model";

const hline: Drawing = { id: "a", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 1000, price: 10 }], createdMs: 1, updatedMs: 1 };
const rect: Drawing = { id: "b", symbol: "US.AAPL", kind: "rect", anchors: [{ timeMs: 1000, price: 10 }, { timeMs: 2000, price: 20 }], createdMs: 1, updatedMs: 1 };

describe("anchorCount", () => {
  it("is 1 for hline/hray and 2 for trendline/ray/rect", () => {
    expect(anchorCount("hline")).toBe(1);
    expect(anchorCount("hray")).toBe(1);
    expect(anchorCount("trendline")).toBe(2);
    expect(anchorCount("ray")).toBe(2);
    expect(anchorCount("rect")).toBe(2);
  });
});

describe("isValidDrawing", () => {
  it("accepts well-formed drawings", () => {
    expect(isValidDrawing(hline)).toBe(true);
    expect(isValidDrawing(rect)).toBe(true);
  });
  it("rejects wrong anchor count for the kind", () => {
    expect(isValidDrawing({ ...rect, anchors: [{ timeMs: 1, price: 2 }] })).toBe(false);
    expect(isValidDrawing({ ...hline, anchors: [] })).toBe(false);
  });
  it("rejects unknown kinds, non-finite numbers, and missing fields", () => {
    expect(isValidDrawing({ ...hline, kind: "fib" })).toBe(false);
    expect(isValidDrawing({ ...hline, anchors: [{ timeMs: NaN, price: 10 }] })).toBe(false);
    expect(isValidDrawing({ ...hline, id: 5 })).toBe(false);
    expect(isValidDrawing(null)).toBe(false);
    expect(isValidDrawing("x")).toBe(false);
  });
});

describe("validateDrawings", () => {
  it("returns [] for non-arrays", () => {
    expect(validateDrawings(null)).toEqual([]);
    expect(validateDrawings({})).toEqual([]);
    expect(validateDrawings(undefined)).toEqual([]);
  });
  it("keeps valid entries and drops invalid ones, warning the count", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const out = validateDrawings([hline, { junk: true }, rect, { ...hline, kind: "nope" }]);
    expect(out.map((d) => d.id)).toEqual(["a", "b"]);
    expect(warn).toHaveBeenCalledOnce();
    warn.mockRestore();
  });
  it("does not warn when nothing is dropped", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    validateDrawings([hline, rect]);
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/drawings/model.test.ts`
Expected: FAIL — cannot resolve `./model`.

- [ ] **Step 3: Write the implementation**

Create `ui/src/render/chart/drawings/model.ts`:

```ts
// Drawing data model + load-time validation. Pure — no LWC, no DOM.

export type DrawingKind = "hline" | "hray" | "trendline" | "ray" | "rect";

export interface Anchor {
  timeMs: number; // epoch ms (a bar's time on the chart it was drawn)
  price: number;  // raw price
}

export interface Drawing {
  id: string;       // crypto.randomUUID()
  symbol: string;   // "US.AAPL"
  kind: DrawingKind;
  anchors: Anchor[]; // hline/hray: 1, trendline/ray/rect: 2
  createdMs: number;
  updatedMs: number;
}

const KINDS: ReadonlySet<string> = new Set<DrawingKind>(["hline", "hray", "trendline", "ray", "rect"]);

export function anchorCount(kind: DrawingKind): 1 | 2 {
  return kind === "hline" || kind === "hray" ? 1 : 2;
}

function isFiniteNumber(x: unknown): x is number {
  return typeof x === "number" && Number.isFinite(x);
}

function isAnchor(x: unknown): x is Anchor {
  return typeof x === "object" && x !== null
    && isFiniteNumber((x as Anchor).timeMs) && isFiniteNumber((x as Anchor).price);
}

export function isValidDrawing(x: unknown): x is Drawing {
  if (typeof x !== "object" || x === null) return false;
  const d = x as Record<string, unknown>;
  if (typeof d.id !== "string" || typeof d.symbol !== "string") return false;
  if (typeof d.kind !== "string" || !KINDS.has(d.kind)) return false;
  if (!isFiniteNumber(d.createdMs) || !isFiniteNumber(d.updatedMs)) return false;
  if (!Array.isArray(d.anchors)) return false;
  if (d.anchors.length !== anchorCount(d.kind as DrawingKind)) return false;
  return d.anchors.every(isAnchor);
}

// Load-time gate: drops malformed entries so a corrupt config never crashes a chart.
export function validateDrawings(raw: unknown): Drawing[] {
  if (!Array.isArray(raw)) return [];
  const out = raw.filter(isValidDrawing);
  const dropped = raw.length - out.length;
  if (dropped > 0) console.warn(`[drawings] dropped ${dropped} malformed drawing(s) on load`);
  return out;
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/drawings/model.test.ts`
Expected: PASS (all cases).

- [ ] **Step 5: Commit**

```bash
cd ui && npx tsc --noEmit && cd ..
git add ui/src/render/chart/drawings/model.ts ui/src/render/chart/drawings/model.test.ts
git commit -m "feat(ui/chart): drawing data model + load-time validation"
```

---

### Task 2: Geometry (pure math)

**Files:**
- Create: `ui/src/render/chart/drawings/geometry.ts`
- Test: `ui/src/render/chart/drawings/geometry.test.ts`

**Interfaces:**
- Consumes: `DrawingKind` from `./model`; `Timeframe` from `../barBucket`.
- Produces:
  - `type Px = { x: number; y: number }`
  - `type Hit = { type: "handle"; index: number } | { type: "body" } | null`
  - `function timeframeToMs(tf: Timeframe): number`
  - `function timeToLogical(timeMs: number, barsMs: readonly number[], timeframeMs: number): number`
  - `function distToSegment(px, py, ax, ay, bx, by: number): number`
  - `function extendToEdge(p0: Px, p1: Px, width: number): Px`
  - `function snapToLevels(cursorY: number, levels: readonly { price: number; y: number }[], tolPx: number): number | null`
  - `function hitTest(kind: DrawingKind, pts: readonly (Px | null)[], cursor: Px, width: number, seg?: number, handle?: number): Hit`

- [ ] **Step 1: Write the failing test**

Create `ui/src/render/chart/drawings/geometry.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { timeframeToMs, timeToLogical, distToSegment, extendToEdge, snapToLevels, hitTest } from "./geometry";

describe("timeframeToMs", () => {
  it("maps intraday timeframes to milliseconds", () => {
    expect(timeframeToMs("10s")).toBe(10_000);
    expect(timeframeToMs("1m")).toBe(60_000);
    expect(timeframeToMs("5m")).toBe(300_000);
    expect(timeframeToMs("60m")).toBe(3_600_000);
    expect(timeframeToMs("D")).toBe(86_400_000);
  });
});

describe("timeToLogical", () => {
  // three 1m bars at t=0, 60000, 120000 → logical 0,1,2
  const bars = [0, 60_000, 120_000];
  const tf = 60_000;
  it("returns integer logical at a bar time", () => {
    expect(timeToLogical(0, bars, tf)).toBe(0);
    expect(timeToLogical(120_000, bars, tf)).toBe(2);
  });
  it("interpolates a fractional logical between adjacent bars", () => {
    expect(timeToLogical(30_000, bars, tf)).toBeCloseTo(0.5, 6);
    expect(timeToLogical(90_000, bars, tf)).toBeCloseTo(1.5, 6);
  });
  it("extrapolates right by the timeframe beyond the last bar", () => {
    expect(timeToLogical(180_000, bars, tf)).toBeCloseTo(3, 6);
  });
  it("extrapolates left (negative) before the first bar", () => {
    expect(timeToLogical(-60_000, bars, tf)).toBeCloseTo(-1, 6);
  });
  it("returns 0 for an empty bar array", () => {
    expect(timeToLogical(1234, [], tf)).toBe(0);
  });
  it("interpolates across an uneven (session-gap) bar spacing", () => {
    // bars 0 and 600000 are adjacent logicals 0,1 despite a 10x gap
    expect(timeToLogical(300_000, [0, 600_000], 60_000)).toBeCloseTo(0.5, 6);
  });
});

describe("distToSegment", () => {
  it("is the perpendicular distance for an interior projection", () => {
    expect(distToSegment(5, 3, 0, 0, 10, 0)).toBeCloseTo(3, 6);
  });
  it("clamps to the nearest endpoint outside the segment", () => {
    expect(distToSegment(-4, 0, 0, 0, 10, 0)).toBeCloseTo(4, 6);
  });
  it("handles a zero-length segment", () => {
    expect(distToSegment(3, 4, 0, 0, 0, 0)).toBeCloseTo(5, 6);
  });
});

describe("extendToEdge", () => {
  it("extends a rightward ray to the right edge", () => {
    expect(extendToEdge({ x: 10, y: 10 }, { x: 20, y: 20 }, 100)).toEqual({ x: 100, y: 100 });
  });
  it("extends a leftward ray to the left edge (x=0)", () => {
    expect(extendToEdge({ x: 20, y: 20 }, { x: 10, y: 10 }, 100)).toEqual({ x: 0, y: 0 });
  });
});

describe("snapToLevels", () => {
  const levels = [{ price: 10, y: 100 }, { price: 11, y: 90 }, { price: 12, y: 50 }];
  it("snaps to the nearest level within tolerance", () => {
    expect(snapToLevels(96, levels, 6)).toBe(11);
  });
  it("returns null when no level is within tolerance", () => {
    expect(snapToLevels(70, levels, 6)).toBeNull();
  });
  it("prefers the closest when two are within tolerance", () => {
    expect(snapToLevels(95, levels, 6)).toBe(11); // 90 is 5 away, 100 is 5 away — tie → first-min (11)
  });
});

describe("hitTest", () => {
  const cursor = { x: 50, y: 50 };
  it("prefers a handle over the body", () => {
    const pts = [{ x: 50, y: 50 }, { x: 200, y: 200 }];
    expect(hitTest("trendline", pts, cursor, 400)).toEqual({ type: "handle", index: 0 });
  });
  it("hits an hline body by y-distance regardless of x", () => {
    expect(hitTest("hline", [{ x: 9999, y: 52 }], cursor, 400)).toEqual({ type: "body" });
    expect(hitTest("hline", [{ x: 9999, y: 80 }], cursor, 400)).toBeNull();
  });
  it("hits an hray body only to the right of its anchor", () => {
    expect(hitTest("hray", [{ x: 40, y: 51 }], cursor, 400)).toEqual({ type: "body" });
    expect(hitTest("hray", [{ x: 80, y: 51 }], cursor, 400)).toBeNull();
  });
  it("hits a trendline body near the segment", () => {
    expect(hitTest("trendline", [{ x: 0, y: 48 }, { x: 100, y: 48 }], cursor, 400)).toEqual({ type: "body" });
  });
  it("hits a ray body along its rightward extension", () => {
    // ray from (0,0) through (10,10): at x=50 the ray is at y=50
    expect(hitTest("ray", [{ x: 0, y: 0 }, { x: 10, y: 10 }], cursor, 400)).toEqual({ type: "body" });
  });
  it("hits a rect body near an edge but not the interior", () => {
    const pts = [{ x: 0, y: 0 }, { x: 100, y: 100 }];
    expect(hitTest("rect", pts, { x: 50, y: 2 }, 400)).toEqual({ type: "body" }); // near top edge
    expect(hitTest("rect", pts, { x: 50, y: 50 }, 400)).toBeNull();               // interior
  });
  it("returns null when the primary anchor is off-screen (null)", () => {
    expect(hitTest("hline", [null], cursor, 400)).toBeNull();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/drawings/geometry.test.ts`
Expected: FAIL — cannot resolve `./geometry`.

- [ ] **Step 3: Write the implementation**

Create `ui/src/render/chart/drawings/geometry.ts`:

```ts
import type { DrawingKind } from "./model";
import type { Timeframe } from "../barBucket";

export type Px = { x: number; y: number };
export type Hit = { type: "handle"; index: number } | { type: "body" } | null;

// Nominal per-bar time step, used only to EXTRAPOLATE beyond loaded data
// (interpolation between two loaded bars never needs it). D/W/M are coarse
// approximations — acceptable because the trading workspace is 1m + 10s.
export function timeframeToMs(tf: Timeframe): number {
  switch (tf) {
    case "10s": return 10_000;
    case "1m": return 60_000;
    case "5m": return 300_000;
    case "15m": return 900_000;
    case "30m": return 1_800_000;
    case "60m": return 3_600_000;
    case "D": return 86_400_000;
    case "W": return 604_800_000;
    case "M": return 2_592_000_000;
  }
}

// Map an anchor's epoch-ms to a fractional LWC logical index on THIS chart's
// bar array. bar[i] sits at logical i; between bars we interpolate; beyond the
// ends we extrapolate by timeframeMs (rays keep pointing into the future; an
// anchor before loaded history keeps the line's true slope). barsMs must be
// ascending. Returns 0 for an empty array (primitive skips drawing with no bars).
export function timeToLogical(timeMs: number, barsMs: readonly number[], timeframeMs: number): number {
  const n = barsMs.length;
  if (n === 0) return 0;
  const first = barsMs[0];
  const last = barsMs[n - 1];
  if (timeMs <= first) return -(first - timeMs) / timeframeMs;
  if (timeMs >= last) return (n - 1) + (timeMs - last) / timeframeMs;
  // largest i with barsMs[i] <= timeMs
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (barsMs[mid] <= timeMs) lo = mid;
    else hi = mid - 1;
  }
  const span = barsMs[lo + 1] - barsMs[lo];
  const frac = span > 0 ? (timeMs - barsMs[lo]) / span : 0;
  return lo + frac;
}

export function distToSegment(px: number, py: number, ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax;
  const dy = by - ay;
  const len2 = dx * dx + dy * dy;
  if (len2 === 0) return Math.hypot(px - ax, py - ay);
  let t = ((px - ax) * dx + (py - ay) * dy) / len2;
  t = Math.max(0, Math.min(1, t));
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
}

// Extend the ray p0→p1 to the viewport edge in its own x-direction. Vertical
// rays extend far along y. Used by both hit-testing and the renderer.
export function extendToEdge(p0: Px, p1: Px, width: number): Px {
  const dx = p1.x - p0.x;
  const dy = p1.y - p0.y;
  if (dx === 0) return { x: p1.x, y: p1.y + (dy >= 0 ? 1 : -1) * 1e6 };
  const targetX = dx > 0 ? width : 0;
  const t = (targetX - p0.x) / dx;
  return { x: targetX, y: p0.y + t * dy };
}

// Magnet: nearest level whose y is within tolPx of the cursor, else null.
export function snapToLevels(cursorY: number, levels: readonly { price: number; y: number }[], tolPx: number): number | null {
  let bestPrice: number | null = null;
  let bestDist = Infinity;
  for (const l of levels) {
    const d = Math.abs(cursorY - l.y);
    if (d <= tolPx && d < bestDist) {
      bestDist = d;
      bestPrice = l.price;
    }
  }
  return bestPrice;
}

// Pixel-space hit test. `pts` are the projected pixel positions of the drawing's
// anchors (null = off-screen). Handles win over the body. `width` is the pane
// width for horizontal/ray extension.
export function hitTest(kind: DrawingKind, pts: readonly (Px | null)[], cursor: Px, width: number, seg = 5, handle = 6): Hit {
  for (let i = 0; i < pts.length; i++) {
    const p = pts[i];
    if (p && Math.hypot(cursor.x - p.x, cursor.y - p.y) <= handle) return { type: "handle", index: i };
  }
  const p0 = pts[0];
  if (!p0) return null;
  switch (kind) {
    case "hline":
      return Math.abs(cursor.y - p0.y) <= seg ? { type: "body" } : null;
    case "hray":
      return Math.abs(cursor.y - p0.y) <= seg && cursor.x >= p0.x - seg ? { type: "body" } : null;
    case "trendline": {
      const p1 = pts[1];
      return p1 && distToSegment(cursor.x, cursor.y, p0.x, p0.y, p1.x, p1.y) <= seg ? { type: "body" } : null;
    }
    case "ray": {
      const p1 = pts[1];
      if (!p1) return null;
      const far = extendToEdge(p0, p1, width);
      return distToSegment(cursor.x, cursor.y, p0.x, p0.y, far.x, far.y) <= seg ? { type: "body" } : null;
    }
    case "rect": {
      const p1 = pts[1];
      if (!p1) return null;
      const edges: [number, number, number, number][] = [
        [p0.x, p0.y, p1.x, p0.y],
        [p1.x, p0.y, p1.x, p1.y],
        [p1.x, p1.y, p0.x, p1.y],
        [p0.x, p1.y, p0.x, p0.y],
      ];
      for (const [ax, ay, bx, by] of edges) {
        if (distToSegment(cursor.x, cursor.y, ax, ay, bx, by) <= seg) return { type: "body" };
      }
      return null;
    }
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/drawings/geometry.test.ts`
Expected: PASS (all cases). If the `snapToLevels` tie case fails, confirm the "first strictly-closest wins" semantics match the test (`d < bestDist`, not `<=`).

- [ ] **Step 5: Commit**

```bash
cd ui && npx tsc --noEmit && cd ..
git add ui/src/render/chart/drawings/geometry.ts ui/src/render/chart/drawings/geometry.test.ts
git commit -m "feat(ui/chart): pure geometry for drawing tools (interpolation, hit-test, magnet)"
```

---

### Task 3: DrawingStore core (in-memory + revision) + registry wiring

In-memory store only — no bus, no persistence yet (Task 4 layers those on). This delivers a working, shared, revision-tracked store registered in `makeStores()`.

**Files:**
- Create: `ui/src/render/chart/drawings/store.ts`
- Test: `ui/src/render/chart/drawings/store.test.ts`
- Modify: `ui/src/data/registry.ts` (add `drawings` field + construction)

**Interfaces:**
- Consumes: `PaintStore` from `../../../data/store`; `Drawing` from `./model`.
- Produces:
  - `class DrawingStore extends PaintStore` with `forSymbol(sym: string): Drawing[]`, `upsert(d: Drawing): void`, `remove(id: string): void`, `clearSymbol(sym: string): void`, `getRev(): number` (inherited).
  - `Stores.drawings: DrawingStore` (registry).

- [ ] **Step 1: Write the failing test**

Create `ui/src/render/chart/drawings/store.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { DrawingStore } from "./store";
import type { Drawing } from "./model";

const mk = (id: string, symbol: string): Drawing => ({
  id, symbol, kind: "hline", anchors: [{ timeMs: 1000, price: 10 }], createdMs: 1, updatedMs: 1,
});

describe("DrawingStore core", () => {
  it("upsert adds a drawing under its symbol and bumps the revision", () => {
    const s = new DrawingStore();
    const r0 = s.getRev();
    s.upsert(mk("a", "US.AAPL"));
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    expect(s.getRev()).toBeGreaterThan(r0);
  });

  it("upsert replaces an existing id in place (no duplicate)", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert({ ...mk("a", "US.AAPL"), anchors: [{ timeMs: 1000, price: 99 }] });
    const arr = s.forSymbol("US.AAPL");
    expect(arr).toHaveLength(1);
    expect(arr[0].anchors[0].price).toBe(99);
  });

  it("forSymbol returns [] for an unknown symbol and isolates symbols", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.TSLA"));
    expect(s.forSymbol("US.NVDA")).toEqual([]);
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    expect(s.forSymbol("US.TSLA").map((d) => d.id)).toEqual(["b"]);
  });

  it("remove deletes by id (looking up its symbol) and bumps the revision", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.AAPL"));
    const r0 = s.getRev();
    s.remove("a");
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["b"]);
    expect(s.getRev()).toBeGreaterThan(r0);
  });

  it("remove of an unknown id is a no-op and does not bump the revision", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    const r0 = s.getRev();
    s.remove("zzz");
    expect(s.getRev()).toBe(r0);
    expect(s.forSymbol("US.AAPL")).toHaveLength(1);
  });

  it("clearSymbol empties one symbol only and bumps the revision", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.TSLA"));
    const r0 = s.getRev();
    s.clearSymbol("US.AAPL");
    expect(s.forSymbol("US.AAPL")).toEqual([]);
    expect(s.forSymbol("US.TSLA").map((d) => d.id)).toEqual(["b"]);
    expect(s.getRev()).toBeGreaterThan(r0);
  });

  it("preserves insertion order within a symbol", () => {
    const s = new DrawingStore();
    s.upsert(mk("a", "US.AAPL"));
    s.upsert(mk("b", "US.AAPL"));
    s.upsert(mk("c", "US.AAPL"));
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a", "b", "c"]);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/drawings/store.test.ts`
Expected: FAIL — cannot resolve `./store`.

- [ ] **Step 3: Write the core implementation**

Create `ui/src/render/chart/drawings/store.ts`. (Task 4 extends this same file with bus + persistence — leave room for those additions; do not add them yet.)

```ts
import { PaintStore } from "../../../data/store";
import type { Drawing } from "./model";

// Symbol-keyed store of chart drawings, shared across every panel in a window
// (one instance from makeStores()). Extends PaintStore so N chart panels each
// track their own last-seen revision via getRev() without starving each other.
//
// Primary index is byId (so remove(id) is O(1) and can find the owning symbol);
// bySymbol tracks id membership + insertion order per symbol.
export class DrawingStore extends PaintStore {
  private readonly byId = new Map<string, Drawing>();
  private readonly bySymbol = new Map<string, Set<string>>();

  forSymbol(symbol: string): Drawing[] {
    const ids = this.bySymbol.get(symbol);
    if (!ids) return [];
    const out: Drawing[] = [];
    for (const id of ids) {
      const d = this.byId.get(id);
      if (d) out.push(d);
    }
    return out;
  }

  upsert(d: Drawing): void {
    this.setLocal(d);
  }

  remove(id: string): void {
    if (this.deleteLocal(id)) this.markDirty();
  }

  clearSymbol(symbol: string): void {
    this.clearLocal(symbol);
    this.markDirty();
  }

  // --- internal mutation primitives (Task 4 reuses these for remote apply) ---

  protected setLocal(d: Drawing): void {
    this.byId.set(d.id, d);
    let ids = this.bySymbol.get(d.symbol);
    if (!ids) {
      ids = new Set<string>();
      this.bySymbol.set(d.symbol, ids);
    }
    ids.add(d.id);
    this.markDirty();
  }

  protected deleteLocal(id: string): boolean {
    const d = this.byId.get(id);
    if (!d) return false;
    this.byId.delete(id);
    this.bySymbol.get(d.symbol)?.delete(id);
    return true;
  }

  protected clearLocal(symbol: string): void {
    const ids = this.bySymbol.get(symbol);
    if (!ids) return;
    for (const id of ids) this.byId.delete(id);
    this.bySymbol.delete(symbol);
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/drawings/store.test.ts`
Expected: PASS.

- [ ] **Step 5: Register the store in `makeStores()`**

Modify `ui/src/data/registry.ts`. Add the import near the other store imports:

```ts
import { DrawingStore } from "../render/chart/drawings/store";
```

Add the field to the `Stores` interface (after `fills`):

```ts
  fills: FillStore;
  drawings: DrawingStore;
```

Add the construction to the `makeStores()` return object (after `fills`):

```ts
    fills: new FillStore(),
    drawings: new DrawingStore(),
```

Note: `routeToStore` does NOT get a case for drawings — drawings are loaded on demand via `GetConfig`/`SetConfig` (Task 4), not pushed over topics (mirrors how `config` is handled outside `routeToStore`).

- [ ] **Step 6: Verify the whole UI still type-checks and the registry test passes**

Run: `cd ui && npx tsc --noEmit && npx vitest run src/data/registry.test.ts`
Expected: PASS, no type errors.

- [ ] **Step 7: Commit**

```bash
git add ui/src/render/chart/drawings/store.ts ui/src/render/chart/drawings/store.test.ts ui/src/data/registry.ts
git commit -m "feat(ui/chart): DrawingStore core (symbol-keyed, revision-tracked) + registry"
```

---

### Task 4: DrawingStore sync (BroadcastChannel) + persistence (debounced KV) + lazy load

Layers cross-window sync, debounced per-symbol persistence, and lazy load onto the core store — mirroring `linkGroups.ts` (echo-guard) and `WorkspaceStore` (debounce). The store is fully functional in-memory when disconnected (tests, single-window without config).

**Files:**
- Modify: `ui/src/render/chart/drawings/store.ts`
- Modify: `ui/src/render/chart/drawings/store.test.ts`
- Modify: `ui/test/fakes.ts` (add `FakeDrawingBus` + `FakeDrawingBusHub`)

**Interfaces:**
- Consumes: `validateDrawings` from `./model`.
- Produces:
  - `interface DrawingMsg { op: "upsert" | "remove" | "clear"; symbol: string; drawing?: Drawing; id?: string }`
  - `interface DrawingBus { post(msg: DrawingMsg): void; onMessage(cb: (msg: DrawingMsg) => void): () => void; close(): void }`
  - `class BroadcastChannelDrawingBus implements DrawingBus` (wraps `new BroadcastChannel("etape.drawings")`)
  - `DrawingStore.connect(deps: { commands: CommandClient; bus: DrawingBus; onError: (reason: string) => void }): () => void`
  - `DrawingStore.ensureLoaded(symbol: string): void`
  - `DrawingStore.flush(): Promise<void>` (synchronous-drain, also used by tests)

- [ ] **Step 1: Add the fake bus to `ui/test/fakes.ts`**

Append to `ui/test/fakes.ts` (mirrors the existing `FakeBus`/`FakeBusHub`):

```ts
import type { DrawingBus, DrawingMsg } from "../src/render/chart/drawings/store";

// Shared in-memory bus simulating BroadcastChannel("etape.drawings") across "windows".
export class FakeDrawingBusHub {
  private buses = new Set<FakeDrawingBus>();
  join(b: FakeDrawingBus): void { this.buses.add(b); }
  leave(b: FakeDrawingBus): void { this.buses.delete(b); }
  broadcast(from: FakeDrawingBus, msg: DrawingMsg): void {
    this.buses.forEach((b) => { if (b !== from) b.deliver(msg); });
  }
}
export class FakeDrawingBus implements DrawingBus {
  private cb: ((msg: DrawingMsg) => void) | null = null;
  constructor(private hub: FakeDrawingBusHub) { hub.join(this); }
  post(msg: DrawingMsg): void { this.hub.broadcast(this, msg); }
  onMessage(cb: (msg: DrawingMsg) => void): () => void { this.cb = cb; return () => { this.cb = null; }; }
  deliver(msg: DrawingMsg): void { this.cb?.(msg); }
  close(): void { this.hub.leave(this); }
}
```

- [ ] **Step 2: Write the failing tests**

Append to `ui/src/render/chart/drawings/store.test.ts` (add the imports at the top of the file):

```ts
import { vi } from "vitest";
import { FakeDrawingBus, FakeDrawingBusHub } from "../../../../test/fakes";

interface FakeAck { status: string; value?: unknown; reason?: string }
function fakeCommands(overrides?: Partial<{ get: FakeAck; set: FakeAck }>) {
  const calls: { name: string; args: any }[] = [];
  const sendCommand = vi.fn(async (name: string, args: unknown): Promise<FakeAck> => {
    calls.push({ name, args });
    if (name === "GetConfig") return overrides?.get ?? { status: "accepted", value: [] };
    return overrides?.set ?? { status: "accepted" };
  });
  return { sendCommand, calls };
}

describe("DrawingStore sync + persistence", () => {
  const mk = (id: string, symbol: string): Drawing => ({
    id, symbol, kind: "hline", anchors: [{ timeMs: 1000, price: 10 }], createdMs: 1, updatedMs: 1,
  });

  it("local upsert publishes on the bus and schedules a persist", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands();
    const s = new DrawingStore(0); // debounceMs 0
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.upsert(mk("a", "US.AAPL"));
    await s.flush();
    const set = cmd.calls.find((c) => c.name === "SetConfig");
    expect(set?.args.key).toBe("drawings.US.AAPL");
    expect((set?.args.value as Drawing[]).map((d) => d.id)).toEqual(["a"]);
  });

  it("propagates a local upsert to another window without an echo storm", async () => {
    const hub = new FakeDrawingBusHub();
    const cmdA = fakeCommands();
    const cmdB = fakeCommands();
    const a = new DrawingStore(0);
    const b = new DrawingStore(0);
    a.connect({ commands: cmdA, bus: new FakeDrawingBus(hub), onError: () => {} });
    b.connect({ commands: cmdB, bus: new FakeDrawingBus(hub), onError: () => {} });
    a.upsert(mk("a", "US.AAPL"));
    // B received the drawing…
    expect(b.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    // …but B must NOT re-persist a remotely-applied drawing (single-writer).
    await b.flush();
    expect(cmdB.calls.some((c) => c.name === "SetConfig")).toBe(false);
  });

  it("remote remove/clear apply locally without re-publishing", async () => {
    const hub = new FakeDrawingBusHub();
    const a = new DrawingStore(0);
    const b = new DrawingStore(0);
    a.connect({ commands: fakeCommands(), bus: new FakeDrawingBus(hub), onError: () => {} });
    b.connect({ commands: fakeCommands(), bus: new FakeDrawingBus(hub), onError: () => {} });
    a.upsert(mk("a", "US.AAPL"));
    a.remove("a");
    expect(b.forSymbol("US.AAPL")).toEqual([]);
    a.upsert(mk("c", "US.AAPL"));
    a.clearSymbol("US.AAPL");
    expect(b.forSymbol("US.AAPL")).toEqual([]);
  });

  it("ensureLoaded fetches once per symbol per session and validates", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands({ get: { status: "accepted", value: [mk("x", "US.AAPL"), { junk: true }] } });
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.ensureLoaded("US.AAPL");
    s.ensureLoaded("US.AAPL"); // second call must not refetch
    await Promise.resolve(); await Promise.resolve();
    expect(cmd.calls.filter((c) => c.name === "GetConfig")).toHaveLength(1);
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["x"]); // junk dropped
  });

  it("ensureLoaded is a no-op (empty list) when the store is not connected", async () => {
    const s = new DrawingStore(0);
    s.ensureLoaded("US.AAPL");
    await Promise.resolve();
    expect(s.forSymbol("US.AAPL")).toEqual([]);
  });

  it("a loaded drawing is not re-persisted", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands({ get: { status: "accepted", value: [mk("x", "US.AAPL")] } });
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.ensureLoaded("US.AAPL");
    await Promise.resolve(); await Promise.resolve();
    await s.flush();
    expect(cmd.calls.some((c) => c.name === "SetConfig")).toBe(false);
  });

  it("clearSymbol persists an empty array (so a restart does not reload)", async () => {
    const hub = new FakeDrawingBusHub();
    const cmd = fakeCommands();
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError: () => {} });
    s.upsert(mk("a", "US.AAPL"));
    await s.flush();
    s.clearSymbol("US.AAPL");
    await s.flush();
    const last = [...cmd.calls].reverse().find((c) => c.name === "SetConfig");
    expect(last?.args.key).toBe("drawings.US.AAPL");
    expect(last?.args.value).toEqual([]);
  });

  it("surfaces a save failure via onError and keeps the symbol dirty for retry", async () => {
    const hub = new FakeDrawingBusHub();
    const onError = vi.fn();
    const cmd = fakeCommands({ set: { status: "blocked", reason: "disk full" } });
    const s = new DrawingStore(0);
    s.connect({ commands: cmd, bus: new FakeDrawingBus(hub), onError });
    s.upsert(mk("a", "US.AAPL"));
    await s.flush();
    expect(onError).toHaveBeenCalledWith("disk full");
    // still dirty → a subsequent flush retries the write
    const before = cmd.calls.filter((c) => c.name === "SetConfig").length;
    await s.flush();
    expect(cmd.calls.filter((c) => c.name === "SetConfig").length).toBeGreaterThan(before);
  });

  it("disconnected upsert still works in-memory and never persists", async () => {
    const s = new DrawingStore(0);
    s.upsert(mk("a", "US.AAPL"));
    expect(s.forSymbol("US.AAPL").map((d) => d.id)).toEqual(["a"]);
    await s.flush(); // no throw, no deps
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd ui && npx vitest run src/render/chart/drawings/store.test.ts`
Expected: FAIL — `connect`/`ensureLoaded`/`flush`/`DrawingStore(0)` not defined.

- [ ] **Step 4: Extend `store.ts` with bus, connect, persistence, and lazy load**

Update `ui/src/render/chart/drawings/store.ts`. Add imports + types at the top:

```ts
import { validateDrawings } from "./model";

export interface DrawingMsg {
  op: "upsert" | "remove" | "clear";
  symbol: string;
  drawing?: Drawing; // op "upsert"
  id?: string;       // op "remove"
}

export interface DrawingBus {
  post(msg: DrawingMsg): void;
  onMessage(cb: (msg: DrawingMsg) => void): () => void;
  close(): void;
}

export class BroadcastChannelDrawingBus implements DrawingBus {
  private ch = new BroadcastChannel("etape.drawings");
  post(msg: DrawingMsg): void { this.ch.postMessage(msg); }
  onMessage(cb: (msg: DrawingMsg) => void): () => void {
    const handler = (e: MessageEvent) => cb(e.data as DrawingMsg);
    this.ch.addEventListener("message", handler);
    return () => this.ch.removeEventListener("message", handler);
  }
  close(): void { this.ch.close(); }
}

// Minimal command surface (mirrors WorkspaceStore's local CommandClient).
interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }>;
}

interface Deps { commands: CommandClient; bus: DrawingBus; onError: (reason: string) => void }

const keyFor = (symbol: string) => `drawings.${symbol}`;
```

Add a constructor + the sync/persistence fields and methods to the `DrawingStore` class body. Replace the Task 3 `upsert`/`remove`/`clearSymbol` methods with these publish-aware versions:

```ts
  private deps: Deps | null = null;
  private readonly loaded = new Set<string>();
  private readonly dirtySymbols = new Set<string>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private offBus: (() => void) | null = null;

  constructor(private readonly debounceMs = 500) {
    super();
  }

  // Wire cross-window sync + persistence + error surfacing. Returns a disposer.
  connect(deps: Deps): () => void {
    this.deps = deps;
    this.offBus = deps.bus.onMessage((m) => this.applyRemote(m));
    return () => {
      this.offBus?.();
      this.offBus = null;
      if (this.timer) { clearTimeout(this.timer); this.timer = null; }
      this.deps = null;
    };
  }

  // Fire GetConfig once per symbol per session. Absent/malformed → empty.
  ensureLoaded(symbol: string): void {
    if (this.loaded.has(symbol) || !this.deps) return;
    this.loaded.add(symbol);
    const commands = this.deps.commands;
    void commands.sendCommand("GetConfig", { key: keyFor(symbol) })
      .then((ack) => {
        if (ack.status === "accepted") {
          for (const d of validateDrawings(ack.value)) this.setLocal(d); // load path: no publish/persist
        }
      })
      .catch(() => { /* load never blocks or crashes a chart */ });
  }

  async flush(): Promise<void> {
    this.timer = null;
    const deps = this.deps;
    if (!deps) { this.dirtySymbols.clear(); return; }
    const symbols = [...this.dirtySymbols];
    this.dirtySymbols.clear();
    for (const symbol of symbols) {
      const ack = await deps.commands.sendCommand("SetConfig", { key: keyFor(symbol), value: this.forSymbol(symbol) });
      if (ack.status !== "accepted") {
        deps.onError(ack.reason ?? `Failed to save drawings for ${symbol}`);
        this.dirtySymbols.add(symbol); // retry on the next flush
      }
    }
  }

  private scheduleFlush(symbol: string): void {
    this.dirtySymbols.add(symbol);
    if (this.timer) return;
    this.timer = setTimeout(() => { void this.flush(); }, this.debounceMs);
  }

  private applyRemote(m: DrawingMsg): void {
    if (m.op === "upsert" && m.drawing) this.setLocal(m.drawing);
    else if (m.op === "remove" && m.id) { if (this.deleteLocal(m.id)) this.markDirty(); }
    else if (m.op === "clear") { this.clearLocal(m.symbol); this.markDirty(); }
  }
```

And replace the three public mutators (from Task 3) with publish-aware versions:

```ts
  upsert(d: Drawing): void {
    this.setLocal(d);
    if (this.deps) {
      this.deps.bus.post({ op: "upsert", symbol: d.symbol, drawing: d });
      this.scheduleFlush(d.symbol);
    }
  }

  remove(id: string): void {
    const d = this.byId.get(id);
    if (!d) return;
    this.deleteLocal(id);
    this.markDirty();
    if (this.deps) {
      this.deps.bus.post({ op: "remove", symbol: d.symbol, id });
      this.scheduleFlush(d.symbol);
    }
  }

  clearSymbol(symbol: string): void {
    this.clearLocal(symbol);
    this.markDirty();
    if (this.deps) {
      this.deps.bus.post({ op: "clear", symbol });
      this.scheduleFlush(symbol);
    }
  }
```

Note: `byId`/`setLocal`/`deleteLocal`/`clearLocal` from Task 3 are reused unchanged. The load path (`ensureLoaded`) and remote path (`applyRemote`) both funnel through `setLocal`/`deleteLocal`/`clearLocal`, which never publish or persist — only the public mutators do (single-writer echo-guard, exactly like `linkGroups.ts`'s `focus()` vs `setLocal`).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd ui && npx vitest run src/render/chart/drawings/store.test.ts`
Expected: PASS (core + sync/persistence suites).

- [ ] **Step 6: Type-check and commit**

```bash
cd ui && npx tsc --noEmit && cd ..
git add ui/src/render/chart/drawings/store.ts ui/src/render/chart/drawings/store.test.ts ui/test/fakes.ts
git commit -m "feat(ui/chart): DrawingStore cross-window sync + debounced KV persistence + lazy load"
```

---

### Task 5: DrawingsPrimitive renderer

One `ISeriesPrimitive<Time>` per chart, attached to the candle series with `zOrder: "top"`. Renders persisted drawings + selection handles + placement ghost + measure box for the chart's current symbol. Unlike the existing primitives, it **captures `requestUpdate`** from `attached()` so the interaction layer can force a repaint mid-gesture without a bar update.

**Files:**
- Create: `ui/src/render/chart/drawings/primitive.ts`
- Test: `ui/src/render/chart/drawings/primitive.test.ts`

**Interfaces:**
- Consumes: `Drawing`, `DrawingKind`, `Anchor` from `./model`; `timeToLogical`, `extendToEdge` from `./geometry`; `Palette` from `../../palette`; LWC types from `"lightweight-charts"`.
- Produces:
  - `interface Transient { ghost?: { kind: DrawingKind; anchors: Anchor[] }; measure?: { from: Anchor; to: Anchor } }`
  - `interface DrawingsPrimitiveHandle { setSelection(id: string | null): void; setTransient(t: Transient | null): void; requestUpdate(): void }`
  - `class DrawingsPrimitive implements ISeriesPrimitive<Time>, DrawingsPrimitiveHandle` with `setPalette(p)`, `setDrawings(d: Drawing[])`, `setBars(barsMs: readonly number[], timeframeMs: number)`, plus the handle methods and `attached`/`detached`/`paneViews`.

- [ ] **Step 1: Write the failing test**

Create `ui/src/render/chart/drawings/primitive.test.ts`:

```ts
import { describe, it, expect, vi } from "vitest";
import { DrawingsPrimitive } from "./primitive";
import { LIGHT } from "../../palette";
import type { Drawing } from "./model";

// Records the 2D-context ops the renderer issues, without a real canvas.
function recordingCtx() {
  const calls: [string, ...number[]][] = [];
  const rec = (name: string) => (...args: number[]) => { calls.push([name, ...args]); };
  return {
    calls,
    ctx: {
      beginPath: rec("beginPath"), moveTo: rec("moveTo"), lineTo: rec("lineTo"),
      stroke: rec("stroke"), strokeRect: rec("strokeRect"), fillRect: rec("fillRect"),
      fillText: (t: string, x: number, y: number) => { calls.push(["fillText", x, y]); (calls as any).push(["text:" + t]); },
      setLineDash: () => {}, save: () => {}, restore: () => {},
      strokeStyle: "", fillStyle: "", lineWidth: 0, font: "", globalAlpha: 1, textBaseline: "",
    },
  };
}

function fakeTarget(ctx: unknown, width = 400, height = 300) {
  return {
    useBitmapCoordinateSpace: (cb: (s: any) => void) =>
      cb({ context: ctx, bitmapSize: { width, height }, mediaSize: { width, height }, horizontalPixelRatio: 1, verticalPixelRatio: 1 }),
  };
}

// logical*10 = x ; price → y = 1000 - price
const chartApi = { timeScale: () => ({ logicalToCoordinate: (l: number) => l * 10 }) };
const series = { priceToCoordinate: (p: number) => 1000 - p };
function attach(prim: DrawingsPrimitive, requestUpdate = vi.fn()) {
  (prim as any).attached({ chart: chartApi, series, requestUpdate });
  prim.setBars([0, 60_000], 60_000); // logical 0 at t=0, logical 1 at t=60000
  return requestUpdate;
}
function draw(prim: DrawingsPrimitive, ctx: unknown) {
  const view = prim.paneViews()[0];
  view.renderer()!.draw(fakeTarget(ctx) as any);
}

const hline: Drawing = { id: "h", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 10 }], createdMs: 1, updatedMs: 1 };

describe("DrawingsPrimitive", () => {
  it("returns a single top-zOrder pane view", () => {
    const p = new DrawingsPrimitive(LIGHT);
    const views = p.paneViews();
    expect(views).toHaveLength(1);
    expect(views[0].zOrder!()).toBe("top");
  });

  it("captures requestUpdate from attached()", () => {
    const p = new DrawingsPrimitive(LIGHT);
    const ru = attach(p);
    p.requestUpdate();
    expect(ru).toHaveBeenCalledOnce();
  });

  it("draws an hline spanning the full pane width at the price's y", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([hline]);
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    expect(calls).toContainEqual(["moveTo", 0, 990]);
    expect(calls).toContainEqual(["lineTo", 400, 990]);
  });

  it("skips a drawing whose price is off-screen (null coordinate)", () => {
    const p = new DrawingsPrimitive(LIGHT);
    (p as any).attached({ chart: chartApi, series: { priceToCoordinate: () => null }, requestUpdate: vi.fn() });
    p.setBars([0, 60_000], 60_000);
    p.setDrawings([hline]);
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    expect(calls.some((c) => c[0] === "lineTo")).toBe(false);
  });

  it("renders selection handles for the selected drawing", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([hline]);
    p.setSelection("h");
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    expect(calls.some((c) => c[0] === "fillRect")).toBe(true);   // handle body
    expect(calls.some((c) => c[0] === "strokeRect")).toBe(true); // handle border
  });

  it("renders a rectangle drawing as a stroked rect", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    const rect: Drawing = { id: "r", symbol: "US.AAPL", kind: "rect", anchors: [{ timeMs: 0, price: 20 }, { timeMs: 60_000, price: 10 }], createdMs: 1, updatedMs: 1 };
    p.setDrawings([rect]);
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    // corners: (logical0→x0, price20→y980) and (logical1→x10, price10→y990)
    expect(calls).toContainEqual(["strokeRect", 0, 980, 10, 10]);
  });

  it("draws a placement ghost from the transient state", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([]);
    p.setTransient({ ghost: { kind: "trendline", anchors: [{ timeMs: 0, price: 20 }, { timeMs: 60_000, price: 10 }] } });
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    expect(calls).toContainEqual(["moveTo", 0, 980]);
    expect(calls).toContainEqual(["lineTo", 10, 990]);
  });

  it("draws a measure box with a label", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setTransient({ measure: { from: { timeMs: 0, price: 10 }, to: { timeMs: 60_000, price: 11 } } });
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    expect(calls.some((c) => c[0] === "fillText")).toBe(true);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/drawings/primitive.test.ts`
Expected: FAIL — cannot resolve `./primitive`.

- [ ] **Step 3: Write the implementation**

Create `ui/src/render/chart/drawings/primitive.ts`:

```ts
import type {
  ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, Time,
  IPrimitivePaneView, IPrimitivePaneRenderer, Logical,
} from "lightweight-charts";
import type { Palette } from "../../palette";
import type { Anchor, Drawing, DrawingKind } from "./model";
import { extendToEdge, timeToLogical } from "./geometry";

// Repo convention: derive the draw target structurally instead of importing
// fancy-canvas's CanvasRenderingTarget2D directly.
type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];

export interface Transient {
  ghost?: { kind: DrawingKind; anchors: Anchor[] };
  measure?: { from: Anchor; to: Anchor };
}

export interface DrawingsPrimitiveHandle {
  setSelection(id: string | null): void;
  setTransient(t: Transient | null): void;
  requestUpdate(): void;
}

type Px = { x: number; y: number };

export class DrawingsPrimitive implements ISeriesPrimitive<Time>, DrawingsPrimitiveHandle {
  private series: ISeriesApi<"Candlestick"> | null = null;
  private chartApi: SeriesAttachedParameter<Time>["chart"] | null = null;
  private requestUpdateFn: (() => void) | null = null;
  private drawings: Drawing[] = [];
  private barsMs: readonly number[] = [];
  private timeframeMs = 60_000;
  private selectionId: string | null = null;
  private transient: Transient | null = null;

  constructor(private palette: Palette) {}

  attached(p: SeriesAttachedParameter<Time>): void {
    this.series = p.series as ISeriesApi<"Candlestick">;
    this.chartApi = p.chart;
    this.requestUpdateFn = p.requestUpdate;
  }
  detached(): void {
    this.series = null;
    this.chartApi = null;
    this.requestUpdateFn = null;
  }

  requestUpdate(): void { this.requestUpdateFn?.(); }
  setPalette(p: Palette): void { this.palette = p; }
  setDrawings(d: Drawing[]): void { this.drawings = d; }
  setBars(barsMs: readonly number[], timeframeMs: number): void { this.barsMs = barsMs; this.timeframeMs = timeframeMs; }
  setSelection(id: string | null): void { this.selectionId = id; }
  setTransient(t: Transient | null): void { this.transient = t; }

  paneViews(): readonly IPrimitivePaneView[] {
    const draw = (target: DrawTarget) => this.draw(target);
    return [{ renderer: () => ({ draw }), zOrder: () => "top" as const }];
  }

  private xOf(a: Anchor, hr: number): number | null {
    if (!this.chartApi) return null;
    const logical = timeToLogical(a.timeMs, this.barsMs, this.timeframeMs);
    const x = this.chartApi.timeScale().logicalToCoordinate(logical as Logical);
    return x === null ? null : x * hr;
  }
  private yOf(a: Anchor, vr: number): number | null {
    const y = this.series?.priceToCoordinate(a.price) ?? null;
    return y === null ? null : y * vr;
  }
  private pt(a: Anchor, hr: number, vr: number): Px | null {
    const x = this.xOf(a, hr);
    const y = this.yOf(a, vr);
    return x === null || y === null ? null : { x, y };
  }

  private draw(target: DrawTarget): void {
    target.useBitmapCoordinateSpace(({ context: ctx, bitmapSize, horizontalPixelRatio: hr, verticalPixelRatio: vr }) => {
      const width = bitmapSize.width;
      ctx.setLineDash([]);
      for (const d of this.drawings) {
        const selected = d.id === this.selectionId;
        this.strokeShape(ctx, d.kind, d.anchors, hr, vr, width, selected ? this.palette.accent : this.palette.text, selected ? 2 : 1);
        if (selected) this.handles(ctx, d.anchors, hr, vr);
      }
      if (this.transient?.ghost) {
        ctx.setLineDash([4, 3]);
        this.strokeShape(ctx, this.transient.ghost.kind, this.transient.ghost.anchors, hr, vr, width, this.palette.accent, 1);
        ctx.setLineDash([]);
      }
      if (this.transient?.measure) this.measure(ctx, this.transient.measure, hr, vr);
    });
  }

  private strokeShape(ctx: any, kind: DrawingKind, anchors: Anchor[], hr: number, vr: number, width: number, color: string, lineWidth: number): void {
    ctx.strokeStyle = color;
    ctx.lineWidth = lineWidth;
    const p0 = this.pt(anchors[0], hr, vr);
    if (!p0) return;
    if (kind === "hline") { this.line(ctx, 0, p0.y, width, p0.y); return; }
    if (kind === "hray") { this.line(ctx, p0.x, p0.y, width, p0.y); return; }
    const p1 = anchors[1] ? this.pt(anchors[1], hr, vr) : null;
    if (!p1) return;
    if (kind === "trendline") { this.line(ctx, p0.x, p0.y, p1.x, p1.y); return; }
    if (kind === "ray") { const far = extendToEdge(p0, p1, width); this.line(ctx, p0.x, p0.y, far.x, far.y); return; }
    if (kind === "rect") { ctx.strokeRect(Math.min(p0.x, p1.x), Math.min(p0.y, p1.y), Math.abs(p1.x - p0.x), Math.abs(p1.y - p0.y)); }
  }

  private line(ctx: any, x0: number, y0: number, x1: number, y1: number): void {
    ctx.beginPath();
    ctx.moveTo(x0, y0);
    ctx.lineTo(x1, y1);
    ctx.stroke();
  }

  private handles(ctx: any, anchors: Anchor[], hr: number, vr: number): void {
    const r = 3;
    for (const a of anchors) {
      const p = this.pt(a, hr, vr);
      if (!p) continue;
      ctx.fillStyle = this.palette.bg;
      ctx.fillRect(p.x - r, p.y - r, r * 2, r * 2);
      ctx.strokeStyle = this.palette.accent;
      ctx.lineWidth = 1;
      ctx.strokeRect(p.x - r, p.y - r, r * 2, r * 2);
    }
  }

  private measure(ctx: any, m: { from: Anchor; to: Anchor }, hr: number, vr: number): void {
    const p0 = this.pt(m.from, hr, vr);
    const p1 = this.pt(m.to, hr, vr);
    if (!p0 || !p1) return;
    const x = Math.min(p0.x, p1.x);
    const y = Math.min(p0.y, p1.y);
    const w = Math.abs(p1.x - p0.x);
    const h = Math.abs(p1.y - p0.y);
    ctx.fillStyle = this.palette.accent;
    ctx.globalAlpha = 0.12;
    ctx.fillRect(x, y, w, h);
    ctx.globalAlpha = 1;
    ctx.strokeStyle = this.palette.accent;
    ctx.lineWidth = 1;
    ctx.strokeRect(x, y, w, h);
    const dPts = m.to.price - m.from.price;
    const dPct = m.from.price !== 0 ? (dPts / m.from.price) * 100 : 0;
    const bars = Math.round(timeToLogical(m.to.timeMs, this.barsMs, this.timeframeMs)) - Math.round(timeToLogical(m.from.timeMs, this.barsMs, this.timeframeMs));
    const label = `${dPts >= 0 ? "+" : ""}${dPts.toFixed(2)}  ${dPct >= 0 ? "+" : ""}${dPct.toFixed(2)}%  ${Math.abs(bars)} bars`;
    ctx.fillStyle = this.palette.text;
    ctx.font = `${12 * vr}px sans-serif`;
    ctx.textBaseline = "bottom";
    ctx.fillText(label, x, y - 2);
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/drawings/primitive.test.ts`
Expected: PASS (all cases). This uses a fake target/context — no real canvas — so it stays on the default `threads` pool (no `poolMatchGlobs` change needed).

- [ ] **Step 5: Type-check and commit**

```bash
cd ui && npx tsc --noEmit && cd ..
git add ui/src/render/chart/drawings/primitive.ts ui/src/render/chart/drawings/primitive.test.ts
git commit -m "feat(ui/chart): DrawingsPrimitive renderer (lines/rays/rect, selection, ghost, measure)"
```

---

### Task 6: DrawingInteraction state machine

A plain class per chart (no React) with pointer + key handlers on the host, converting px↔`{timeMs, price}` through a facade subset. Drives the `select`/`armed`/`measure` state machine, pan/zoom lock, magnet, and mutations into the store. Time always snaps to the hovered bar; price snaps to O/H/L/C when magnet is on.

**Files:**
- Create: `ui/src/render/chart/drawings/interaction.ts`
- Test: `ui/src/render/chart/drawings/interaction.test.ts`

**Interfaces:**
- Consumes: `Drawing`, `Anchor`, `DrawingKind`, `anchorCount` from `./model`; `DrawingStore` from `./store`; `DrawingsPrimitiveHandle`, `Transient` from `./primitive`; `timeToLogical`, `hitTest`, `snapToLevels`, `type Px` from `./geometry`; `Bar` from `../../../gen/wsmsg`.
- Produces:
  - `type Tool = "select" | "hline" | "hray" | "trendline" | "ray" | "rect" | "measure"`
  - `interface DrawingFacade { logicalToCoordinate(l: number): number | null; coordinateToLogical(x: number): number | null; coordinateToPrice(y: number): number | null; priceToCoordinate(p: number): number | null; setPanZoomEnabled(on: boolean): void }`
  - `interface InteractionHost { addEventListener; removeEventListener; getBoundingClientRect; focus; clientWidth; tabIndex; style: { outline: string } }` (real `HTMLDivElement` satisfies it structurally)
  - `interface DrawingContext { symbol(): string; bars(): readonly Bar[]; timeframeMs(): number; magnet(): boolean }`
  - `class DrawingInteraction` with `constructor(host, facade, primitive, store, ctx, opts?: { newId?: () => string; onToolChange?: (t: Tool) => void })`, `setTool(t)`, `onSymbolChanged()`, `dispose()`.

- [ ] **Step 1: Write the failing test**

Create `ui/src/render/chart/drawings/interaction.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from "vitest";
import { DrawingInteraction, type Tool } from "./interaction";
import { DrawingStore } from "./store";
import type { Bar } from "../../../gen/wsmsg";

// Two 1m bars: t=0 (09:30) and t=60000. bucketStart is ISO; Date.parse recovers ms.
const bars: Bar[] = [
  { symbol: "US.AAPL", timeframe: "1m", bucketStart: new Date(0).toISOString(), o: 10, h: 20, l: 5, c: 15, v: 1, inProgress: false },
  { symbol: "US.AAPL", timeframe: "1m", bucketStart: new Date(60_000).toISOString(), o: 15, h: 25, l: 12, c: 22, v: 1, inProgress: false },
];

// Facade: logical*10=x ; price→y = 1000-price ; y→price = 1000-y ; x→logical = x/10
function fakeFacade() {
  return {
    logicalToCoordinate: (l: number) => l * 10,
    coordinateToLogical: (x: number) => x / 10,
    coordinateToPrice: (y: number) => 1000 - y,
    priceToCoordinate: (p: number) => 1000 - p,
    setPanZoomEnabled: vi.fn(),
  };
}
function fakePrimitive() {
  return { setSelection: vi.fn(), setTransient: vi.fn(), requestUpdate: vi.fn() };
}
function fakeHost() {
  const handlers = new Map<string, (e: any) => void>();
  const host = {
    addEventListener: (t: string, cb: (e: any) => void) => handlers.set(t, cb),
    removeEventListener: (t: string) => handlers.delete(t),
    getBoundingClientRect: () => ({ left: 0, top: 0, width: 400, height: 300 }),
    focus: vi.fn(),
    clientWidth: 400,
    tabIndex: 0,
    style: { outline: "" },
  };
  return { host, fire: (t: string, e: any) => handlers.get(t)?.(e) };
}
function ctx(magnet = false) {
  return { symbol: () => "US.AAPL", bars: () => bars, timeframeMs: () => 60_000, magnet: () => magnet };
}

let ids = 0;
const newId = () => `id${++ids}`;
beforeEach(() => { ids = 0; });

describe("DrawingInteraction", () => {
  it("arming a drawing tool locks pan/zoom", () => {
    const f = fakeFacade();
    const di = new DrawingInteraction(fakeHost().host, f, fakePrimitive(), new DrawingStore(), ctx(), { newId });
    di.setTool("trendline");
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(false);
  });

  it("commits an hline on the first click and reverts to select", () => {
    const store = new DrawingStore();
    const f = fakeFacade();
    const onToolChange = vi.fn();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, f, fakePrimitive(), store, ctx(), { newId, onToolChange });
    di.setTool("hline");
    fire("pointerdown", { clientX: 5, clientY: 900 }); // price = 1000-900 = 100
    const drawn = store.forSymbol("US.AAPL");
    expect(drawn).toHaveLength(1);
    expect(drawn[0].kind).toBe("hline");
    expect(drawn[0].anchors[0].price).toBe(100);
    expect(onToolChange).toHaveBeenLastCalledWith("select");
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(true); // unlocked after commit
  });

  it("requires two clicks for a trendline, showing a ghost between them", () => {
    const store = new DrawingStore();
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    di.setTool("trendline");
    fire("pointerdown", { clientX: 0, clientY: 990 });
    expect(store.forSymbol("US.AAPL")).toHaveLength(0); // not committed yet
    fire("pointermove", { clientX: 10, clientY: 980 });
    expect(prim.setTransient).toHaveBeenCalled();       // ghost shown
    fire("pointerdown", { clientX: 10, clientY: 980 });
    const drawn = store.forSymbol("US.AAPL");
    expect(drawn).toHaveLength(1);
    expect(drawn[0].kind).toBe("trendline");
    expect(drawn[0].anchors).toHaveLength(2);
  });

  it("Esc cancels an in-progress placement and reverts to select", () => {
    const store = new DrawingStore();
    const prim = fakePrimitive();
    const onToolChange = vi.fn();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId, onToolChange });
    di.setTool("rect");
    fire("pointerdown", { clientX: 0, clientY: 990 });
    fire("keydown", { key: "Escape" });
    fire("pointerdown", { clientX: 10, clientY: 980 });
    expect(store.forSymbol("US.AAPL")).toHaveLength(0); // placement was abandoned
    expect(onToolChange).toHaveBeenLastCalledWith("select");
  });

  it("selects a drawing on click and deletes it with Delete", () => {
    const store = new DrawingStore();
    store.upsert({ id: "x", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 100 }], createdMs: 1, updatedMs: 1 });
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    // hline at price 100 → y = 900. Click near it in select mode.
    fire("pointerdown", { clientX: 50, clientY: 901 });
    expect(prim.setSelection).toHaveBeenLastCalledWith("x");
    fire("keydown", { key: "Delete" });
    expect(store.forSymbol("US.AAPL")).toHaveLength(0);
  });

  it("clicking empty space deselects", () => {
    const store = new DrawingStore();
    store.upsert({ id: "x", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 100 }], createdMs: 1, updatedMs: 1 });
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    fire("pointerdown", { clientX: 50, clientY: 300 }); // far from the line (y=900)
    expect(prim.setSelection).toHaveBeenLastCalledWith(null);
  });

  it("magnet snaps the placed price to the hovered bar's OHLC when enabled", () => {
    const store = new DrawingStore();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), fakePrimitive(), store, ctx(true), { newId });
    di.setTool("hline");
    // bar0 high=20 → y=980. Click at y=983 (3px away, within 6px) → snaps to 20.
    fire("pointerdown", { clientX: 0, clientY: 983 });
    expect(store.forSymbol("US.AAPL")[0].anchors[0].price).toBe(20);
  });

  it("measure shows a transient box and never persists a drawing", () => {
    const store = new DrawingStore();
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    di.setTool("measure");
    fire("pointerdown", { clientX: 0, clientY: 990 });
    fire("pointermove", { clientX: 10, clientY: 980 });
    fire("pointerup", { clientX: 10, clientY: 980 });
    expect(prim.setTransient).toHaveBeenCalledWith(expect.objectContaining({ measure: expect.anything() }));
    expect(store.forSymbol("US.AAPL")).toHaveLength(0);
  });

  it("onSymbolChanged cancels the gesture, drops selection, and restores pan/zoom", () => {
    const store = new DrawingStore();
    const f = fakeFacade();
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, f, prim, store, ctx(), { newId });
    di.setTool("trendline");
    fire("pointerdown", { clientX: 0, clientY: 990 }); // anchor0 pending
    di.onSymbolChanged();
    fire("pointerdown", { clientX: 10, clientY: 980 }); // would-be 2nd click
    expect(store.forSymbol("US.AAPL")).toHaveLength(0); // placement was reset
    expect(prim.setSelection).toHaveBeenLastCalledWith(null);
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(true);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd ui && npx vitest run src/render/chart/drawings/interaction.test.ts`
Expected: FAIL — cannot resolve `./interaction`.

- [ ] **Step 3: Write the implementation**

Create `ui/src/render/chart/drawings/interaction.ts`:

```ts
import type { Bar } from "../../../gen/wsmsg";
import { anchorCount, type Anchor, type Drawing, type DrawingKind } from "./model";
import type { DrawingStore } from "./store";
import type { DrawingsPrimitiveHandle } from "./primitive";
import { hitTest, snapToLevels, timeToLogical, type Px } from "./geometry";

export type Tool = "select" | "hline" | "hray" | "trendline" | "ray" | "rect" | "measure";

export interface DrawingFacade {
  logicalToCoordinate(logical: number): number | null;
  coordinateToLogical(x: number): number | null;
  coordinateToPrice(y: number): number | null;
  priceToCoordinate(price: number): number | null;
  setPanZoomEnabled(on: boolean): void;
}

export interface InteractionHost {
  addEventListener(type: string, cb: (e: any) => void): void;
  removeEventListener(type: string, cb: (e: any) => void): void;
  getBoundingClientRect(): { left: number; top: number; width: number; height: number };
  focus(): void;
  clientWidth: number;
  tabIndex: number;
  style: { outline: string };
}

export interface DrawingContext {
  symbol(): string;
  bars(): readonly Bar[];
  timeframeMs(): number;
  magnet(): boolean;
}

type PointerLike = { clientX: number; clientY: number };
type KeyLike = { key: string; preventDefault?: () => void };

type Gesture =
  | { kind: "none" }
  | { kind: "placing"; anchor0: Anchor }
  | { kind: "measuring"; from: Anchor }
  | { kind: "handleDrag"; id: string; index: number }
  | { kind: "bodyDrag"; id: string; downLogical: number; downPrice: number; orig: Anchor[] };

const MAGNET_PX = 6;

export class DrawingInteraction {
  private tool: Tool = "select";
  private gesture: Gesture = { kind: "none" };
  private selectionId: string | null = null;
  private readonly newId: () => string;
  private readonly onToolChange?: (t: Tool) => void;
  private readonly listeners: [string, (e: any) => void][] = [];

  constructor(
    private readonly host: InteractionHost,
    private readonly facade: DrawingFacade,
    private readonly primitive: DrawingsPrimitiveHandle,
    private readonly store: DrawingStore,
    private readonly ctx: DrawingContext,
    opts?: { newId?: () => string; onToolChange?: (t: Tool) => void },
  ) {
    this.newId = opts?.newId ?? (() => crypto.randomUUID());
    this.onToolChange = opts?.onToolChange;
    host.tabIndex = host.tabIndex >= 0 ? host.tabIndex : 0;
    host.style.outline = "none";
    const on = (t: string, cb: (e: any) => void) => { host.addEventListener(t, cb); this.listeners.push([t, cb]); };
    on("pointerdown", (e) => this.onPointerDown(e));
    on("pointermove", (e) => this.onPointerMove(e));
    on("pointerup", (e) => this.onPointerUp(e));
    on("keydown", (e) => this.onKeyDown(e));
  }

  setTool(tool: Tool): void {
    this.cancelGesture();
    this.tool = tool;
    if (tool !== "select") { this.selectionId = null; this.primitive.setSelection(null); }
    this.applyPanZoomLock();
    this.primitive.requestUpdate();
  }

  onSymbolChanged(): void {
    this.cancelGesture();
    this.selectionId = null;
    this.primitive.setSelection(null);
    this.applyPanZoomLock();
    this.primitive.requestUpdate();
  }

  dispose(): void {
    for (const [t, cb] of this.listeners) this.host.removeEventListener(t, cb);
    this.listeners.length = 0;
    this.facade.setPanZoomEnabled(true);
  }

  // --- pan/zoom lock: armed tools lock the whole time; select/measure only during a drag ---
  private applyPanZoomLock(): void {
    const armed = this.tool !== "select" && this.tool !== "measure";
    this.facade.setPanZoomEnabled(!armed);
  }

  private cancelGesture(): void {
    this.gesture = { kind: "none" };
    this.primitive.setTransient(null);
  }

  // --- coordinate helpers ---
  private pos(e: PointerLike): Px {
    const r = this.host.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }
  private barsMs(): number[] {
    return this.ctx.bars().map((b) => Date.parse(b.bucketStart));
  }
  private snap(p: Px): Anchor | null {
    const bars = this.ctx.bars();
    if (bars.length === 0) return null;
    const logical = this.facade.coordinateToLogical(p.x);
    if (logical === null) return null;
    const idx = Math.max(0, Math.min(bars.length - 1, Math.round(logical)));
    const timeMs = Date.parse(bars[idx].bucketStart);
    const raw = this.facade.coordinateToPrice(p.y);
    let price = raw ?? 0;
    if (this.ctx.magnet() && raw !== null) {
      const b = bars[idx];
      const levels = [b.o, b.h, b.l, b.c]
        .map((pr) => ({ price: pr, y: this.facade.priceToCoordinate(pr) }))
        .filter((l): l is { price: number; y: number } => l.y !== null);
      const snapped = snapToLevels(p.y, levels, MAGNET_PX);
      if (snapped !== null) price = snapped;
    }
    return { timeMs, price };
  }
  private project(a: Anchor): Px | null {
    const logical = timeToLogical(a.timeMs, this.barsMs(), this.ctx.timeframeMs());
    const x = this.facade.logicalToCoordinate(logical);
    const y = this.facade.priceToCoordinate(a.price);
    return x === null || y === null ? null : { x, y };
  }

  // --- pointer handlers ---
  private onPointerDown(e: PointerLike): void {
    this.host.focus();
    const p = this.pos(e);
    const anchor = this.snap(p);

    if (this.tool === "measure") {
      if (!anchor) return;
      this.gesture = { kind: "measuring", from: anchor };
      this.facade.setPanZoomEnabled(false);
      this.primitive.setTransient({ measure: { from: anchor, to: anchor } });
      this.primitive.requestUpdate();
      return;
    }

    if (this.tool !== "select") { this.placeAnchor(anchor); return; }

    // select mode: hit-test top-most first
    const drawings = this.store.forSymbol(this.ctx.symbol());
    for (let i = drawings.length - 1; i >= 0; i--) {
      const d = drawings[i];
      const pts = d.anchors.map((a) => this.project(a));
      const hit = hitTest(d.kind, pts, p, this.host.clientWidth);
      if (!hit) continue;
      this.selectionId = d.id;
      this.primitive.setSelection(d.id);
      this.facade.setPanZoomEnabled(false);
      if (hit.type === "handle") {
        this.gesture = { kind: "handleDrag", id: d.id, index: hit.index };
      } else {
        const logical = this.facade.coordinateToLogical(p.x) ?? 0;
        const price = this.facade.coordinateToPrice(p.y) ?? 0;
        this.gesture = { kind: "bodyDrag", id: d.id, downLogical: logical, downPrice: price, orig: d.anchors.map((a) => ({ ...a })) };
      }
      this.primitive.requestUpdate();
      return;
    }
    // empty space → deselect (pan/zoom stays enabled so LWC pans)
    this.selectionId = null;
    this.primitive.setSelection(null);
    this.primitive.requestUpdate();
  }

  private placeAnchor(anchor: Anchor | null): void {
    if (!anchor) return;
    const kind = this.tool as DrawingKind;
    if (this.gesture.kind === "placing") {
      // second click → commit
      this.commit(kind, [this.gesture.anchor0, anchor]);
      return;
    }
    if (anchorCount(kind) === 1) { this.commit(kind, [anchor]); return; }
    // first click of a 2-anchor tool → start placing, show ghost
    this.gesture = { kind: "placing", anchor0: anchor };
    this.primitive.setTransient({ ghost: { kind, anchors: [anchor, anchor] } });
    this.primitive.requestUpdate();
  }

  private commit(kind: DrawingKind, anchors: Anchor[]): void {
    const now = Date.now();
    const d: Drawing = { id: this.newId(), symbol: this.ctx.symbol(), kind, anchors, createdMs: now, updatedMs: now };
    this.store.upsert(d);
    this.cancelGesture();
    // revert to select (TradingView behavior)
    this.tool = "select";
    this.onToolChange?.("select");
    this.applyPanZoomLock();
    this.primitive.requestUpdate();
  }

  private onPointerMove(e: PointerLike): void {
    const p = this.pos(e);
    const g = this.gesture;
    if (g.kind === "placing") {
      const anchor = this.snap(p);
      if (anchor) { this.primitive.setTransient({ ghost: { kind: this.tool as DrawingKind, anchors: [g.anchor0, anchor] } }); this.primitive.requestUpdate(); }
    } else if (g.kind === "measuring") {
      const anchor = this.snap(p);
      if (anchor) { this.primitive.setTransient({ measure: { from: g.from, to: anchor } }); this.primitive.requestUpdate(); }
    } else if (g.kind === "handleDrag") {
      const anchor = this.snap(p);
      const d = this.currentDrawing(g.id);
      if (anchor && d) {
        const anchors = d.anchors.map((a, i) => (i === g.index ? anchor : a));
        this.store.upsert({ ...d, anchors, updatedMs: Date.now() });
        this.primitive.requestUpdate();
      }
    } else if (g.kind === "bodyDrag") {
      const d = this.currentDrawing(g.id);
      const curLogical = this.facade.coordinateToLogical(p.x);
      const curPrice = this.facade.coordinateToPrice(p.y);
      if (d && curLogical !== null && curPrice !== null) {
        const dBars = Math.round(curLogical) - Math.round(g.downLogical);
        const dPrice = curPrice - g.downPrice;
        const bars = this.ctx.bars();
        const barsMs = this.barsMs();
        const anchors = g.orig.map((a) => {
          const idx = Math.max(0, Math.min(bars.length - 1, Math.round(timeToLogical(a.timeMs, barsMs, this.ctx.timeframeMs())) + dBars));
          return { timeMs: bars.length ? Date.parse(bars[idx].bucketStart) : a.timeMs, price: a.price + dPrice };
        });
        this.store.upsert({ ...d, anchors, updatedMs: Date.now() });
        this.primitive.requestUpdate();
      }
    }
  }

  private onPointerUp(_e: PointerLike): void {
    const g = this.gesture;
    if (g.kind === "handleDrag" || g.kind === "bodyDrag") {
      this.gesture = { kind: "none" };
      this.applyPanZoomLock(); // back to select → unlock
      this.primitive.requestUpdate();
    } else if (g.kind === "measuring") {
      // keep the box visible until the next pointerdown or Esc; just end the drag
      this.gesture = { kind: "none" };
      this.facade.setPanZoomEnabled(true);
    }
  }

  private onKeyDown(e: KeyLike): void {
    if (e.key === "Escape") {
      e.preventDefault?.();
      if (this.gesture.kind === "placing") {
        this.cancelGesture();
        this.tool = "select";
        this.onToolChange?.("select");
        this.applyPanZoomLock();
      } else if (this.gesture.kind === "measuring" || this.primitive) {
        this.cancelGesture(); // clears a lingering measure box
      }
      this.selectionId = null;
      this.primitive.setSelection(null);
      this.primitive.requestUpdate();
      return;
    }
    if ((e.key === "Delete" || e.key === "Backspace") && this.selectionId) {
      e.preventDefault?.();
      this.store.remove(this.selectionId);
      this.selectionId = null;
      this.primitive.setSelection(null);
      this.primitive.requestUpdate();
    }
  }

  private currentDrawing(id: string): Drawing | undefined {
    return this.store.forSymbol(this.ctx.symbol()).find((d) => d.id === id);
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd ui && npx vitest run src/render/chart/drawings/interaction.test.ts`
Expected: PASS (all cases).

- [ ] **Step 5: Type-check and commit**

```bash
cd ui && npx tsc --noEmit && cd ..
git add ui/src/render/chart/drawings/interaction.ts ui/src/render/chart/drawings/interaction.test.ts
git commit -m "feat(ui/chart): DrawingInteraction pointer/key state machine (select/armed/measure)"
```

- [ ] **Step 6: Phase 1 gate — run the full drawings suite + type-check**

Run: `cd ui && npx tsc --noEmit && npx vitest run src/render/chart/drawings/`
Expected: all model/geometry/store/primitive/interaction suites PASS, no type errors. Phase 1 is complete and self-contained — the modules are not yet imported by `ChartPanel`, so the running app is unchanged.

---

## Phase 2 — Integration (GATED on the Daylight Ledger redesign landing on `main`)

> **Do not start Phase 2 until the Daylight Ledger redesign (`docs/superpowers/plans/2026-07-07-ui-redesign-daylight-ledger.md`) has landed on `main`.** Tasks 8–9 depend on the redesign's `ui/src/chrome/cssVars.ts` and the rewritten `ui/src/global.css` (with `.btn`/`.ctl`/`.popover` classes and the `--accent`/`--border-strong`/`--bg`/`--surface`/`--text`/`--text-muted`/`--danger` custom properties). Task 7's re-verify step confirms the target state before any edit.

### Task 7: Re-verify integration surface + add facade coordinate/pan-zoom methods

**Files:**
- Modify: `ui/src/render/chart/ChartApiFacade.ts`
- Modify: `ui/src/chrome/panels/ChartPanel.tsx` (the `makeFacade` closure only)
- Modify: `ui/src/render/chart/ChartController.test.ts` (`fakeFacade()`)
- Modify: `ui/src/chrome/panels/ChartPanel.test.tsx` (the mocked chart)

**Interfaces:**
- Produces (added to `ChartApiFacade`): `logicalToCoordinate(logical: number): number | null`, `coordinateToLogical(x: number): number | null`, `coordinateToPrice(y: number): number | null`, `setPanZoomEnabled(on: boolean): void`. These satisfy the `DrawingFacade` subset structurally (Task 6).

- [ ] **Step 1: Re-verify the integration surface (no edits yet)**

Confirm the plan's assumptions still hold against post-redesign `main`. Run:

```bash
cd ui
# host div is still position:relative and named hostRef
grep -n 'hostRef' src/chrome/panels/ChartPanel.tsx
grep -n 'position: "relative"' src/chrome/panels/ChartPanel.tsx
# makeFacade still builds the facade + attaches primitives here
grep -n 'makeFacade\|attachPrimitive\|const facade' src/chrome/panels/ChartPanel.tsx
# the paint-loop dirty check + symbol-apply hooks still exist
grep -n 'isDirty\|getRev()\|applySymbol\|backfillFills\|scheduler.register' src/chrome/panels/ChartPanel.tsx
# redesign token layer landed
test -f src/chrome/cssVars.ts && echo "cssVars OK" || echo "MISSING cssVars — redesign not landed"
grep -n '\.btn\b\|\.ctl\b\|\.popover\b' src/global.css
grep -n '\-\-accent\|\-\-border-strong' src/render/palette.ts src/chrome/cssVars.ts 2>/dev/null
```

Expected: `hostRef` + `position: "relative"` present; `makeFacade`/`attachPrimitive` present; `isDirty`/`getRev()`/`applySymbol`/`scheduler.register` present; `cssVars OK`; `.btn`/`.ctl`/`.popover` present in `global.css`. **If any assumption fails** (e.g. the redesign renamed `hostRef`, moved `makeFacade`, or restructured the host div), STOP and reconcile Tasks 7–9's diffs against the actual code before proceeding — the code below assumes the structure recon captured on 2026-07-07.

- [ ] **Step 2: Extend the `ChartApiFacade` interface**

Modify `ui/src/render/chart/ChartApiFacade.ts` — add to the `ChartApiFacade` interface (after `priceToCoordinate`):

```ts
  logicalToCoordinate(logical: number): number | null;
  coordinateToLogical(x: number): number | null;
  coordinateToPrice(y: number): number | null;
  setPanZoomEnabled(on: boolean): void;
```

- [ ] **Step 3: Implement them in `makeFacade` + update the test fakes (compile-forcing change)**

In `ui/src/chrome/panels/ChartPanel.tsx`, extend the type import from `"lightweight-charts"` to include `type Logical, type Coordinate`, then add the four methods to the `facade` object literal inside `makeFacade` (next to `timeToCoordinate`/`priceToCoordinate`):

```ts
    logicalToCoordinate: (logical) => chart.timeScale().logicalToCoordinate(logical as Logical),
    coordinateToLogical: (x) => chart.timeScale().coordinateToLogical(x as Coordinate),
    coordinateToPrice: (y) => candle?.coordinateToPrice(y as Coordinate) ?? null,
    setPanZoomEnabled: (on) => chart.applyOptions({ handleScroll: on, handleScale: on }),
```

In `ui/src/render/chart/ChartController.test.ts`, add the four methods to `fakeFacade()` (no-ops matching the existing style):

```ts
    logicalToCoordinate: () => 0,
    coordinateToLogical: () => 0,
    coordinateToPrice: () => 0,
    setPanZoomEnabled: () => {},
```

In `ui/src/chrome/panels/ChartPanel.test.tsx`, extend the mocked `chartApi`:
- add `coordinateToLogical: vi.fn(() => 0), logicalToCoordinate: vi.fn(() => 0)` to the `timeScale` mock's returned object;
- add `coordinateToPrice: vi.fn(() => 0)` to the object returned by `addSeries`'s `vi.fn()`.

- [ ] **Step 4: Type-check and run the affected suites**

Run: `cd ui && npx tsc --noEmit && npx vitest run src/render/chart/ChartController.test.ts src/chrome/panels/ChartPanel.test.tsx`
Expected: PASS, no type errors. (`ChartApiFacade` now satisfies `DrawingFacade` structurally.)

- [ ] **Step 5: Commit**

```bash
git add ui/src/render/chart/ChartApiFacade.ts ui/src/chrome/panels/ChartPanel.tsx ui/src/render/chart/ChartController.test.ts ui/src/chrome/panels/ChartPanel.test.tsx
git commit -m "feat(ui/chart): facade coordinate + pan/zoom methods for drawing tools"
```

---

### Task 8: Wire the drawing layer into ChartPanel + App.tsx

Attach the `DrawingsPrimitive` to the candle series, feed it persisted drawings + bar times each dirty frame, load persisted drawings on symbol apply, drive a `DrawingInteraction` on the host, and connect the store to the bus/persistence/toast in `App.tsx`.

**Files:**
- Modify: `ui/src/chrome/panels/ChartPanel.tsx`
- Modify: `ui/src/App.tsx`
- Modify: `ui/src/chrome/panels/ChartPanel.test.tsx` (integration tests)

**Interfaces:**
- Consumes: `DrawingsPrimitive` from `../../render/chart/drawings/primitive`; `DrawingInteraction`, `type Tool` from `../../render/chart/drawings/interaction`; `timeframeToMs` from `../../render/chart/drawings/geometry`; `type Timeframe` from `../../render/chart/barBucket`; `DrawingStore`, `BroadcastChannelDrawingBus` from the store module.
- Produces: `ChartPanel` state `activeTool: Tool` + `magnetRef` (consumed by the `DrawingRail` in Task 9 via the interaction ref).

- [ ] **Step 1: Write the failing integration tests**

Append to `ui/src/chrome/panels/ChartPanel.test.tsx`. Add imports at the top:

```tsx
import { FakeDrawingBus, FakeDrawingBusHub } from "../../../test/fakes";
```

Add these cases inside `describe("ChartPanel", ...)`:

```tsx
  it("loads persisted drawings for its symbol on mount (ensureLoaded → GetConfig)", async () => {
    const stores = makeStores();
    const hub = new FakeDrawingBusHub();
    const drawCmd = { sendCommand: vi.fn(async () => ({ status: "accepted", value: [] })) };
    stores.drawings.connect({ commands: drawCmd as never, bus: new FakeDrawingBus(hub), onError: () => {} });
    renderChart("c1", stores);
    await Promise.resolve();
    expect(drawCmd.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "drawings.US.AAPL" });
  });

  it("shares one drawings store across two panels without crashing", () => {
    const stores = makeStores();
    renderChart("panel-a", stores);
    renderChart("panel-b", stores);
    stores.drawings.upsert({ id: "d", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }], createdMs: 1, updatedMs: 1 });
    expect(stores.drawings.forSymbol("US.AAPL")).toHaveLength(1);
  });
```

- [ ] **Step 2: Run to verify the first test fails**

Run: `cd ui && npx vitest run src/chrome/panels/ChartPanel.test.tsx`
Expected: the "ensureLoaded → GetConfig" test FAILS (no `GetConfig` for `drawings.US.AAPL` yet — ChartPanel doesn't call `ensureLoaded`). The "shares one store" test may pass trivially.

- [ ] **Step 3: Attach the primitive in `makeFacade`**

In `ui/src/chrome/panels/ChartPanel.tsx`, add the import:

```ts
import { DrawingsPrimitive } from "../../render/chart/drawings/primitive";
import { DrawingInteraction, type Tool } from "../../render/chart/drawings/interaction";
import { timeframeToMs } from "../../render/chart/drawings/geometry";
import type { Timeframe } from "../../render/chart/barBucket";
```

In `makeFacade`, instantiate the primitive alongside `session`/`diamonds`, attach it to the candle series, expose it on the return value, and add it to the palette closure. Change the signature/return:

```ts
function makeFacade(chart: IChartApi, palette: Palette): {
  facade: ChartApiFacade; setPalette: (p: Palette) => void; drawings: DrawingsPrimitive;
} {
  let candle: ISeriesApi<"Candlestick"> | null = null;
  const session = new SessionShadingPrimitive(palette);
  const diamonds = new DiamondFillPrimitive(palette);
  const drawings = new DrawingsPrimitive(palette);
  // ... inside addSeries, when kind === "candle", after candle.attachPrimitive(diamonds):
  //     candle.attachPrimitive(drawings);   // zOrder "top" → above candles + indicators
  // ... return:
  return { facade, setPalette: (p) => { session.setPalette(p); diamonds.setPalette(p); drawings.setPalette(p); }, drawings };
}
```

- [ ] **Step 4: Wire refs, interaction, paint loop, and symbol-apply in the mount effect**

In the `ChartPanel` component body, add refs and tool state (near `controllerRef`):

```ts
const interactionRef = useRef<DrawingInteraction | null>(null);
const magnetRef = useRef(true);
const tfRef = useRef<string>(timeframe0);
const [activeTool, setActiveTool] = useState<Tool>("select");
useEffect(() => { tfRef.current = timeframe; }, [timeframe]);
```

In the mount `useEffect` (deps `[config.id]`), destructure `drawings` from `makeFacade` and construct the interaction after `controller.mount()` and the indicator restore loop, BEFORE the first `applySymbol()`:

```ts
const { facade, setPalette, drawings } = makeFacade(chart, palette);
// ... controller created + mounted + indicators restored ...
const interaction = new DrawingInteraction(
  host,
  facade,
  drawings,
  stores.drawings,
  {
    symbol: () => currentSymbol,
    bars: () => stores.bars.series(currentSymbol, tfRef.current),
    timeframeMs: () => timeframeToMs(tfRef.current as Timeframe),
    magnet: () => magnetRef.current,
  },
  { onToolChange: (t) => setActiveTool(t) },
);
interactionRef.current = interaction;
```

Update `applySymbol` to load drawings + reset the interaction on symbol change:

```ts
const applySymbol = () => {
  currentSymbol = linkGroups.symbolFor(config.group) ?? symbol;
  controller.setSymbol(currentSymbol);
  backfillFills(currentSymbol);
  stores.drawings.ensureLoaded(currentSymbol);
  interactionRef.current?.onSymbolChanged();
};
```

Extend the scheduler surface's `isDirty` + `paint` (add `lastDrawingsRev`):

```ts
let lastBarsRev = -1;
let lastIndicatorsRev = -1;
let lastFillsRev = -1;
let lastDrawingsRev = -1;
const off = scheduler.register({
  id: `chart:${config.id}`,
  isDirty: () => {
    const barsRev = stores.bars.getRev();
    const indicatorsRev = stores.indicators.getRev();
    const fillsRev = stores.fills.getRev();
    const drawingsRev = stores.drawings.getRev();
    const changed = barsRev !== lastBarsRev || indicatorsRev !== lastIndicatorsRev || fillsRev !== lastFillsRev || drawingsRev !== lastDrawingsRev;
    lastBarsRev = barsRev; lastIndicatorsRev = indicatorsRev; lastFillsRev = fillsRev; lastDrawingsRev = drawingsRev;
    return changed;
  },
  paint: () => {
    controller.sync();
    controller.setFills(stores.fills.forSymbol(currentSymbol));
    drawings.setDrawings(stores.drawings.forSymbol(currentSymbol));
    drawings.setBars(
      stores.bars.series(currentSymbol, tfRef.current).map((b) => Date.parse(b.bucketStart)),
      timeframeToMs(tfRef.current as Timeframe),
    );
  },
});
```

Add `interaction.dispose()` to the cleanup return (before/after `controller.dispose()`):

```ts
return () => { off(); offLink(); ro.disconnect(); interaction.dispose(); controller.dispose(); controllerRef.current = null; interactionRef.current = null; };
```

> Note: the mount effect deliberately depends only on `[config.id]`, so `activeTool`/`magnet` changes never re-create the chart or interaction — the rail (Task 9) drives them imperatively through `interactionRef.current`. `setActiveTool` is stable (React guarantees setState identity), so it is safe to close over inside the effect.

- [ ] **Step 5: Connect the store in `App.tsx` (bus + persistence + toast)**

In `ui/src/App.tsx`, add imports. **Note: `useEffect`, `useMemo`, and `useState` are already imported at line 1 (`import { useEffect, useMemo, useState } from "react";`) — do NOT re-import them or you'll get a duplicate-identifier error.** Add only:

```ts
import { BroadcastChannelDrawingBus } from "./render/chart/drawings/store";
import type { DrawingStore } from "./render/chart/drawings/store";
import { useToasts } from "./chrome/Toast";
```

Memoize `commands` so the bridge connects exactly once (replace the plain `const commands = {...}` with a `useMemo`):

```ts
const commands = useMemo(() => ({
  sendCommand: (name: string, args: unknown) => client.sendCommand(name, args),
  sendQuery: (name: string, args: unknown) => client.sendQuery(name, args),
}), [client]);
```

Add a bridge component (top-level in `App.tsx`) that connects the store from inside the `ToastProvider`:

```tsx
function DrawingsSyncBridge(
  { store, commands }: { store: DrawingStore; commands: { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }> } },
): null {
  const toast = useToasts();
  useEffect(() => {
    const off = store.connect({
      commands,
      bus: new BroadcastChannelDrawingBus(),
      onError: (reason) => toast.push({ level: "danger", text: `Drawings: ${reason}` }),
    });
    return off;
  }, [store, commands, toast]);
  return null;
}
```

Render it inside `<ToastProvider>` (as a sibling of the shell, so `useToasts()` resolves):

```tsx
<ToastProvider>
  <DrawingsSyncBridge store={stores.drawings} commands={commands} />
  {/* ...existing shell... */}
</ToastProvider>
```

- [ ] **Step 6: Run the tests + type-check**

Run: `cd ui && npx tsc --noEmit && npx vitest run src/chrome/panels/ChartPanel.test.tsx`
Expected: both new integration tests PASS; the existing ChartPanel tests still PASS.

- [ ] **Step 7: Full-suite check + commit**

Run: `cd ui && npx vitest run && npx tsc --noEmit`
Expected: full UI suite green (run on the **main checkout**, not a worktree, per the node-canvas fork-pool quirk).

```bash
git add ui/src/chrome/panels/ChartPanel.tsx ui/src/App.tsx ui/src/chrome/panels/ChartPanel.test.tsx
git commit -m "feat(ui/chart): wire drawings primitive + interaction + store sync into ChartPanel"
```

---

### Task 9: DrawingRail toolbar (Daylight Ledger tokens) + final wiring

A ~28px React overlay rail inside the chart host's left edge. Buttons top→bottom: **cursor · h-line · h-ray · trendline · ray · rect · measure · magnet (toggle) · trash**. Active tool + active magnet get the bronze (`--accent`) selected treatment. Trash deletes the selection, or (with no selection) opens a confirm popover to clear all drawings for the symbol.

**Files:**
- Create: `ui/src/chrome/panels/DrawingRail.tsx`
- Test: `ui/src/chrome/panels/DrawingRail.test.tsx`
- Modify: `ui/src/render/chart/drawings/interaction.ts` (+ `interaction.test.ts`) — add `hasSelection()` / `deleteSelection()`
- Modify: `ui/src/chrome/panels/ChartPanel.tsx` (render the rail + `magnet`/`chartSymbol` state)

**Interfaces:**
- Produces:
  - `DrawingInteraction.hasSelection(): boolean`, `DrawingInteraction.deleteSelection(): void`
  - `interface DrawingRailProps { activeTool: Tool; magnet: boolean; symbol: string; onSelectTool(t: Tool): void; onToggleMagnet(): void; hasSelection(): boolean; onDeleteSelection(): void; onClearAll(): void }`
  - `function DrawingRail(props: DrawingRailProps): JSX.Element`

- [ ] **Step 1: Add `hasSelection`/`deleteSelection` to the interaction (with tests)**

Append to `ui/src/render/chart/drawings/interaction.test.ts`, inside the existing `describe`:

```ts
  it("exposes selection state and deletes the selection imperatively", () => {
    const store = new DrawingStore();
    store.upsert({ id: "x", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 100 }], createdMs: 1, updatedMs: 1 });
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    expect(di.hasSelection()).toBe(false);
    fire("pointerdown", { clientX: 50, clientY: 901 }); // select the hline (y≈900)
    expect(di.hasSelection()).toBe(true);
    di.deleteSelection();
    expect(store.forSymbol("US.AAPL")).toHaveLength(0);
    expect(di.hasSelection()).toBe(false);
    expect(prim.setSelection).toHaveBeenLastCalledWith(null);
  });
```

Add the two methods to `DrawingInteraction` in `ui/src/render/chart/drawings/interaction.ts`:

```ts
  hasSelection(): boolean {
    return this.selectionId !== null;
  }

  deleteSelection(): void {
    if (!this.selectionId) return;
    this.store.remove(this.selectionId);
    this.selectionId = null;
    this.primitive.setSelection(null);
    this.primitive.requestUpdate();
  }
```

Run: `cd ui && npx vitest run src/render/chart/drawings/interaction.test.ts`
Expected: PASS (including the new case).

- [ ] **Step 2: Write the failing DrawingRail test**

Create `ui/src/chrome/panels/DrawingRail.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { DrawingRail } from "./DrawingRail";

beforeEach(() => cleanup());

function props(overrides?: Partial<Parameters<typeof DrawingRail>[0]>) {
  return {
    activeTool: "select" as const,
    magnet: true,
    symbol: "US.AAPL",
    onSelectTool: vi.fn(),
    onToggleMagnet: vi.fn(),
    hasSelection: vi.fn(() => false),
    onDeleteSelection: vi.fn(),
    onClearAll: vi.fn(),
    ...overrides,
  };
}

describe("DrawingRail", () => {
  it("renders one button per tool plus magnet and trash", () => {
    render(<DrawingRail {...props()} />);
    for (const label of ["select", "horizontal line", "horizontal ray", "trendline", "ray", "rectangle", "measure", "magnet", "delete"]) {
      expect(screen.getByLabelText(label)).toBeTruthy();
    }
  });

  it("selecting a tool calls onSelectTool", () => {
    const p = props();
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("trendline"));
    expect(p.onSelectTool).toHaveBeenCalledWith("trendline");
  });

  it("marks the active tool with aria-pressed", () => {
    render(<DrawingRail {...props({ activeTool: "rect" })} />);
    expect(screen.getByLabelText("rectangle").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByLabelText("select").getAttribute("aria-pressed")).toBe("false");
  });

  it("reflects and toggles magnet", () => {
    const p = props({ magnet: true });
    render(<DrawingRail {...p} />);
    expect(screen.getByLabelText("magnet").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByLabelText("magnet"));
    expect(p.onToggleMagnet).toHaveBeenCalledOnce();
  });

  it("trash deletes the selection when one exists (no popover)", () => {
    const p = props({ hasSelection: vi.fn(() => true) });
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("delete"));
    expect(p.onDeleteSelection).toHaveBeenCalledOnce();
    expect(screen.queryByText(/Clear all drawings/i)).toBeNull();
    expect(p.onClearAll).not.toHaveBeenCalled();
  });

  it("trash with no selection opens a confirm popover naming the symbol; confirm clears all", () => {
    const p = props({ hasSelection: vi.fn(() => false) });
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("delete"));
    expect(screen.getByText(/Clear all drawings for US\.AAPL/i)).toBeTruthy();
    fireEvent.click(screen.getByText("Clear"));
    expect(p.onClearAll).toHaveBeenCalledOnce();
  });

  it("cancel dismisses the confirm popover without clearing", () => {
    const p = props({ hasSelection: vi.fn(() => false) });
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("delete"));
    fireEvent.click(screen.getByText("Cancel"));
    expect(p.onClearAll).not.toHaveBeenCalled();
    expect(screen.queryByText(/Clear all drawings/i)).toBeNull();
  });
});
```

Run: `cd ui && npx vitest run src/chrome/panels/DrawingRail.test.tsx`
Expected: FAIL — cannot resolve `./DrawingRail`.

- [ ] **Step 3: Write the DrawingRail component**

Create `ui/src/chrome/panels/DrawingRail.tsx`:

```tsx
import { useState } from "react";
import type { Tool } from "../../render/chart/drawings/interaction";

export interface DrawingRailProps {
  activeTool: Tool;
  magnet: boolean;
  symbol: string;
  onSelectTool(t: Tool): void;
  onToggleMagnet(): void;
  hasSelection(): boolean;
  onDeleteSelection(): void;
  onClearAll(): void;
}

const TOOLS: { tool: Tool; label: string; glyph: string }[] = [
  { tool: "select", label: "select", glyph: "⌖" },
  { tool: "hline", label: "horizontal line", glyph: "─" },
  { tool: "hray", label: "horizontal ray", glyph: "╌►" },
  { tool: "trendline", label: "trendline", glyph: "╱" },
  { tool: "ray", label: "ray", glyph: "╱►" },
  { tool: "rect", label: "rectangle", glyph: "▭" },
  { tool: "measure", label: "measure", glyph: "↥" },
];

const railBtn = (active: boolean): React.CSSProperties => ({
  width: 24, height: 24, display: "flex", alignItems: "center", justifyContent: "center",
  fontSize: 13, lineHeight: 1, cursor: "pointer", borderRadius: 3,
  border: `1px solid ${active ? "var(--accent)" : "var(--border-strong)"}`,
  background: active ? "rgba(154,106,27,.08)" : "var(--bg)",
  color: active ? "var(--accent)" : "var(--text)",
});

export function DrawingRail(props: DrawingRailProps): JSX.Element {
  const { activeTool, magnet, symbol, onSelectTool, onToggleMagnet, hasSelection, onDeleteSelection, onClearAll } = props;
  const [confirmClear, setConfirmClear] = useState(false);

  const onTrash = () => {
    if (hasSelection()) { onDeleteSelection(); return; }
    setConfirmClear(true);
  };

  return (
    <div
      onPointerDown={(e) => e.stopPropagation()} // rail clicks must not reach the drawing pointer handlers
      style={{
        position: "absolute", left: 4, top: 4, zIndex: 5,
        display: "flex", flexDirection: "column", gap: 2, padding: 2,
        background: "var(--surface)", border: "1px solid var(--border-strong)", borderRadius: 4,
      }}
    >
      {TOOLS.map(({ tool, label, glyph }) => (
        <button key={tool} aria-label={label} aria-pressed={activeTool === tool}
          style={railBtn(activeTool === tool)} onClick={() => onSelectTool(tool)}>{glyph}</button>
      ))}
      <button aria-label="magnet" aria-pressed={magnet} style={railBtn(magnet)} onClick={onToggleMagnet}>🧲</button>
      <button aria-label="delete" aria-pressed={false} style={railBtn(false)} onClick={onTrash}>🗑</button>

      {confirmClear && (
        <div className="popover" role="dialog"
          style={{ position: "absolute", left: 30, bottom: 0, width: 200, padding: 8, fontSize: 12, zIndex: 6 }}>
          <div style={{ marginBottom: 8, color: "var(--text)" }}>Clear all drawings for {symbol}?</div>
          <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
            <button className="btn" onClick={() => setConfirmClear(false)}>Cancel</button>
            <button className="btn" style={{ borderColor: "var(--danger)", color: "var(--danger)" }}
              onClick={() => { onClearAll(); setConfirmClear(false); }}>Clear</button>
          </div>
        </div>
      )}
    </div>
  );
}
```

Run: `cd ui && npx vitest run src/chrome/panels/DrawingRail.test.tsx`
Expected: PASS (all cases).

- [ ] **Step 4: Render the rail inside ChartPanel + add `magnet`/`chartSymbol` state**

In `ui/src/chrome/panels/ChartPanel.tsx`, add the import and state (near the Task 8 additions):

```ts
import { DrawingRail } from "./DrawingRail";
// ...in the component body:
const [magnet, setMagnet] = useState(true);
const [chartSymbol, setChartSymbol] = useState(symbol); // `symbol` = config's initial symbol var
```

In the mount effect's `applySymbol`, mirror the live symbol into React state (append after `interactionRef.current?.onSymbolChanged();`):

```ts
  setChartSymbol(currentSymbol);
```

Render the rail as a child of the host div (replace the self-closing host div):

```tsx
<div ref={hostRef} style={{ flex: 1, minHeight: 0, position: "relative" }}>
  <DrawingRail
    activeTool={activeTool}
    magnet={magnet}
    symbol={chartSymbol}
    onSelectTool={(t) => { setActiveTool(t); interactionRef.current?.setTool(t); }}
    onToggleMagnet={() => { magnetRef.current = !magnetRef.current; setMagnet(magnetRef.current); }}
    hasSelection={() => interactionRef.current?.hasSelection() ?? false}
    onDeleteSelection={() => interactionRef.current?.deleteSelection()}
    onClearAll={() => stores.drawings.clearSymbol(chartSymbol)}
  />
</div>
```

> The rail is a React child of the same `position: relative` host div into which LWC imperatively appends its canvas; `z-index: 5` keeps it above the canvas and `onPointerDown` stopPropagation prevents rail clicks from placing anchors.

- [ ] **Step 5: Full suite + type-check + commit**

Run: `cd ui && npx tsc --noEmit && npx vitest run` (on the **main checkout**).
Expected: full UI suite green.

```bash
git add ui/src/chrome/panels/DrawingRail.tsx ui/src/chrome/panels/DrawingRail.test.tsx ui/src/render/chart/drawings/interaction.ts ui/src/render/chart/drawings/interaction.test.ts ui/src/chrome/panels/ChartPanel.tsx
git commit -m "feat(ui/chart): DrawingRail toolbar + wire tool/magnet/trash into ChartPanel"
```

- [ ] **Step 6: Manual end-to-end verification (real app, main checkout)**

Run the app (`ui` dev server + engine or mock) and verify against a live/replay chart:
1. Arm each tool from the rail; place h-line, h-ray, trendline, ray, rectangle. Confirm pan/zoom locks while armed and unlocks after commit (tool reverts to cursor).
2. Magnet on (default): placing/dragging near a bar O/H/L/C snaps price; toggle off → no snap.
3. Select a drawing, drag its body and a handle — confirm it live-updates on a second chart showing the same symbol (open a 1m + 10s of the same symbol).
4. `Delete` removes the selection; `Esc` cancels a placement / clears the measure box / deselects.
5. Measure: drag shows Δpoints / Δ% / bar-count; box persists after release until next pointerdown or `Esc`; never saved.
6. Trash with a selection deletes it; trash with none → confirm popover clears all for the symbol across every chart showing it.
7. Reload the app → drawings persist. Kill a save (simulate a blocked `SetConfig`) → a danger toast shows the reason; drawings stay in memory.

---

## Self-Review

**Spec coverage** — every spec section maps to a task:
- Data model (`model.ts`) + validator → Task 1. Geometry / cross-timeframe interpolation + right-extrapolation + magnet → Task 2. `DrawingStore` (rev, forSymbol/upsert/remove/clearSymbol) → Task 3; bus sync + echo-guard + debounced per-symbol persist + lazy load + save-failure → Task 4. `DrawingsPrimitive` (persisted + selection handles + ghost + measure, `zOrder:"top"`, captured `requestUpdate`) → Task 5. `DrawingInteraction` (select/armed/measure, pan-zoom lock, magnet, symbol-switch cancel, mutations→store) → Task 6. Facade additions → Task 7. ChartPanel/App wiring (paint `drawingsRev`, `ensureLoaded`, same-window shared store, cross-window bus, toast) → Task 8. Slim left rail w/ Daylight Ledger tokens, trash+confirm, no letter hotkeys → Task 9. Error handling (drop-on-load, save-failure toast, no-BroadcastChannel single-window) → Tasks 1/4/9. Testing (pure tables, store fakes, state-machine synthetic events, two-panel integration) → each task's tests.
- Non-goals (per-drawing style, ladder rendering, letter hotkeys, fibs/text/arrows/alerts) → correctly absent.

**Type consistency** — `Anchor`/`Drawing`/`DrawingKind` (model) flow unchanged through geometry/store/primitive/interaction. `DrawingsPrimitiveHandle` (primitive) is the exact surface the interaction consumes. `DrawingFacade` (interaction) is structurally satisfied by the four methods added to `ChartApiFacade` (Task 7). `DrawingBus`/`DrawingMsg` (store) match `FakeDrawingBus` (fakes) and `BroadcastChannelDrawingBus`. `Tool` (interaction) is the single source consumed by `DrawingRail` + ChartPanel state. Bar time is `Date.parse(bucketStart)` everywhere.

**No placeholders** — every code step carries complete, runnable code; every run step names the command + expected result.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-07-drawing-tools.md`.**

Phase 1 (Tasks 1–6) is greenfield and can start now, even in parallel with the Daylight Ledger redesign execution. Phase 2 (Tasks 7–9) is gated on that redesign landing on `main`; Task 7 opens with a re-verify step.

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Uses `superpowers:subagent-driven-development`.
2. **Inline Execution** — execute tasks in this session with checkpoints. Uses `superpowers:executing-plans`.

Which approach?
