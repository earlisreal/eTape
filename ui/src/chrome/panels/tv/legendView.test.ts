// ui/src/chrome/panels/tv/legendView.test.ts
import { describe, it, expect } from "vitest";
import { computeLegendView } from "./legendView";
import { LIGHT } from "../../../render/palette";
import type { Bar } from "../../../wire/contract";
import type { IndicatorReader } from "../../../render/chart/ChartController";

const bar = (bucketStart: string, o: number, c: number): Bar =>
  ({ symbol: "US.AAPL", timeframe: "1m", bucketStart, o, h: Math.max(o, c), l: Math.min(o, c), c, v: 1000, inProgress: false });
const bars = [bar("2026-07-08T13:30:00Z", 10, 11), bar("2026-07-08T13:31:00Z", 11, 10.5)];
const emptyReader: IndicatorReader = { series: () => [] };

describe("computeLegendView", () => {
  it("uses the last bar when logical is null", () => {
    const v = computeLegendView(bars, emptyReader, [], LIGHT, null);
    expect(v.c).toBe(10.5);
    expect(v.up).toBe(false);          // c < o
    expect(v.changePct).toBeCloseTo(((10.5 - 11) / 11) * 100);
    expect(v.volume).toBe(1000);
  });

  it("indexes by the crosshair logical", () => {
    const v = computeLegendView(bars, emptyReader, [], LIGHT, 0);
    expect(v.c).toBe(11);
    expect(v.up).toBe(true);
  });

  it("resolves indicator values + colors + a TV-style label", () => {
    const reader: IndicatorReader = { series: (k) => (k === "e1" ? [{ timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 10.7 }] : []) };
    const v = computeLegendView(bars, reader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, null);
    expect(v.indicators[0].label).toBe("EMA 9 close");
    expect(v.indicators[0].values).toEqual([10.7]);
    expect(v.indicators[0].colors).toEqual([LIGHT.indEma]);
    expect(v.indicators[0].paneIndex).toBe(0);
  });

  it("returns nulls for a cold (empty) series", () => {
    const v = computeLegendView([], emptyReader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, null);
    expect(v.c).toBeNull();
    expect(v.indicators[0].values).toEqual([null]);
  });

  it("MACD: signal is 'open' when the fast (macd) line is at/above the slow (signal) line", () => {
    const reader: IndicatorReader = {
      series: (k) => (k === "m1#macd" ? [{ timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 0.5 }]
        : k === "m1#signal" ? [{ timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 0.3 }] : []),
    };
    const v = computeLegendView(bars, reader, [{ instanceId: "m1", type: "MACD", params: { fast: 12, slow: 26, signal: 9 } }], LIGHT, null);
    expect(v.indicators[0].signal).toBe("open");
  });

  it("MACD: signal is 'close' when the fast (macd) line is below the slow (signal) line", () => {
    const reader: IndicatorReader = {
      series: (k) => (k === "m1#macd" ? [{ timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 0.2 }]
        : k === "m1#signal" ? [{ timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 0.3 }] : []),
    };
    const v = computeLegendView(bars, reader, [{ instanceId: "m1", type: "MACD", params: { fast: 12, slow: 26, signal: 9 } }], LIGHT, null);
    expect(v.indicators[0].signal).toBe("close");
  });

  it("MACD: signal is null when a value is missing (cold series)", () => {
    const v = computeLegendView(bars, emptyReader, [{ instanceId: "m1", type: "MACD", params: { fast: 12, slow: 26, signal: 9 } }], LIGHT, null);
    expect(v.indicators[0].signal).toBeNull();
  });

  it("non-MACD rows never get a signal", () => {
    const reader: IndicatorReader = { series: (k) => (k === "e1" ? [{ timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 10.7 }] : []) };
    const v = computeLegendView(bars, reader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, null);
    expect(v.indicators[0].signal).toBeNull();
  });
});

// valueAt's linear scan was replaced with a binary search (points are sorted
// ascending by timeMs — IndicatorStore's snapshot-sort + append-in-order
// behavior guarantees this). These tests exercise valueAt indirectly through
// computeLegendView (matching this file's own convention — valueAt itself is
// not exported), using a 4-bar series so `logical` can select a target time
// distinct from every point's own bucket, and pin down every boundary case
// the old linear scan handled: before the first point, strictly between two
// points, exactly on a point, after the last point, and an empty series.
describe("computeLegendView valueAt (binary search)", () => {
  const b4 = [
    bar("2026-07-08T13:29:00Z", 1, 1),
    bar("2026-07-08T13:30:00Z", 1, 1),
    bar("2026-07-08T13:31:00Z", 1, 1),
    bar("2026-07-08T13:32:00Z", 1, 1),
  ];

  it("target before the first point returns null", () => {
    const reader: IndicatorReader = { series: () => [{ timeMs: Date.parse("2026-07-08T13:30:00Z"), value: 5 }] };
    // logical 0 -> bar0 @ :29, strictly before the only point @ :30.
    const v = computeLegendView(b4, reader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, 0);
    expect(v.indicators[0].values).toEqual([null]);
  });

  it("target strictly between two points returns the earlier point's value", () => {
    const reader: IndicatorReader = {
      series: () => [
        { timeMs: Date.parse("2026-07-08T13:29:00Z"), value: 1 },
        { timeMs: Date.parse("2026-07-08T13:32:00Z"), value: 2 },
      ],
    };
    // logical 2 -> bar2 @ :31, strictly between the two points @ :29 and :32.
    const v = computeLegendView(b4, reader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, 2);
    expect(v.indicators[0].values).toEqual([1]);
  });

  it("target exactly on a point's timeMs returns that point's value", () => {
    const reader: IndicatorReader = {
      series: () => [
        { timeMs: Date.parse("2026-07-08T13:29:00Z"), value: 1 },
        { timeMs: Date.parse("2026-07-08T13:30:00Z"), value: 2 },
        { timeMs: Date.parse("2026-07-08T13:31:00Z"), value: 3 },
      ],
    };
    // logical 2 -> bar2 @ :31, exactly the 3rd point's timeMs.
    const v = computeLegendView(b4, reader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, 2);
    expect(v.indicators[0].values).toEqual([3]);
  });

  it("target after the last point returns the last point's value", () => {
    const reader: IndicatorReader = { series: () => [{ timeMs: Date.parse("2026-07-08T13:29:00Z"), value: 1 }] };
    // logical 3 -> bar3 @ :32, after the only point @ :29.
    const v = computeLegendView(b4, reader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, 3);
    expect(v.indicators[0].values).toEqual([1]);
  });

  it("an empty points array returns null", () => {
    const v = computeLegendView(b4, emptyReader, [{ instanceId: "e1", type: "EMA", params: { period: 9 } }], LIGHT, 0);
    expect(v.indicators[0].values).toEqual([null]);
  });
});
