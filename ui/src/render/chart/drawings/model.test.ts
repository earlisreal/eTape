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
