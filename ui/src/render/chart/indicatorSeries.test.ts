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

// NOTE: `describeIndicator`, `withDefaultParams`, `INDICATOR_CATALOG`, and `LIGHT` are
// ALREADY imported at the top of this file — do NOT re-import them (a duplicate binding
// is a parse-time SyntaxError). Just append this describe block.

describe("describeIndicator style resolution", () => {
  it("resolves width/lineStyle from styles, falling back to defaults", () => {
    const d = describeIndicator({ instanceId: "e1", type: "EMA", params: { period: 9 } }, LIGHT);
    expect(d[0].width).toBeTypeOf("number");
    expect(d[0].lineStyle).toBe("solid");
    expect(d[0].color).toBe(LIGHT.indEma);
  });

  it("applies per-slot style overrides", () => {
    const d = describeIndicator(
      { instanceId: "e1", type: "EMA", params: { period: 9 }, styles: { line: { color: "#123456", width: 4, lineStyle: "dashed" } } },
      LIGHT,
    );
    expect(d[0].color).toBe("#123456");
    expect(d[0].width).toBe(4);
    expect(d[0].lineStyle).toBe("dashed");
  });

  it("styles override legacy colors", () => {
    const d = describeIndicator(
      { instanceId: "e1", type: "EMA", params: { period: 9 }, colors: { line: "#aaaaaa" }, styles: { line: { color: "#bbbbbb" } } },
      LIGHT,
    );
    expect(d[0].color).toBe("#bbbbbb");
  });
});
