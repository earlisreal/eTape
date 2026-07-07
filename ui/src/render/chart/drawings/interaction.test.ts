import { describe, it, expect, vi, beforeEach } from "vitest";
import { DrawingInteraction } from "./interaction";
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
    new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
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
    new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    fire("pointerdown", { clientX: 50, clientY: 300 }); // far from the line (y=900)
    expect(prim.setSelection).toHaveBeenLastCalledWith(null);
  });

  it("magnet snaps the placed price to the hovered bar's OHLC when enabled", () => {
    const store = new DrawingStore();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), fakePrimitive(), store, ctx(true), { newId });
    di.setTool("hline");
    // bar0 high=20 → y=980. Click at y=979 (1px away, within 6px, strictly nearer than
    // any other OHLC level of this bar) → snaps to 20.
    fire("pointerdown", { clientX: 0, clientY: 979 });
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
    const onToolChange = vi.fn();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, f, prim, store, ctx(), { newId, onToolChange });
    di.setTool("trendline");
    fire("pointerdown", { clientX: 0, clientY: 990 }); // anchor0 pending
    di.onSymbolChanged();
    fire("pointerdown", { clientX: 10, clientY: 980 }); // would-be 2nd click
    expect(store.forSymbol("US.AAPL")).toHaveLength(0); // placement was reset
    expect(prim.setSelection).toHaveBeenLastCalledWith(null);
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(true);
    expect(onToolChange).toHaveBeenLastCalledWith("select"); // armed tool reset, not left silently on trendline
  });

  it("Escape during a body drag restores pan/zoom", () => {
    const store = new DrawingStore();
    store.upsert({ id: "x", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 100 }], createdMs: 1, updatedMs: 1 });
    const f = fakeFacade();
    const { host, fire } = fakeHost();
    new DrawingInteraction(host, f, fakePrimitive(), store, ctx(), { newId });
    // hline at price 100 → y = 900. Click near its body (not the handle at x=0,y=900) to start a bodyDrag.
    fire("pointerdown", { clientX: 50, clientY: 901 });
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(false); // drag started, pan/zoom locked
    fire("keydown", { key: "Escape" });
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(true); // Escape restores pan/zoom even for a drag, not just a placement
  });

  it("Escape during an active measure gesture restores pan/zoom", () => {
    const store = new DrawingStore();
    const f = fakeFacade();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, f, fakePrimitive(), store, ctx(), { newId });
    di.setTool("measure");
    fire("pointerdown", { clientX: 0, clientY: 990 }); // starts measuring, locks pan/zoom
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(false);
    fire("keydown", { key: "Escape" });
    expect(f.setPanZoomEnabled).toHaveBeenLastCalledWith(true); // Escape restores pan/zoom for the current tool (measure → unlocked)
  });

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

  it("ignores a rail-originated pointerdown instead of deselecting", () => {
    const store = new DrawingStore();
    store.upsert({ id: "x", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 100 }], createdMs: 1, updatedMs: 1 });
    const prim = fakePrimitive();
    const { host, fire } = fakeHost();
    new DrawingInteraction(host, fakeFacade(), prim, store, ctx(), { newId });
    // hline at price 100 → y = 900. Click near it in select mode (mirrors the
    // "selects a drawing on click and deletes it with Delete" test above).
    fire("pointerdown", { clientX: 50, clientY: 901 });
    expect(prim.setSelection).toHaveBeenLastCalledWith("x");
    expect(prim.setSelection).toHaveBeenCalledTimes(1);

    // A leaked pointerdown from a rail button (e.g. Trash) at the rail's screen
    // position — far from the drawing — must NOT run the empty-space deselect.
    const railTarget = { closest: (sel: string) => (sel === "[data-drawing-rail]" ? {} : null) };
    fire("pointerdown", { clientX: 10, clientY: 10, target: railTarget as unknown as Element });
    expect(prim.setSelection).toHaveBeenCalledTimes(1); // no additional call — selection unchanged
    expect(prim.setSelection).toHaveBeenLastCalledWith("x");
  });

  it("an armed one-anchor tool ignores a rail-originated pointerdown (no spurious commit)", () => {
    const store = new DrawingStore();
    const { host, fire } = fakeHost();
    const di = new DrawingInteraction(host, fakeFacade(), fakePrimitive(), store, ctx(), { newId });
    di.setTool("hline");

    const railTarget = { closest: (sel: string) => (sel === "[data-drawing-rail]" ? {} : null) };
    fire("pointerdown", { clientX: 10, clientY: 10, target: railTarget as unknown as Element });
    expect(store.forSymbol("US.AAPL")).toHaveLength(0); // no spurious drawing committed

    // the tool is still armed and a real click still places the drawing normally
    fire("pointerdown", { clientX: 5, clientY: 900 });
    expect(store.forSymbol("US.AAPL")).toHaveLength(1);
  });
});
