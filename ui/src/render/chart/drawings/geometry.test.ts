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
    expect(snapToLevels(96, levels, 6)).toBe(10);
  });
  it("returns null when no level is within tolerance", () => {
    expect(snapToLevels(70, levels, 6)).toBeNull();
  });
  it("prefers the closest when two are within tolerance", () => {
    expect(snapToLevels(95, levels, 6)).toBe(10); // 90 is 5 away, 100 is 5 away — tie → first-encountered wins (10)
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
  it("hits a trendline body near the segment", () => {
    expect(hitTest("trendline", [{ x: 0, y: 48 }, { x: 100, y: 48 }], cursor, 400)).toEqual({ type: "body" });
  });
  it("hits an extendedline body along its forward extension", () => {
    // line through (0,0) and (10,10): at x=50 the line is at y=50
    expect(hitTest("extendedline", [{ x: 0, y: 0 }, { x: 10, y: 10 }], cursor, 400)).toEqual({ type: "body" });
  });
  it("hits an extendedline body along its backward extension too (unlike a ray)", () => {
    // same line through (0,0) and (10,10), extended backward: at x=-50,y=-50 relative
    // to the anchors — probe a point on the backward half instead, e.g. (-40,-40)
    // clamped into the 400-wide pane: extendToEdge(p1,p0,400) backward endpoint is (0,0)
    // itself here (dx>0 from p1 back to p0 hits x=0 exactly), so probe just past it
    // using a shifted line: anchors (200,200) and (210,210) — backward hits x=0,y=0.
    expect(hitTest("extendedline", [{ x: 200, y: 200 }, { x: 210, y: 210 }], { x: 5, y: 5 }, 400)).toEqual({ type: "body" });
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
