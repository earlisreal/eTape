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
