import { describe, it, expect } from "vitest";
import { ChartController, LEFT_PAD_BARS, type BarReader, type IndicatorReader, type CommandSender } from "./ChartController";
import type { ChartApiFacade, LwcSeries } from "./ChartApiFacade";
import { LIGHT, DARK } from "../palette";
import type { Bar } from "../../wire/contract";
import { withDefaultParams } from "./indicatorSeries";
import type { Band } from "./sessions";

function fakeSeries(): LwcSeries & { calls: string[]; updates: unknown[]; setDataCalls: unknown[][]; orderCalls: number[]; optionCalls: unknown[] } {
  const calls: string[] = [];
  const updates: unknown[] = [];
  const setDataCalls: unknown[][] = [];
  const orderCalls: number[] = [];
  const optionCalls: unknown[] = [];
  return {
    calls, updates, setDataCalls, orderCalls, optionCalls,
    setData: (data) => { calls.push("setData"); setDataCalls.push(data as unknown[]); },
    update: (bar) => { calls.push("update"); updates.push(bar); },
    applyOptions: (o) => { calls.push("applyOptions"); optionCalls.push(o); },
    setSeriesOrder: (order) => { calls.push("setSeriesOrder"); orderCalls.push(order); },
  };
}

function fakeFacade() {
  const created: Array<{ kind: string; pane: number; options: unknown; series: ReturnType<typeof fakeSeries> }> = [];
  const scaleMargins: Array<{ id: string; margins: { top: number; bottom: number } }> = [];
  const stretchFactors = new Map<number, number>();
  const facade: ChartApiFacade & { created: typeof created; scrolls: number; resets: number; priceResets: number; bands: number; lastBands: unknown[]; scaleMargins: typeof scaleMargins }
    & { mainKind: string; screenshots: number; crosshairCb: ((l: number | null) => void) | null }
    & { watermark: string | null; lastOptions: unknown; stretchFactors: typeof stretchFactors } = {
    created, scrolls: 0, resets: 0, priceResets: 0, bands: 0, lastBands: [], scaleMargins,
    mainKind: "", screenshots: 0, crosshairCb: null,
    watermark: null, lastOptions: null, stretchFactors,
    setMainSeries: (kind, o) => { const s = fakeSeries(); created.push({ kind, pane: 0, options: o, series: s }); facade.mainKind = kind; return s; },
    takeScreenshot: () => { facade.screenshots++; return {} as unknown as HTMLCanvasElement; },
    subscribeCrosshairMove: (cb) => { facade.crosshairCb = cb; return () => { facade.crosshairCb = null; }; },
    paneHeights: () => [400, 120],
    paneStretchFactor: (i) => stretchFactors.get(i) ?? 1,
    setPaneStretchFactor: (i, f) => { stretchFactors.set(i, f); },
    priceScaleWidth: () => 60,
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
    resetTimeScale: () => { facade.resets++; },
    resetPriceScale: () => { facade.priceResets++; },
    resize: () => {},
    applyOptions: (o) => { facade.lastOptions = o; },
    setWatermark: (t) => { facade.watermark = t; },
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

  it("resetZoom resets the time scale to default spacing + the latest bar, and re-enables price autoScale", () => {
    const { facade, ctrl } = make(barReaderOf([]));
    expect(facade.resets).toBe(0);
    expect(facade.priceResets).toBe(0);
    ctrl.resetZoom();
    expect(facade.resets).toBe(1);
    expect(facade.priceResets).toBe(1);
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

  it("main-pane overlay lines (EMA/SMA/VWAP) opt out of the candle scale's autoscale", () => {
    // A far-off indicator value (e.g. an EMA computed over reverse-split-adjusted
    // history) must never expand the candle price scale and crush the candles.
    const { facade, ctrl } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "ema-1", type: "EMA", params: { period: 200 } });
    const line = facade.created.find((c) => c.kind === "line");
    const options = line?.options as { autoscaleInfoProvider?: () => { priceRange: unknown } };
    expect(options.autoscaleInfoProvider).toBeTypeOf("function");
    expect(options.autoscaleInfoProvider!()).toEqual({ priceRange: null });
  });

  it("MACD's sub-pane lines keep autoscaling their own pane (no priceRange override)", () => {
    const { facade, ctrl } = make(barReaderOf([]));
    ctrl.addIndicator({ instanceId: "macd-1", type: "MACD", params: { fast: 12, slow: 26, signal: 9 } });
    const subpaneLines = facade.created.filter((c) => c.kind === "line" && c.pane === 1);
    expect(subpaneLines.length).toBeGreaterThan(0);
    for (const c of subpaneLines) {
      expect((c.options as { autoscaleInfoProvider?: unknown }).autoscaleInfoProvider).toBeUndefined();
    }
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
    // 2 real bars + LEFT_PAD_BARS leading whitespace points (Bug 4: farthest-left
    // pan leaves empty margin before the earliest real bar, mirroring rightOffset).
    expect(candle.setDataCalls.at(-1)).toHaveLength(2 + LEFT_PAD_BARS);

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

  it("shades pre-market even when the session boundary isn't an exact bar time (60m regression)", () => {
    // 60m buckets anchor at 09:30 ET, so pre-market buckets fall at 03:30/04:30/… —
    // 04:00 (the wall-clock PRE boundary) is never an exact 60m bar time. Resolving
    // band edges against that wall-clock boundary (the old sessionBands(from,to))
    // made LWC's timeToCoordinate return null for a time no bar has, silently
    // dropping the whole band — this is the "60m shows no pre-market shading" bug.
    // Bands must instead resolve on the bars' own bucket times.
    const bars = [
      bar("2026-07-06T08:30:00Z", 10), // 04:30 ET — a pre-market 60m bucket
      bar("2026-07-06T13:30:00Z", 11), // 09:30 ET — the RTH-open 60m bucket
    ];
    const facade = fakeFacade();
    const ctrl = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "60m" },
      { bars: barReaderOf(bars), indicators: emptyIndicators, commands: commandSpy() });
    ctrl.mount();
    ctrl.sync();
    const bands = facade.lastBands as Band[];
    const pre = bands.find((b) => b.session === "pre");
    expect(pre).toBeDefined();
    // The band's start is the bar's own time (an exact bar → timeToCoordinate never
    // returns null), not the 04:00 ET wall-clock boundary the bar doesn't sit on.
    expect(pre!.startMs).toBe(Date.parse("2026-07-06T08:30:00Z"));
    expect(pre!.endMs).toBe(Date.parse("2026-07-06T13:30:00Z"));
    const rth = bands.find((b) => b.session === "rth");
    expect(rth).toBeDefined();
    expect(rth!.startMs).toBe(Date.parse("2026-07-06T13:30:00Z"));
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

  it("falls back to a full setData rebuild instead of an out-of-order update() when the series isn't sorted", () => {
    // Regression for the 10s-chart freeze: series.update() requires a non-decreasing
    // time; feeding it a bucket earlier than one already applied used to throw and,
    // under the old Scheduler, permanently kill this chart's paint surface.
    const bars = [bar("2026-07-06T13:30:00Z", 10)];
    const { facade, ctrl } = make(barReaderOf(bars));
    ctrl.sync(); // backfill via setData
    const candle = facade.created[0].series;
    const setDataBefore = candle.calls.filter((c) => c === "setData").length;
    // Grew from 1 to 3 bars, but the tail isn't sorted (13:31 comes after 13:32).
    bars.push(bar("2026-07-06T13:32:00Z", 12));
    bars.push(bar("2026-07-06T13:31:00Z", 11));
    ctrl.sync();
    expect(candle.calls.filter((c) => c === "setData").length).toBe(setDataBefore + 1); // full rebuild
    expect(candle.setDataCalls.at(-1)).toHaveLength(3 + LEFT_PAD_BARS); // + leading whitespace pad
  });
});

describe("ChartController main series + facade capabilities", () => {
  it("mount creates the main series via setMainSeries (kind 'candle') and the volume via addSeries", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    expect(facade.mainKind).toBe("candle");
    // created[0] is the main (candle), created[1] is the volume histogram
    expect(facade.created[0].kind).toBe("candle");
    expect(facade.created[1].kind).toBe("histogram");
  });

  it("exposes screenshot, crosshair subscription, and pane heights", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    expect(facade.paneHeights()).toEqual([400, 120]);
    const dispose = facade.subscribeCrosshairMove(() => {});
    expect(facade.crosshairCb).toBeTypeOf("function");
    dispose();
    expect(facade.crosshairCb).toBeNull();
  });
});

describe("ChartController.setChartType", () => {
  const bars = [bar("2026-07-08T13:30:00Z", 10), bar("2026-07-08T13:31:00Z", 11)];

  it("recreates the main series with the new kind", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf(bars), indicators: emptyIndicators, commands: commandSpy() });
    c.mount(); c.sync();
    c.setChartType("line");
    expect(facade.mainKind).toBe("line");
  });

  it("feeds line/area main series {time,value}, and candle/bar main series OHLC", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf(bars), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    c.setChartType("line"); c.sync();
    const lineMain = facade.created[facade.created.length - 1].series;
    const lineData = lineMain.setDataCalls[lineMain.setDataCalls.length - 1] as Array<Record<string, unknown>>;
    const lineReal = lineData.filter((d) => "value" in d);
    expect(lineReal[0]).toHaveProperty("value", 10);
    expect(lineReal[0]).not.toHaveProperty("open");

    c.setChartType("candle"); c.sync();
    const candleMain = facade.created[facade.created.length - 1].series;
    const candleData = candleMain.setDataCalls[candleMain.setDataCalls.length - 1] as Array<Record<string, unknown>>;
    const candleReal = candleData.filter((d) => "close" in d);
    expect(candleReal[0]).toMatchObject({ open: 10, high: 10, low: 10, close: 10 });
  });

  it("is a no-op when the type is unchanged", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf(bars), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    const before = facade.created.length;
    c.setChartType("candle");
    expect(facade.created.length).toBe(before);
  });
});

describe("ChartController indicator hidden + style", () => {
  it("creates a hidden indicator series with visible:false", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    c.addIndicator({ instanceId: "e1", type: "EMA", params: { period: 9 }, hidden: true });
    const emaSeries = facade.created.find((x) => (x.options as { color?: string }).color === LIGHT.indEma);
    expect((emaSeries!.options as { visible?: boolean }).visible).toBe(false);
  });

  it("toggling hidden applies visible in place without re-subscribing", () => {
    const facade = fakeFacade();
    const cmd = commandSpy();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: cmd });
    c.mount();
    c.addIndicator({ instanceId: "e1", type: "EMA", params: { period: 9 } });
    const subsBefore = cmd.names.filter((n) => n === "SubscribeIndicator").length;
    c.updateIndicator({ instanceId: "e1", type: "EMA", params: { period: 9 }, hidden: true });
    const subsAfter = cmd.names.filter((n) => n === "SubscribeIndicator").length;
    expect(subsAfter).toBe(subsBefore); // no re-subscribe on a hidden toggle
    const emaSeries = facade.created.find((x) => (x.options as { color?: string }).color === LIGHT.indEma)!.series;
    expect(emaSeries.optionCalls.some((o) => (o as { visible?: boolean }).visible === false)).toBe(true);
  });

  it("every indicator series is created with lastValueVisible:false (no price-axis highlight)", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    c.addIndicator({ instanceId: "e1", type: "EMA", params: { period: 9 } });
    c.addIndicator({ instanceId: "m1", type: "MACD", params: withDefaultParams("MACD") });
    // Every series but the candle (created via setMainSeries, kind "candle") is either
    // the always-on volume overlay or an indicator — both must suppress the axis label.
    const nonCandle = facade.created.filter((x) => x.kind !== "candle");
    expect(nonCandle.length).toBeGreaterThan(0);
    for (const s of nonCandle) {
      expect((s.options as { lastValueVisible?: boolean }).lastValueVisible).toBe(false);
    }
  });

  it("MACD's histogram slot can be hidden independently via styles.hist.hidden, at creation", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    c.addIndicator({ instanceId: "m1", type: "MACD", params: withDefaultParams("MACD"), styles: { hist: { hidden: true } } });
    const hist = facade.created.find((x) => (x.options as { color?: string }).color === LIGHT.indMacdHist)!;
    const macdLine = facade.created.find((x) => (x.options as { color?: string }).color === LIGHT.indMacdLine)!;
    expect((hist.options as { visible?: boolean }).visible).toBe(false);
    expect((macdLine.options as { visible?: boolean }).visible).toBe(true);
  });

  it("MACD's histogram slot can be hidden independently via styles.hist.hidden, applied in place on update", () => {
    const facade = fakeFacade();
    const cmd = commandSpy();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: cmd });
    c.mount();
    const params = withDefaultParams("MACD");
    c.addIndicator({ instanceId: "m1", type: "MACD", params });
    const subsBefore = cmd.names.filter((n) => n === "SubscribeIndicator").length;
    c.updateIndicator({ instanceId: "m1", type: "MACD", params, styles: { hist: { hidden: true } } });
    expect(cmd.names.filter((n) => n === "SubscribeIndicator").length).toBe(subsBefore); // style-only, no re-subscribe
    const hist = facade.created.find((x) => (x.options as { color?: string }).color === LIGHT.indMacdHist)!.series;
    const macdLine = facade.created.find((x) => (x.options as { color?: string }).color === LIGHT.indMacdLine)!.series;
    expect(hist.optionCalls.some((o) => (o as { visible?: boolean }).visible === false)).toBe(true);
    expect(macdLine.optionCalls.every((o) => (o as { visible?: boolean }).visible !== false)).toBe(true);
  });
});

describe("ChartController.setPaneCollapsed", () => {
  it("collapses a pane to the small stretch floor, and expand restores the prior factor", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    facade.stretchFactors.set(1, 2.5); // pane already resized by the user before collapsing
    c.setPaneCollapsed(1, true);
    expect(facade.stretchFactors.get(1)).toBeLessThan(0.5);
    c.setPaneCollapsed(1, false);
    expect(facade.stretchFactors.get(1)).toBe(2.5);
  });

  it("expanding a pane that was never collapsed falls back to the LWC default factor of 1", () => {
    const facade = fakeFacade();
    const c = new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf([]), indicators: emptyIndicators, commands: commandSpy() });
    c.mount();
    c.setPaneCollapsed(1, false);
    expect(facade.stretchFactors.get(1)).toBe(1);
  });
});

describe("ChartController chart settings", () => {
  const bars = [bar("2026-07-08T13:30:00Z", 10)];
  const mk = (facade: ReturnType<typeof fakeFacade>) =>
    new ChartController(facade, LIGHT, { symbol: "US.AAPL", timeframe: "1m" },
      { bars: barReaderOf(bars), indicators: emptyIndicators, commands: commandSpy() });

  it("setShowSessions(false) clears session bands on the next sync", () => {
    const facade = fakeFacade(); const c = mk(facade); c.mount();
    c.setShowSessions(false); c.sync();
    expect(facade.lastBands).toEqual([]);
  });

  it("setVolumeVisible(false) hides the volume series", () => {
    const facade = fakeFacade(); const c = mk(facade); c.mount();
    c.setVolumeVisible(false);
    const vol = facade.created.find((x) => x.kind === "histogram")!.series;
    expect(vol.optionCalls.some((o) => (o as { visible?: boolean }).visible === false)).toBe(true);
  });

  it("a palette switch after hiding volume does not silently re-show it", () => {
    // Regression: setPalette() re-applies volumeOptions(p) on every theme switch;
    // without re-asserting the user's visible:false on top of it, a light/dark
    // toggle would resurrect a volume series the user had explicitly hidden.
    const facade = fakeFacade(); const c = mk(facade); c.mount();
    c.setVolumeVisible(false);
    const vol = facade.created.find((x) => x.kind === "histogram")!.series;
    c.setPalette(DARK);
    const lastOptions = vol.optionCalls.at(-1) as { visible?: boolean };
    expect(lastOptions.visible).toBe(false);
  });

  it("setGrid(false) applies invisible grid options", () => {
    const facade = fakeFacade(); const c = mk(facade); c.mount();
    c.setGrid(false);
    expect(JSON.stringify(facade.lastOptions)).toContain('"visible":false');
  });

  it("setWatermark toggles the facade watermark to the bare symbol / null", () => {
    const facade = fakeFacade(); const c = mk(facade); c.mount();
    c.setWatermark(true);
    expect(facade.watermark).toBe("AAPL");
    c.setWatermark(false);
    expect(facade.watermark).toBeNull();
  });
});
