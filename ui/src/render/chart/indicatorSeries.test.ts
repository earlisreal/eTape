import { describe, it, expect } from "vitest";
import { describeIndicator, withDefaultParams, INDICATOR_CATALOG } from "./indicatorSeries";
import { LIGHT } from "../palette";

describe("indicator catalog", () => {
  it("exposes editable params with defaults + bounds for parameterized types", () => {
    expect(INDICATOR_CATALOG.EMA.params).toEqual([{ key: "period", label: "Period", default: 9, min: 1, max: 400 }]);
    expect(INDICATOR_CATALOG.MACD.params.map((p) => p.key)).toEqual(["fast", "slow", "signal"]);
    expect(INDICATOR_CATALOG.VWAP.params).toEqual([]); // VWAP has no params
  });

  it("withDefaultParams fills missing params from the catalog, keeping user overrides", () => {
    expect(withDefaultParams("EMA")).toEqual({ period: 9 });
    expect(withDefaultParams("EMA", { period: 21 })).toEqual({ period: 21 });
    expect(withDefaultParams("MACD", { fast: 8 })).toEqual({ fast: 8, slow: 26, signal: 9 });
  });
});

describe("describeIndicator", () => {
  it("overlays VWAP/EMA/SMA as single-slot lines on the main pane (0), palette-defaulted", () => {
    for (const [type, color] of [["VWAP", LIGHT.indVwap], ["EMA", LIGHT.indEma], ["SMA", LIGHT.indSma]] as const) {
      const [d] = describeIndicator({ instanceId: `${type}-1`, type, params: {} }, LIGHT);
      expect(d).toMatchObject({ kind: "line", paneIndex: 0, slot: "line", color, key: `${type}-1` });
    }
  });

  it("routes VOLUME to a histogram on the main pane", () => {
    expect(describeIndicator({ instanceId: "vol", type: "VOLUME", params: {} }, LIGHT)[0].kind).toBe("histogram");
  });

  it("MACD yields three slots in the sub-pane (1), each palette-defaulted and #slot-keyed", () => {
    const ds = describeIndicator({ instanceId: "macd", type: "MACD", params: {} }, LIGHT);
    expect(ds.map((d) => d.slot)).toEqual(["macd", "signal", "hist"]);
    expect(ds.map((d) => d.key)).toEqual(["macd#macd", "macd#signal", "macd#hist"]);
    expect(ds.every((d) => d.paneIndex === 1)).toBe(true);
    expect([ds[0].color, ds[1].color, ds[2].color]).toEqual([LIGHT.indMacdLine, LIGHT.indMacdSignal, LIGHT.indMacdHist]);
  });

  it("honors a per-slot color override, even for one of MACD's three series", () => {
    const [d] = describeIndicator({ instanceId: "ema", type: "EMA", params: {}, colors: { line: "#123456" } }, LIGHT);
    expect(d.color).toBe("#123456");
    const macd = describeIndicator({ instanceId: "m", type: "MACD", params: {}, colors: { signal: "#abcdef" } }, LIGHT);
    expect(macd.find((d) => d.slot === "signal")!.color).toBe("#abcdef");
    expect(macd.find((d) => d.slot === "macd")!.color).toBe(LIGHT.indMacdLine); // others stay palette-default
  });
});
