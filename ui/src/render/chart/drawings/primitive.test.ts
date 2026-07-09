import { describe, it, expect, vi } from "vitest";
import { DrawingsPrimitive } from "./primitive";
import { LIGHT } from "../../palette";
import type { Drawing } from "./model";
import { DEFAULT_DRAWING_WIDTH, DEFAULT_LINE_STYLE } from "./model";
import { LINE_DASH } from "../lineStyle";

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

  it("draws an extendedline through both anchors to the pane edge in both directions", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    // Anchors deliberately away from x=0/x=width so both extensions are visibly
    // past the anchors, not degenerate with an edge (unlike x=0-anchored hline tests).
    // t=15000 -> logical 0.25 -> x=2.5, price 20 -> y=980.
    // t=45000 -> logical 0.75 -> x=7.5, price 10 -> y=990.
    const line: Drawing = { id: "e", symbol: "US.AAPL", kind: "extendedline", anchors: [{ timeMs: 15_000, price: 20 }, { timeMs: 45_000, price: 10 }], createdMs: 1, updatedMs: 1 };
    p.setDrawings([line]);
    const { ctx, calls } = recordingCtx();
    draw(p, ctx);
    // Backward extension (past the first anchor, toward x=0): y=975.
    expect(calls).toContainEqual(["moveTo", 0, 975]);
    // Forward extension (past the second anchor, toward x=400/width): y=1775.
    expect(calls).toContainEqual(["lineTo", 400, 1775]);
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

// Captures strokeStyle / lineWidth / dash at each stroke, which recordingCtx() doesn't.
function styleCtx() {
  const strokes: { color: string; width: number; dash: number[] }[] = [];
  let dash: number[] = [];
  const ctx: any = {
    beginPath() {}, moveTo() {}, lineTo() {},
    stroke() { strokes.push({ color: ctx.strokeStyle, width: ctx.lineWidth, dash: [...dash] }); },
    strokeRect() { strokes.push({ color: ctx.strokeStyle, width: ctx.lineWidth, dash: [...dash] }); },
    fillRect() {}, fillText() {}, setLineDash(d: number[]) { dash = d; }, save() {}, restore() {},
    strokeStyle: "", fillStyle: "", lineWidth: 0, font: "", globalAlpha: 1, textBaseline: "",
  };
  return { ctx, strokes };
}

describe("DrawingsPrimitive per-drawing style + hide-all", () => {
  it("strokes a drawing with its own color, width, and dash", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([{ ...hline, color: "#2962FF", width: 3, lineStyle: "dashed" }]);
    const { ctx, strokes } = styleCtx();
    draw(p, ctx);
    expect(strokes[0].color).toBe("#2962FF");
    expect(strokes[0].width).toBe(3);
    expect(strokes[0].dash.length).toBeGreaterThan(0);
  });

  it("falls back to palette text color and default width when unstyled", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([hline]);
    const { ctx, strokes } = styleCtx();
    draw(p, ctx);
    expect(strokes[0].color).toBe(LIGHT.text);
    expect(strokes[0].width).toBe(1);
    expect(strokes[0].dash).toEqual([]);
  });

  it("setHideAll(true) skips all committed drawings", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([hline]);
    p.setHideAll(true);
    const { ctx, strokes } = styleCtx();
    draw(p, ctx);
    expect(strokes).toHaveLength(0);
  });

  it("setHideAll(true) still renders the active placement ghost", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([hline]);
    p.setHideAll(true);
    p.setTransient({ ghost: { kind: "trendline", anchors: [{ timeMs: 0, price: 20 }, { timeMs: 60_000, price: 10 }] } });
    const { ctx, strokes } = styleCtx();
    draw(p, ctx);
    // Only the ghost's stroke should fire: the committed hline stays hidden behind hideAll,
    // while the in-progress placement preview renders unconditionally.
    expect(strokes).toHaveLength(1);
    expect(strokes[0].color).toBe(LIGHT.accent);
    expect(strokes[0].dash).toEqual([4, 3]);
  });

  it("applies a partial style override (color only) and falls back to defaults for width/lineStyle", () => {
    const p = new DrawingsPrimitive(LIGHT);
    attach(p);
    p.setDrawings([{ ...hline, color: "#2962FF" }]);
    const { ctx, strokes } = styleCtx();
    draw(p, ctx);
    expect(strokes[0].color).toBe("#2962FF");
    expect(strokes[0].width).toBe(DEFAULT_DRAWING_WIDTH);
    expect(strokes[0].dash).toEqual(LINE_DASH[DEFAULT_LINE_STYLE]);
  });
});
