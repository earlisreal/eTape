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
