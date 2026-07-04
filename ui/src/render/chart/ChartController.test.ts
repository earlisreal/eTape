import { describe, it, expect } from "vitest";
import { ChartController, type BarReader, type IndicatorReader, type CommandSender } from "./ChartController";
import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import { LIGHT } from "../palette";
import type { Bar } from "../../wire/contract";

function fakeSeries(): LwcSeries & { calls: string[] } {
  const calls: string[] = [];
  return { calls, setData: () => calls.push("setData"), update: () => calls.push("update"), applyOptions: () => calls.push("applyOptions") };
}

function fakeFacade() {
  const created: Array<{ kind: string; pane: number; series: LwcSeries & { calls: string[] } }> = [];
  let atRightEdge = true;
  const facade: ChartApiFacade & { created: typeof created; setRightEdge: (v: boolean) => void; scrolls: number; bands: number } = {
    created, scrolls: 0, bands: 0,
    setRightEdge: (v) => { atRightEdge = v; },
    addSeries: (kind, _o, pane) => { const s = fakeSeries(); created.push({ kind, pane, series: s }); return s; },
    removeSeries: () => {},
    setSessionBands: () => { facade.bands++; },
    setFillMarkers: () => {},
    timeToCoordinate: () => 0,
    priceToCoordinate: () => 0,
    scrollToRealTime: () => { facade.scrolls++; },
    isAtRightEdge: () => atRightEdge,
    resize: () => {},
    applyOptions: () => {},
    remove: () => {},
  };
  return facade;
}

const bar = (bucketStart: string, c: number, inProgress = false): Bar =>
  ({ symbol: "US.AAPL", timeframe: "1m", bucketStart, o: c, h: c, l: c, c, v: 100, inProgress });

function barReaderOf(bars: Bar[]): BarReader { return { series: () => bars }; }
const emptyIndicators: IndicatorReader = { series: () => [] };
function commandSpy(): CommandSender & { names: string[] } {
  const names: string[] = [];
  return { names, sendCommand: (n) => { names.push(n); return Promise.resolve({ status: "accepted" }); } };
}

const make = (reader: BarReader, cmd = commandSpy(), ind: IndicatorReader = emptyIndicators) => {
  const facade = fakeFacade();
  const ctrl = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" }, { bars: reader, indicators: ind, commands: cmd });
  ctrl.mount();
  return { facade, ctrl, cmd };
};

describe("ChartController", () => {
  it("mount creates a candle + volume series", () => {
    const { facade } = make(barReaderOf([]));
    expect(facade.created.map((c) => c.kind)).toEqual(["candle", "histogram"]);
  });

  it("first sync with backfill calls setData, not update", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10), bar("2026-07-06T13:31:00Z", 11)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    const candle = facade.created[0].series;
    expect(candle.calls).toContain("setData");
    expect(candle.calls).not.toContain("update");
  });

  it("second sync with only the last bar changed calls update once", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10, false), bar("2026-07-06T13:31:00Z", 11, true)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();                       // backfill → setData
    bars[1] = bar("2026-07-06T13:31:00Z", 11.5, true); // in-progress tick
    const candle = facade.created[0].series;
    const before = candle.calls.filter((c) => c === "update").length;
    ctrl.sync();
    const after = candle.calls.filter((c) => c === "update").length;
    expect(after - before).toBe(1);
    expect(candle.calls.filter((c) => c === "setData")).toHaveLength(1);
  });

  it("does not force-scroll when the user has scrolled back", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10, true)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    facade.setRightEdge(false);
    bars[0] = bar("2026-07-06T13:30:00Z", 10.5, true);
    ctrl.sync();
    expect(facade.scrolls).toBe(0);
    ctrl.jumpToLive();
    expect(facade.scrolls).toBe(1);
  });

  it("addIndicator subscribes and creates the descriptor series; remove unsubscribes", () => {
    const { facade, ctrl, cmd } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    expect(cmd.names).toContain("SubscribeIndicator");
    expect(facade.created.some((c) => c.kind === "line")).toBe(true);
    ctrl.removeIndicator("vwap-1");
    expect(cmd.names).toContain("UnsubscribeIndicator");
  });

  it("updateIndicator: param edit re-subscribes; color-only edit does not", () => {
    const { ctrl, cmd } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 9 } });
    cmd.names.length = 0;
    ctrl.updateIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 21 } }); // param change
    expect(cmd.names).toEqual(["UnsubscribeIndicator", "SubscribeIndicator"]);
    cmd.names.length = 0;
    ctrl.updateIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 21 }, colors: { line: "#123456" } });
    expect(cmd.names).toEqual([]); // color-only → applied in place, no re-subscribe
  });

  it("setSymbol re-backfills (next sync calls setData again) and re-subscribes indicators", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl, cmd } = make(barReaderOf(bars));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    ctrl.sync();
    const candle = facade.created[0].series;
    const setDataBefore = candle.calls.filter((c) => c === "setData").length;
    cmd.names.length = 0;
    ctrl.setSymbol("US.NVDA");
    ctrl.sync();
    expect(candle.calls.filter((c) => c === "setData").length).toBe(setDataBefore + 1);
    expect(cmd.names).toContain("SubscribeIndicator"); // re-subscribed for the new symbol
  });

  it("sync recomputes and sets session bands", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    expect(facade.bands).toBeGreaterThan(0);
  });

  it("sync on a cold (empty) series does not throw or setData", () => {
    const { facade, ctrl } = make(barReaderOf([]));
    expect(() => ctrl.sync()).not.toThrow();
    expect(facade.created[0].series.calls).not.toContain("setData");
  });

  it("multiple new buckets between two syncs are all pushed via update, not just the last", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync(); // backfill
    const candle = facade.created[0].series;
    const updatesBefore = candle.calls.filter((c) => c === "update").length;
    bars.push(bar("2026-07-06T13:31:00Z", 11));
    bars.push(bar("2026-07-06T13:32:00Z", 12));
    bars.push(bar("2026-07-06T13:33:00Z", 13, true));
    ctrl.sync(); // three new buckets appeared since the last sync
    const updatesAfter = candle.calls.filter((c) => c === "update").length;
    // 4, not 3: the loop re-flushes the previously-last bar (index 0, unchanged here)
    // alongside the 3 genuinely new bars, in case that bar itself changed during the
    // same missed window — see the "finalizes the previously-last bar" test below.
    expect(updatesAfter - updatesBefore).toBe(4);
  });

  it("growth that also finalizes the previously-last bar re-flushes that bar too", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10, true)]; // in-progress
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync(); // backfill
    const candle = facade.created[0].series;
    // Simulate a missed window: the previously-last bar finalizes AND a new bar appears.
    bars[0] = bar("2026-07-06T13:30:00Z", 10.5, false); // finalized, different close
    bars.push(bar("2026-07-06T13:31:00Z", 11, true));   // new bar
    const updatesBefore = candle.calls.filter((c) => c === "update").length;
    ctrl.sync();
    const updatesAfter = candle.calls.filter((c) => c === "update").length;
    expect(updatesAfter - updatesBefore).toBe(2); // both the finalized bar AND the new bar are pushed
  });
});
