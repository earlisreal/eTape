import { describe, it, expect } from "vitest";
import { ChartController, type BarReader, type IndicatorReader, type CommandSender } from "./ChartController";
import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import { LIGHT } from "../palette";
import type { Bar } from "../../wire/contract";
import { withDefaultParams } from "./indicatorSeries";

function fakeSeries(): LwcSeries & { calls: string[]; updates: unknown[]; setDataCalls: unknown[][]; orderCalls: number[] } {
  const calls: string[] = [];
  const updates: unknown[] = [];
  const setDataCalls: unknown[][] = [];
  const orderCalls: number[] = [];
  return {
    calls, updates, setDataCalls, orderCalls,
    setData: (data) => { calls.push("setData"); setDataCalls.push(data as unknown[]); },
    update: (bar) => { calls.push("update"); updates.push(bar); },
    applyOptions: () => calls.push("applyOptions"),
    setSeriesOrder: (order) => { calls.push("setSeriesOrder"); orderCalls.push(order); },
  };
}

function fakeFacade() {
  const created: Array<{ kind: string; pane: number; options: unknown; series: ReturnType<typeof fakeSeries> }> = [];
  const scaleMargins: Array<{ id: string; margins: { top: number; bottom: number } }> = [];
  const facade: ChartApiFacade & { created: typeof created; scrolls: number; bands: number; lastBands: unknown[]; scaleMargins: typeof scaleMargins } = {
    created, scrolls: 0, bands: 0, lastBands: [], scaleMargins,
    addSeries: (kind, o, pane) => { const s = fakeSeries(); created.push({ kind, pane, options: o, series: s }); return s; },
    removeSeries: () => {},
    setPriceScaleMargins: (id, margins) => { scaleMargins.push({ id, margins }); },
    setSessionBands: (b) => { facade.bands++; facade.lastBands = b; },
    setFillMarkers: () => {},
    timeToCoordinate: () => 0,
    priceToCoordinate: () => 0,
    logicalToCoordinate: () => 0,
    coordinateToLogical: () => 0,
    coordinateToPrice: () => 0,
    setPanZoomEnabled: () => {},
    scrollToRealTime: () => { facade.scrolls++; },
    resize: () => {},
    applyOptions: () => {},
    remove: () => {},
  };
  return facade;
}

const bar = (bucketStart: string, c: number, inProgress = false): Bar =>
  ({ symbol: "US.AAPL", timeframe: "1m", bucketStart, o: c, h: c, l: c, c, v: 100, inProgress });

function barReaderOf(bars: Bar[]): BarReader { return { series: () => bars }; }
// A timeframe-aware reader — needed to simulate a switch onto a timeframe
// whose series is empty/not-yet-arrived while another timeframe is populated
// (e.g. Daily seeded independently of a cold 1m symbol).
function barReaderByTf(byTf: Record<string, Bar[]>): BarReader {
  return { series: (_symbol, tf) => byTf[tf] ?? [] };
}
const emptyIndicators: IndicatorReader = { series: () => [] };
function indicatorReaderOf(points: { timeMs: number; value: number }[]): IndicatorReader {
  return { series: () => points };
}
function commandSpy(): CommandSender & { names: string[]; calls: Array<{ name: string; args: unknown }> } {
  const names: string[] = [];
  const calls: Array<{ name: string; args: unknown }> = [];
  return {
    names, calls,
    sendCommand: (n, a) => { names.push(n); calls.push({ name: n, args: a }); return Promise.resolve({ status: "accepted" }); },
  };
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

  it("mount confines the volume overlay to a bottom band so it never floods the candles", () => {
    const { facade } = make(barReaderOf([]));
    // The volume overlay scale ("") must get top-heavy margins (top ≥ 0.5, bottom 0)
    // so volume sits in a bottom band. Without this LWC's default margins let volume
    // autoscale across most of the pane, overlapping the candlesticks.
    const vol = facade.scaleMargins.find((m) => m.id === "");
    expect(vol).toBeDefined();
    expect(vol!.margins.bottom).toBe(0);
    expect(vol!.margins.top).toBeGreaterThanOrEqual(0.5);
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

  it("addIndicator draws thin lines behind the candle, with no per-indicator price line", () => {
    const { facade, ctrl } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    const line = facade.created.find((c) => c.kind === "line");
    expect(line?.options).toMatchObject({ lineWidth: 1, priceLineVisible: false });
    // Candle (created[0]) is lifted back to the top draw order after the indicator
    // is added, so it stays painted over the overlay line.
    const candle = facade.created[0].series;
    expect(candle.orderCalls.length).toBeGreaterThan(0);
    expect(candle.orderCalls.at(-1)).toBe(Number.MAX_SAFE_INTEGER);
  });

  it("removeIndicator re-lifts the candle above any remaining indicators", () => {
    const { facade, ctrl } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    const candle = facade.created[0].series;
    const before = candle.orderCalls.length;
    ctrl.removeIndicator("vwap-1");
    expect(candle.orderCalls.length).toBeGreaterThan(before);
  });

  it("addIndicator sends exactly one SubscribeIndicator with the controller's config; reload re-sends for the new symbol/timeframe", () => {
    const { ctrl, cmd } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    const subscribes = cmd.calls.filter((c) => c.name === "SubscribeIndicator");
    expect(subscribes).toHaveLength(1);
    expect(subscribes[0].args).toEqual({
      instanceId: "vwap-1", symbol: "US.AAPL", timeframe: "1m", type: "VWAP", params: withDefaultParams("VWAP", {}),
    });

    cmd.calls.length = 0;
    ctrl.setSymbol("US.NVDA");
    const resubscribes = cmd.calls.filter((c) => c.name === "SubscribeIndicator");
    expect(resubscribes).toHaveLength(1);
    expect(resubscribes[0].args).toEqual({
      instanceId: "vwap-1", symbol: "US.NVDA", timeframe: "1m", type: "VWAP", params: withDefaultParams("VWAP", {}),
    });
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
    // +2, not +1: setSymbol's resetForReload() clears the stale series with an
    // immediate setData([]) (so the old symbol's candles never linger on
    // screen), then the following sync() backfills the new symbol with a
    // second setData call.
    expect(candle.calls.filter((c) => c === "setData").length).toBe(setDataBefore + 2);
    expect(cmd.names).toContain("SubscribeIndicator"); // re-subscribed for the new symbol
  });

  it("switching timeframe to a not-yet-populated series clears the stale candles instead of freezing them", () => {
    // Regression test: Daily -> 1m used to leave the Daily candles on screen
    // when the 1m series was still empty (Daily can be seeded independently
    // of 1m, so this is the common case, not an edge case).
    const dailyBars = [bar("2026-07-05T00:00:00Z", 9), bar("2026-07-06T00:00:00Z", 10)];
    const reader = barReaderByTf({ D: dailyBars, "1m": [] });
    const facade = fakeFacade();
    const ctrl = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "D" },
      { bars: reader, indicators: emptyIndicators, commands: commandSpy() });
    ctrl.mount();
    ctrl.sync(); // backfill Daily
    const candle = facade.created[0].series;
    const volume = facade.created[1].series;
    expect(candle.setDataCalls.at(-1)).toHaveLength(2);

    ctrl.setTimeframe("1m"); // 1m series is currently empty
    // resetForReload() must clear immediately — before any sync() call —
    // so the Daily candles never remain frozen on screen.
    expect(candle.setDataCalls.at(-1)).toEqual([]);
    expect(volume.setDataCalls.at(-1)).toEqual([]);

    ctrl.sync(); // 1m is empty; applyBars early-returns and must not resurrect Daily's bars
    expect(candle.setDataCalls.at(-1)).toEqual([]);
  });

  it("indicator series: update() fast-path on growth; setSymbol reload forces a full setData again", () => {
    const points: { timeMs: number; value: number }[] = [{ timeMs: 1_000, value: 1 }];
    const { facade, ctrl } = make(barReaderOf([bar("2026-07-06T13:30:00Z", 10)]), commandSpy(), indicatorReaderOf(points));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    const ind = facade.created.find((c) => c.kind === "line")!.series;

    ctrl.sync(); // first application — full setData
    expect(ind.calls.filter((c) => c === "setData")).toHaveLength(1);
    expect(ind.calls).not.toContain("update");

    points.push({ timeMs: 2_000, value: 2 }); // one new point appended
    ctrl.sync();
    expect(ind.calls.filter((c) => c === "setData")).toHaveLength(1); // no additional full setData
    // 2, not 1: the growth loop also re-flushes the previously-last point (index 0,
    // unchanged here) alongside the genuinely new one, in case it was itself revised
    // during the same missed window — see the "growth that also revises..." test below.
    expect(ind.calls.filter((c) => c === "update")).toHaveLength(2);

    ctrl.setSymbol("US.NVDA"); // reload — indicatorApplied cleared
    ctrl.sync();
    expect(ind.calls.filter((c) => c === "setData")).toHaveLength(2); // full setData again post-reload
  });

  it("updateIndicator (param edit) does not reuse the stale applied count against the new re-added series", () => {
    const points: { timeMs: number; value: number }[] = [
      { timeMs: 1_000, value: 1 }, { timeMs: 2_000, value: 2 }, { timeMs: 3_000, value: 3 },
    ];
    const { facade, ctrl } = make(barReaderOf([]), commandSpy(), indicatorReaderOf(points));
    ctrl.addIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 9 } });
    ctrl.sync(); // full setData against the original series; indicatorApplied["ema-1"] = 3

    // Param edit → removeIndicator (old series discarded) + addIndicator (brand-new, empty
    // series under the SAME key, since key = instanceId for a single-slot indicator like EMA).
    // The engine recomputes and returns a same-or-greater-length series under that key.
    points.length = 0;
    points.push({ timeMs: 1_000, value: 10 }, { timeMs: 2_000, value: 20 }, { timeMs: 3_000, value: 30 });
    ctrl.updateIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 21 } });

    const lineSeries = facade.created.filter((c) => c.kind === "line");
    expect(lineSeries).toHaveLength(2); // old series (removed) + new series (re-added)
    const newSeries = lineSeries[1].series;

    ctrl.sync();
    // The new series must get the FULL array via setData — not zero calls (stale applied
    // count equals the new length, so the append loop would run zero iterations) and not
    // a partial tail-only update().
    expect(newSeries.calls.filter((c) => c === "setData")).toHaveLength(1);
    expect(newSeries.calls).not.toContain("update");
  });

  it("same-length in-progress-bar revision (last point's value changes) is applied via update(), not dropped", () => {
    const points: { timeMs: number; value: number }[] = [{ timeMs: 1_000, value: 1 }, { timeMs: 2_000, value: 2 }];
    const { facade, ctrl } = make(barReaderOf([]), commandSpy(), indicatorReaderOf(points));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    const ind = facade.created.find((c) => c.kind === "line")!.series;

    ctrl.sync(); // full setData, 2 points
    expect(ind.calls.filter((c) => c === "setData")).toHaveLength(1);

    // Same length, but IndicatorStore.apply() upserted the last point in place (the
    // in-progress bar's live value) — timeMs unchanged, value changed.
    points[1] = { timeMs: 2_000, value: 2.5 };
    ctrl.sync();

    expect(ind.calls.filter((c) => c === "update")).toHaveLength(1); // revision pushed via update()
    expect(ind.calls.filter((c) => c === "setData")).toHaveLength(1); // not a redundant full setData
  });

  it("growth that also revises the previously-last point (two deltas in one missed window) re-flushes both", () => {
    const points: { timeMs: number; value: number }[] = [{ timeMs: 1_000, value: 1 }];
    const { facade, ctrl } = make(barReaderOf([]), commandSpy(), indicatorReaderOf(points));
    ctrl.addIndicator({ instanceId: "vwap-1", type: "VWAP", params: {} });
    const ind = facade.created.find((c) => c.kind === "line")!.series;

    ctrl.sync(); // first application — full setData, applied = 1
    expect(ind.calls.filter((c) => c === "setData")).toHaveLength(1);

    // Two deltas land on IndicatorStore before the next rAF-coalesced sync:
    // 1) an upsert revises the first (in-progress-bar) point in place, then
    // 2) an append adds a new point for the next in-progress bar.
    points[0] = { timeMs: 1_000, value: 1.5 };
    points.push({ timeMs: 2_000, value: 2 });

    ctrl.sync();

    const updateValues = ind.updates as { time: number; value: number }[];
    expect(updateValues).toContainEqual({ time: 1, value: 1.5 }); // revised first point must reach the series
    expect(updateValues).toContainEqual({ time: 2, value: 2 });   // new second point must also reach the series
  });

  it("sync recomputes and sets session bands", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync();
    expect(facade.bands).toBeGreaterThan(0);
  });

  it("suppresses session bands on Daily but keeps them on intraday timeframes", () => {
    const bars = [bar("2026-07-06T13:30:00Z", 10), bar("2026-07-06T13:31:00Z", 11)];

    const facadeD = fakeFacade();
    const ctrlD = new ChartController(facadeD, LIGHT, { symbol: "US.AAPL", timeframe: "D" },
      { bars: barReaderOf(bars), indicators: emptyIndicators, commands: commandSpy() });
    ctrlD.mount();
    ctrlD.sync();
    expect(facadeD.lastBands).toEqual([]);

    const { facade: facade1m, ctrl: ctrl1m } = make(barReaderOf(bars));
    ctrl1m.sync();
    expect(facade1m.lastBands.length).toBeGreaterThan(0);
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
