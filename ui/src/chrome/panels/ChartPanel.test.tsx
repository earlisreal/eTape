// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, within, screen, act } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";

// Mock lightweight-charts so the panel test never touches a real canvas.
// timeScaleApi is a stable object (not a fresh literal per call) so a test can hold
// a reference to e.g. resetTimeScale and assert it was invoked by the SUT.
const timeScaleApi = { timeToCoordinate: vi.fn(() => 0), scrollToRealTime: vi.fn(), scrollPosition: vi.fn(() => 0),
  coordinateToLogical: vi.fn(() => 0), logicalToCoordinate: vi.fn(() => 0), resetTimeScale: vi.fn(),
  scrollToPosition: vi.fn(), subscribeVisibleLogicalRangeChange: vi.fn(), unsubscribeVisibleLogicalRangeChange: vi.fn(),
  getVisibleRange: vi.fn<() => { from: number; to: number } | null>(() => null),
  getVisibleLogicalRange: vi.fn<() => { from: number; to: number } | null>(() => null),
  setVisibleRange: vi.fn(), setVisibleLogicalRange: vi.fn() };
// priceScaleApi is a stable object (not a fresh literal per call) so a test can hold
// a reference to applyOptions and assert it was invoked by the SUT (mirrors timeScaleApi above).
const priceScaleApi = { applyOptions: vi.fn(), width: vi.fn(() => 60) };
// paneApis is a stable array (not a fresh literal per `panes()` call) so setPaneStretchFactor
// calls made through one `panes()` read are visible to a later `panes()` read in the same
// test — mirrors why timeScaleApi/priceScaleApi above are hoisted instead of inlined.
const paneApis = [
  { attachPrimitive: vi.fn(), getHeight: vi.fn(() => 400), getStretchFactor: vi.fn(() => 1), setStretchFactor: vi.fn() },
  { attachPrimitive: vi.fn(), getHeight: vi.fn(() => 120), getStretchFactor: vi.fn(() => 1), setStretchFactor: vi.fn() },
];
const chartApi = {
  addSeries: vi.fn(() => ({ setData: vi.fn(), update: vi.fn(), applyOptions: vi.fn(), setSeriesOrder: vi.fn(),
    attachPrimitive: vi.fn(), priceToCoordinate: vi.fn(() => 0), coordinateToPrice: vi.fn(() => 0) })),
  removeSeries: vi.fn(),
  panes: vi.fn(() => paneApis),
  priceScale: vi.fn(() => priceScaleApi),
  timeScale: vi.fn(() => timeScaleApi),
  applyOptions: vi.fn(), resize: vi.fn(), remove: vi.fn(),
  takeScreenshot: vi.fn(() => document.createElement("canvas")),
  subscribeCrosshairMove: vi.fn(),
  unsubscribeCrosshairMove: vi.fn(),
};
vi.mock("lightweight-charts", () => ({
  createChart: vi.fn(() => chartApi),
  createTextWatermark: vi.fn(() => ({ detach: vi.fn(), applyOptions: vi.fn() })),
  CandlestickSeries: "Candlestick", HistogramSeries: "Histogram", LineSeries: "Line",
  BarSeries: "Bar", AreaSeries: "Area", CrosshairMode: { Magnet: 1 },
}));

import { ChartPanel } from "./ChartPanel";
import { ChartController } from "../../render/chart/ChartController";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf, type Surface } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { PanelConfig } from "../workspace";
import type { AckMsg, Bar, DeltaMsg, SysEvent } from "../../wire/contract";
import { DEFAULT_CHART_SETTINGS } from "./tv/ChartSettingsDialog";
import { FakeDrawingBus, FakeDrawingBusHub } from "../../../test/fakes";
import { perf } from "../../perf/PerfMonitor";
import { DrawingInteraction } from "../../render/chart/drawings/interaction";
import { makeQueryClient } from "../../wire/queries";

// jsdom has no ResizeObserver; ChartPanel's resize wiring only needs observe/disconnect.
class MockResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
vi.stubGlobal("ResizeObserver", MockResizeObserver);

// jsdom's requestAnimationFrame is a real (timer-based) async callback.
// ChartPanel batches its crosshair/pan handlers' expensive recompute to a
// single rAF (see subscribeCrosshairMove/subscribeVisibleLogicalRangeChange
// wiring in ChartPanel.tsx) so a test that invokes those handlers directly
// and asserts synchronously (this file's established pattern — see
// renderChartCapturingSurface's paint()-instead-of-racing-the-scheduler
// comment) needs the deferred callback to run immediately rather than racing
// a real timer.
vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => { cb(0); return 0; });
vi.stubGlobal("cancelAnimationFrame", () => {});

beforeEach(() => { vi.clearAllMocks(); cleanup(); });

function renderChart(id = "c1", sharedStores?: ReturnType<typeof makeStores>, sharedScheduler?: Scheduler,
  settingsOverride?: Record<string, unknown>, chartQueryResult?: unknown, monitoring = false) {
  const stores = sharedStores ?? makeStores();
  const scheduler = sharedScheduler ?? new Scheduler(browserRaf, () => {});
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const sendQuery = vi.fn(async (name: string, args: unknown) => {
    if (name === "QueryChartWindow" && chartQueryResult) return chartQueryResult;
	  if (name === "QueryChartWindow") return { ...(args as object), bars: [], indicators: [], historyRevision: 0 };
    return [];
  });
  const commands = {
    sendCommand: vi.fn(async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" })),
    sendQuery,
    queries: makeQueryClient(false, (name, args) => sendQuery(name, args)),
  };
  const config = { id, panelId: "chart", group: "green" as const, settings: { symbol: "US.AAPL", timeframe: "1m", ...settingsOverride } };
  const onConfigChange = vi.fn();
  const panel = (group?: PanelConfig["group"], symbol?: string) => (
    <ThemeProvider>
      <ChartPanel config={config} stores={stores} scheduler={scheduler} width={400} height={300}
        linkGroups={linkGroups} commands={commands} onConfigChange={onConfigChange}
        monitoring={monitoring}
        {...(group === undefined ? {} : { group })} {...(symbol === undefined ? {} : { symbol })} />
    </ThemeProvider>
  );
  const utils = render(panel());
  return { ...utils, stores, onConfigChange, scheduler, commands, linkGroups,
    rerenderGroup: (group: PanelConfig["group"]) => utils.rerender(panel(group)),
    rerenderPinnedSymbol: (symbol: string) => utils.rerender(panel(null, symbol)) };
}

// Pushes a bar into the shared BarStore, the same delta shape the engine sends
// for a live bar (see BarStore.test.ts's own `bar`/`delta` helpers). Defaults to
// an in-progress bar; set inProgress to false to push a closed bar.
function pushLiveBar(stores: ReturnType<typeof makeStores>, symbol: string, timeframe: string, o: number, c: number, inProgress = true,
  bucketStart = "2026-07-09T13:31:00.000Z"): void {
  const bar: Bar = { symbol, timeframe, bucketStart, o, h: Math.max(o, c) + 0.1, l: Math.min(o, c) - 0.1, c, v: 100, inProgress };
  const msg: DeltaMsg = { kind: "delta", topic: "md.bars", key: `${symbol}:${timeframe}`, payload: bar };
  stores.bars.apply(msg);
}

// Mounts ChartPanel with a scheduler.register spy that captures the registered
// Surface, mirroring the "repositions the MACD legend" test above — lets a test
// call paint() directly instead of racing the scheduler's own rAF loop.
function renderChartCapturingSurface(settingsOverride?: Record<string, unknown>, chartQueryResult?: unknown) {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  let surface: Surface | undefined;
  vi.spyOn(scheduler, "register").mockImplementation((s: Surface) => { surface = s; return vi.fn(); });
  const utils = renderChart("c1", stores, scheduler, settingsOverride, chartQueryResult);
  return { ...utils, stores, getSurface: () => surface! };
}

describe("ChartPanel", () => {
  it("keeps an unassigned Monitoring chart idle and shows the sync wait state", async () => {
    const { createChart } = await import("lightweight-charts");
    const { commands } = renderChart("empty", undefined, undefined, { symbol: undefined }, undefined, true);
    expect(screen.getByTestId("chart-empty-state").textContent).toBe("Waiting for Scanner Sync");
    expect(createChart).not.toHaveBeenCalled();
    expect(commands.sendQuery).not.toHaveBeenCalled();
  });

  it("creates a chart and registers candle + volume series on mount", async () => {
    const { createChart } = await import("lightweight-charts");
    renderChart();
    expect(createChart).toHaveBeenCalledTimes(1);
    expect(chartApi.addSeries).toHaveBeenCalled(); // candle + volume
  });

  it("removes the chart on unmount", () => {
    const { unmount } = renderChart();
    unmount();
    expect(chartApi.remove).toHaveBeenCalledTimes(1);
  });

  it("caps rightward panning at RIGHT_OFFSET_BARS when no range is known yet, and unsubscribes on unmount", () => {
    const { unmount } = renderChart();
    expect(timeScaleApi.subscribeVisibleLogicalRangeChange).toHaveBeenCalledTimes(1);
    const clampRight = timeScaleApi.subscribeVisibleLogicalRangeChange.mock.calls[0][0] as (r: { from: number; to: number } | null) => void;

    // No range (e.g. before the chart has laid out bars) falls back to the
    // RIGHT_OFFSET_BARS floor: panned past it snaps back, no bar-spacing change.
    timeScaleApi.scrollPosition.mockReturnValue(20);
    clampRight(null);
    expect(timeScaleApi.scrollToPosition).toHaveBeenCalledWith(4, false);

    // Within bounds (resting position or scrolled into history): no snap.
    timeScaleApi.scrollToPosition.mockClear();
    timeScaleApi.scrollPosition.mockReturnValue(4);
    clampRight(null);
    timeScaleApi.scrollPosition.mockReturnValue(-2);
    clampRight(null);
    expect(timeScaleApi.scrollToPosition).not.toHaveBeenCalled();

    unmount();
    expect(timeScaleApi.unsubscribeVisibleLogicalRangeChange).toHaveBeenCalledWith(clampRight);
  });

  it("samples viewport intent through the full pointer-drag lifecycle", () => {
    const noteViewport = vi.spyOn(ChartController.prototype, "noteUserViewportInteraction");
    try {
      const { getByTestId } = renderChart();
      const host = getByTestId("chart-host");
      timeScaleApi.scrollPosition
        .mockReturnValueOnce(3.7)
        .mockReturnValueOnce(-5)
        .mockReturnValueOnce(-20);

      fireEvent.pointerDown(host, { button: 0, pointerType: "mouse", clientX: 0, clientY: 0 });
      fireEvent.pointerMove(host, { buttons: 1, pointerType: "mouse", clientX: 1, clientY: 0 });
      fireEvent.pointerMove(host, { buttons: 1, pointerType: "mouse", clientX: 4, clientY: 0 });
      fireEvent.pointerMove(host, { buttons: 1, pointerType: "mouse", clientX: 40, clientY: 0 });
      fireEvent.pointerUp(host, { pointerType: "mouse" });

      // The first move crosses the threshold while still live; the continued
      // move and pointer-up must sample the final historical position too.
      expect(noteViewport).toHaveBeenCalledTimes(3);
    } finally {
      noteViewport.mockRestore();
    }
  });

  it("expands the rightward pan cap to the visible range width, so the latest bar can reach the left edge", () => {
    renderChart();
    const clampRight = timeScaleApi.subscribeVisibleLogicalRangeChange.mock.calls[0][0] as (r: { from: number; to: number } | null) => void;

    // A 50-bar-wide viewport: panning past the resting margin but still within the
    // expanded range (latest bar not yet past the left edge) does not snap back.
    timeScaleApi.scrollPosition.mockReturnValue(30);
    clampRight({ from: 0, to: 50 });
    expect(timeScaleApi.scrollToPosition).not.toHaveBeenCalled();

    // Panned past the new cap (visibleBars - 1 = 49, i.e. past latest-bar-at-left-edge):
    // snap back to 49, not the old fixed RIGHT_OFFSET_BARS.
    timeScaleApi.scrollPosition.mockReturnValue(60);
    clampRight({ from: 0, to: 50 });
    expect(timeScaleApi.scrollToPosition).toHaveBeenCalledWith(49, false);
  });

  it("loads one prepared snapshot and ignores pan/zoom releases", async () => {
    const { commands, container, stores } = renderChart();
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();
    commands.sendCommand.mockClear();

    const host = container.querySelector("[data-testid=chart-host]") as HTMLElement;
    fireEvent.pointerUp(host);
    fireEvent.wheel(host);
    fireEvent.keyUp(window, { key: "ArrowLeft" });
    expect(commands.sendCommand).not.toHaveBeenCalledWith("LoadOlderBars", expect.anything());
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);

    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(commands.sendQuery).toHaveBeenCalledWith("QueryChartWindow", expect.objectContaining({
      symbol: "US.AAPL", timeframe: "1m", tailBars: 1_000_000,
    }));
	act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
	  seq: 2, ts: "2026-08-03T01:00:01Z", kind: "chart-ready", detail: "US.AAPL",
	} }));
	act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
	  seq: 3, ts: "2026-08-03T01:00:02Z", kind: "history-ready", detail: "US.AAPL",
	} }));
	await act(async () => { await Promise.resolve(); });
	expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(1);
  });

  it("scopes indicator instanceIds to the panel, so two panels adding the same indicator type don't collide (Finding 2 regression)", () => {
    // Both panels share ONE store instance, exactly as App.tsx's single makeStores()
    // call shares BarStore/IndicatorStore across every chart panel in a workspace.
    const stores = makeStores();
    const { container: c1, onConfigChange: onConfigChange1 } = renderChart("panel-a", stores);
    const { container: c2, onConfigChange: onConfigChange2 } = renderChart("panel-b", stores);

    fireEvent.click(within(c1).getByRole("button", { name: "indicators" }));
    fireEvent.click(within(document.body).getByRole("button", { name: "add VWAP" }));
    fireEvent.click(within(c2).getByRole("button", { name: "indicators" }));
    fireEvent.click(within(document.body).getByRole("button", { name: "add VWAP" }));

    // Recover the minted instanceId for each panel's VWAP instance from the persisted
    // config patch (persist() always carries the current `indicators` array).
    type Persisted = { indicators: { instanceId: string }[] };
    const id1 = (onConfigChange1.mock.calls.at(-1)![0] as Persisted).indicators[0].instanceId;
    const id2 = (onConfigChange2.mock.calls.at(-1)![0] as Persisted).indicators[0].instanceId;

    // Before the fix both would be "VWAP-0" (idSeq is per-panel but unscoped) —
    // colliding in the shared IndicatorStore keyed solely by instanceId.
    expect(id1).not.toBe(id2);
    expect(id1.startsWith("panel-a:")).toBe(true);
    expect(id2.startsWith("panel-b:")).toBe(true);
  });

  it("loads persisted drawings for its symbol on mount (ensureLoaded → GetConfig)", async () => {
    const stores = makeStores();
    const hub = new FakeDrawingBusHub();
    const drawCmd = { sendCommand: vi.fn(async () => ({ status: "accepted", value: [] })) };
    stores.drawings.connect({ commands: drawCmd as never, bus: new FakeDrawingBus(hub), onError: () => {} });
    renderChart("c1", stores);
    await Promise.resolve();
    expect(drawCmd.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "drawings.US.AAPL" });
  });

  it("shares one drawings store across two panels without crashing", () => {
    const stores = makeStores();
    renderChart("panel-a", stores);
    renderChart("panel-b", stores);
    stores.drawings.upsert({ id: "d", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }], createdMs: 1, updatedMs: 1 });
    expect(stores.drawings.forSymbol("US.AAPL")).toHaveLength(1);
  });

  it("right-click opens a context menu; Reset chart view calls the chart's resetTimeScale and re-enables price autoScale", () => {
    const { getByTestId, getByRole } = renderChart();
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    expect(screen.queryByRole("button", { name: "Jump to live" })).toBeNull();
    fireEvent.click(getByRole("button", { name: "Reset chart view" }));
    expect(timeScaleApi.resetTimeScale).toHaveBeenCalledTimes(1);
    expect(priceScaleApi.applyOptions).toHaveBeenCalledWith({ autoScale: true });
  });

  it("right-click menu's Remove all drawings clears this symbol's drawings", () => {
    const stores = makeStores();
    stores.drawings.upsert({ id: "d", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }], createdMs: 1, updatedMs: 1 });
    const { getByTestId, getByRole } = renderChart("c1", stores);
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    fireEvent.click(getByRole("button", { name: "Remove all drawings" }));
    expect(stores.drawings.forSymbol("US.AAPL")).toHaveLength(0);
  });

  it("right-click menu shows 'Add ... to watchlist' when the chart's symbol isn't watchlisted; clicking it sends WatchlistAdd", () => {
    const stores = makeStores();
    vi.spyOn(stores.watchlist, "has").mockReturnValue(false);
    const { getByTestId, getByRole, commands } = renderChart("c1", stores);
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    const btn = getByRole("button", { name: "Add AAPL to watchlist" });
    expect(btn).toBeTruthy();
    fireEvent.click(btn);
    expect(commands.sendCommand).toHaveBeenCalledWith("WatchlistAdd", { symbol: "US.AAPL" });
  });

  it("right-click menu shows 'Remove ... from watchlist' when the chart's symbol is already watchlisted; clicking it sends WatchlistRemove", () => {
    const stores = makeStores();
    vi.spyOn(stores.watchlist, "has").mockReturnValue(true);
    const { getByTestId, getByRole, commands } = renderChart("c1", stores);
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    const btn = getByRole("button", { name: "Remove AAPL from watchlist" });
    expect(btn).toBeTruthy();
    fireEvent.click(btn);
    expect(commands.sendCommand).toHaveBeenCalledWith("WatchlistRemove", { symbol: "US.AAPL" });
  });

  it("positions the context menu at viewport coordinates, not host-relative (wrong-chart-in-group regression)", () => {
    const { getByTestId, getByRole } = renderChart();
    const host = getByTestId("chart-host");
    // Simulate this chart being tiled away from the viewport origin, as it would be
    // as the 2nd/3rd chart in a linked group. jsdom's default getBoundingClientRect
    // returns all zeros, which is exactly why the pre-fix bug was invisible to every
    // other right-click test in this file (host-relative == viewport-relative at (0,0)).
    host.getBoundingClientRect = () => ({ left: 100, top: 50, right: 500, bottom: 350,
      width: 400, height: 300, x: 100, y: 50, toJSON: () => {} }) as DOMRect;

    fireEvent.contextMenu(host, { clientX: 120, clientY: 80 });

    const menu = getByRole("menu");
    // Before the fix, TVContextMenu (position: fixed, viewport-relative) was fed the
    // host-relative offset (20, 30) instead of the click's viewport coords (120, 80) —
    // dropping the menu near the viewport origin, i.e. over a different chart.
    expect(menu.style.left).toBe("120px");
    expect(menu.style.top).toBe("80px");
  });

  it("floating toolbar's own controls reflect a style edit made through the toolbar itself (Finding 1 regression)", () => {
    const stores = makeStores();
    // hline with a single anchor at (timeMs:0, price:1) — with every coordinate
    // mock in this file returning 0, its projected point is always (0,0), so a
    // right-click at (0,0) hit-tests it, mirroring the existing right-click
    // selection tests above (they use (20,30), which deliberately misses).
    stores.drawings.upsert({ id: "d1", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }],
      color: "#089981", width: 1, lineStyle: "solid", createdMs: 1, updatedMs: 1 });
    const { getByTestId, getByRole } = renderChart("c1", stores);

    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 0, clientY: 0 });
    const widthBtn = (w: number) => getByRole("button", { name: `width ${w}` }) as HTMLButtonElement;

    // Floating toolbar renders, showing the drawing's initial width as active.
    expect(widthBtn(1).style.fontWeight).toBe("700");
    expect(widthBtn(3).style.fontWeight).toBe("500");

    // Edit width via the toolbar's own control — this patches the store but (like
    // production, where the fix is a same-render memoization guard rather than an
    // immediate call) does not by itself update React state; it takes the next
    // reconciliation pass to pick up the change.
    fireEvent.click(widthBtn(3));

    // Simulate the next unrelated repaint reaching refreshSelection — reuses the
    // same clampRight hook the "caps rightward panning" test above captures
    // (ChartPanel calls refreshSelRef.current?.() from it unconditionally). Before
    // the fix, refreshSelection's equality guard only compared id/rect — since
    // editing width doesn't move the drawing's anchors, rect is unchanged, so it
    // returned the stale `prev` object and this assertion would still see width 1.
    // Wrapped in act() (unlike fireEvent, a direct function call doesn't get one
    // automatically) so the resulting setSelection is flushed before we assert.
    const clampRight = timeScaleApi.subscribeVisibleLogicalRangeChange.mock.calls[0][0] as () => void;
    act(() => { clampRight(); });

    expect(widthBtn(3).style.fontWeight).toBe("700");
    expect(widthBtn(1).style.fontWeight).toBe("500");
  });

  it("editing a drawing's style via the floating toolbar remembers it as the tool's new default", () => {
    const stores = makeStores();
    stores.drawings.upsert({ id: "d1", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }],
      color: "#089981", width: 1, lineStyle: "solid", createdMs: 1, updatedMs: 1 });
    const remember = vi.spyOn(stores.drawingToolStyles, "remember");
    const { getByTestId, getByRole } = renderChart("c1", stores);

    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 0, clientY: 0 });
    fireEvent.click(getByRole("button", { name: "width 3" }));

    expect(remember).toHaveBeenCalledWith("hline", { width: 3 });
  });

  it("a pointerdown on the floating toolbar doesn't deselect, so its buttons still fire (drawing-options regression)", () => {
    const stores = makeStores();
    stores.drawings.upsert({ id: "d1", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }],
      color: "#089981", width: 1, lineStyle: "solid", createdMs: 1, updatedMs: 1 });
    const { getByTestId, getByRole, queryByRole } = renderChart("c1", stores);

    // Select the drawing (same (0,0) hit-test trick as the Finding 1 test above).
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 0, clientY: 0 });
    const del = getByRole("button", { name: "delete drawing" });

    // The real-world button press: a NATIVE pointerdown that bubbles from the
    // toolbar to the chart host, where DrawingInteraction's raw listener runs
    // before any React handler. clientX/Y far from the drawing's (0,0) projection
    // so, without the data-drawing-ui guard, it takes the blank-canvas deselect
    // branch and unmounts the toolbar before the click can fire.
    fireEvent(del, new MouseEvent("pointerdown", { bubbles: true, clientX: 500, clientY: 500 }));
    expect(queryByRole("button", { name: "delete drawing" })).toBeTruthy(); // still mounted

    fireEvent.click(getByRole("button", { name: "delete drawing" }));
    expect(stores.drawings.forSymbol("US.AAPL")).toHaveLength(0); // the action actually ran
  });

  it("the context menu closes after an action and on Escape", () => {
    const { getByTestId, getByRole, queryByRole } = renderChart();
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    fireEvent.click(getByRole("button", { name: "Reset chart view" }));
    expect(queryByRole("button", { name: "Reset chart view" })).toBeNull();

    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    expect(getByRole("button", { name: "Remove all drawings" })).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(queryByRole("button", { name: "Remove all drawings" })).toBeNull();
  });

  it("renders the chart header controls and persists a timeframe change", () => {
    const { getByRole, onConfigChange } = renderChart();
    fireEvent.click(getByRole("button", { name: "timeframe 5m" }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({ timeframe: "5m" }));
  });

  it("queries the newly selected timeframe synchronously", async () => {
    const { getByRole, commands } = renderChart("c1", undefined, undefined, { timeframe: "D" });
    const before = commands.sendQuery.mock.calls.length;
    fireEvent.click(getByRole("button", { name: "timeframe W" }));
    await Promise.resolve();
    const chartQueries = commands.sendQuery.mock.calls.slice(before)
      .filter(([name]) => name === "QueryChartWindow")
      .map(([, args]) => (args as { timeframe: string }).timeframe);
    expect(chartQueries).toContain("W");
    expect(chartQueries).not.toContain("D");
  });

  it("shows drawing tools by default, toggles only its rail, and persists patch-only state", () => {
    const { getByRole, queryByRole, onConfigChange } = renderChart();
    const toggle = getByRole("button", { name: "drawing tools" });
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(getByRole("button", { name: "move toolbar" })).toBeTruthy();
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    expect(queryByRole("button", { name: "move toolbar" })).toBeNull();
    expect(onConfigChange).toHaveBeenLastCalledWith({ drawingToolsVisible: false });
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(getByRole("button", { name: "move toolbar" })).toBeTruthy();
    expect(onConfigChange).toHaveBeenLastCalledWith({ drawingToolsVisible: true });
  });

  it("honors persisted hidden drawing tools and keeps chart panels independent", () => {
    const { container: hidden } = renderChart("hidden", undefined, undefined, { drawingToolsVisible: false });
    const { container: visible, onConfigChange } = renderChart("visible");
    expect(within(hidden).getByRole("button", { name: "drawing tools" }).getAttribute("aria-pressed")).toBe("false");
    expect(within(hidden).queryByRole("button", { name: "move toolbar" })).toBeNull();
    fireEvent.click(within(visible).getByRole("button", { name: "drawing tools" }));
    expect(within(hidden).queryByRole("button", { name: "move toolbar" })).toBeNull();
    expect(within(visible).queryByRole("button", { name: "move toolbar" })).toBeNull();
    expect(onConfigChange).toHaveBeenLastCalledWith({ drawingToolsVisible: false });
  });

  it("restores the current drawing rail position after hide and show", () => {
    const { getByRole, onConfigChange } = renderChart("c1", undefined, undefined, { drawingRailPos: { x: 33, y: 44 } });
    const toggle = getByRole("button", { name: "drawing tools" });
    const grip = getByRole("button", { name: "move toolbar" });
    const rail = grip.parentElement as HTMLElement;
    const host = rail.parentElement as HTMLElement;
    vi.spyOn(rail, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 100, height: 30 } as DOMRect);
    vi.spyOn(host, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 400, height: 300 } as DOMRect);
    fireEvent.pointerDown(grip, { clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { clientX: 60, clientY: 70 });
    fireEvent.pointerUp(window);
    expect(onConfigChange).toHaveBeenLastCalledWith({ drawingRailPos: { x: 50, y: 60 } });
    fireEvent.click(toggle);
    fireEvent.click(toggle);
    expect((getByRole("button", { name: "move toolbar" }).parentElement as HTMLElement).style.left).toBe("50px");
    expect((getByRole("button", { name: "move toolbar" }).parentElement as HTMLElement).style.top).toBe("60px");
  });

  it("disarms the active drawing tool when hiding the rail", () => {
    const setTool = vi.spyOn(DrawingInteraction.prototype, "setTool");
    const { getByRole } = renderChart();
    fireEvent.click(getByRole("button", { name: "trend line" }));
    fireEvent.click(getByRole("button", { name: "drawing tools" }));
    expect(setTool).toHaveBeenLastCalledWith("select");
    setTool.mockRestore();
  });

  it("gates persisted-style drawing tools until style hydration, but leaves Measure usable", async () => {
    let resolveGet!: (ack: { status: string; value?: unknown }) => void;
    const styleCommands = {
      sendCommand: vi.fn((name: string) => name === "GetConfig"
        ? new Promise<{ status: string; value?: unknown }>((resolve) => { resolveGet = resolve; })
        : Promise.resolve({ status: "accepted" })),
    };
    const stores = makeStores();
    stores.drawingToolStyles.connect({ commands: styleCommands });
    const { getByRole } = renderChart("c1", stores);

    expect((getByRole("button", { name: "horizontal line" }) as HTMLButtonElement).disabled).toBe(true);
    expect((getByRole("button", { name: "measure" }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(getByRole("button", { name: "measure" }));
    expect((getByRole("button", { name: "measure" }) as HTMLButtonElement).getAttribute("aria-pressed")).toBeNull();

    await act(async () => {
      resolveGet({ status: "accepted", value: {} });
      await Promise.resolve(); await Promise.resolve();
    });
    expect((getByRole("button", { name: "horizontal line" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("selects a completed drawing and opens its floating style toolbar immediately", async () => {
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const { stores, getByRole, getByTestId } = renderChart("c1", undefined, undefined, { timeframe: "1m" }, {
      symbol: "US.AAPL", timeframe: "1m", fromMs: Date.parse(bars[0].bucketStart), toMs: Date.parse(bars[1].bucketStart) + 60_000,
      bars, indicators: [], historyRevision: 1,
    });
    await act(async () => {
      stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
        seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
      } });
      await Promise.resolve(); await Promise.resolve();
    });

    fireEvent.click(getByRole("button", { name: "horizontal line" }));
    const host = getByTestId("chart-host");
    fireEvent.pointerDown(host, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.pointerUp(host, { clientX: 0, clientY: 0 });

    expect(stores.drawings.forSymbol("US.AAPL")).toHaveLength(1);
    expect(getByRole("button", { name: "delete drawing" })).toBeTruthy();
    expect(getByRole("button", { name: "width 1" })).toBeTruthy();
  });

  it("does not reset a loaded VWAP when the linked group focuses the displayed symbol", async () => {
    const key = "c1:VWAP-0";
    const point = { timeMs: 1, value: 100.5 };
    const { stores, commands, linkGroups } = renderChart("c1", undefined, undefined, {
      timeframe: "D",
      indicators: [{ instanceId: key, type: "VWAP", params: {} }],
    }, {
      symbol: "US.AAPL", timeframe: "D", fromMs: 1, toMs: 2, bars: [],
      indicators: [{ seriesKey: key, points: [point] }], historyRevision: 1,
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const existingPoints = stores.indicators.series(key);
    expect(existingPoints).toEqual([point]);
    const commandCalls = commands.sendCommand.mock.calls as unknown[][];
    const subscribeCount = commandCalls.filter(([name]) => name === "SubscribeIndicator").length;
    commands.sendQuery.mockClear();

    act(() => linkGroups.focus("green", "US.AAPL"));
    await act(async () => { await Promise.resolve(); });
    act(() => linkGroups.focus("green", "US.AAPL"));
    await act(async () => { await Promise.resolve(); });

    expect(stores.indicators.series(key)).toEqual(existingPoints);
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);
    expect((commands.sendCommand.mock.calls as unknown[][]).filter(([name]) => name === "SubscribeIndicator")).toHaveLength(subscribeCount);
    expect(chartApi.remove).not.toHaveBeenCalled();
  });

  it("keeps bars and indicators visible beyond the latest query boundary", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = fromMs + 1;
    const latest: Bar = {
      symbol: "US.AAPL", timeframe: "10s", bucketStart: "2026-07-09T13:30:00.000Z",
      o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: true,
    };
    const queryResult = {
      symbol: "US.AAPL", timeframe: "10s", fromMs, toMs, bars: [latest],
      indicators: [{ seriesKey: "vwap-1", points: [{ timeMs: fromMs, value: 100.5 }] }],
      historyRevision: 1,
    };
    const { stores } = renderChart("c1", undefined, undefined, {
      timeframe: "10s",
      indicators: [{ instanceId: "vwap-1", type: "VWAP", params: {} }],
    }, queryResult);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    const nextMs = Date.parse("2026-07-09T13:31:00.000Z");
    pushLiveBar(stores, "US.AAPL", "10s", 100.5, 101);
    stores.indicators.apply({ kind: "delta", topic: "md.indicator", key: "vwap-1",
      payload: { timeMs: nextMs, value: 100.75 } });

    expect(stores.bars.series("US.AAPL", "10s").at(-1)?.bucketStart).toBe("2026-07-09T13:31:00.000Z");
    expect(stores.indicators.series("vwap-1").at(-1)?.timeMs).toBe(nextMs);
    expect(stores.bars.missingRanges("US.AAPL", "10s", toMs, nextMs + 1)).toEqual([{ fromMs: toMs, toMs: nextMs + 1 }]);
  });

  it("loads indicators for a lower-sequence chart-ready event from another producer", async () => {
    const { stores, commands } = renderChart("c1", undefined, undefined, {
      timeframe: "D",
      indicators: [{ instanceId: "c1:EMA-0", type: "EMA", params: { period: 200 } }],
    });
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();

    const pushEvent = (event: SysEvent) => act(() => stores.health.apply({
      kind: "delta", topic: "sys.events", payload: event,
    }));
    pushEvent({ seq: 50, ts: "2026-08-02T01:00:00Z", kind: "quota", detail: "unrelated" });
    expect(commands.sendQuery).not.toHaveBeenCalled();

    // Hub-owned chart-ready sequences are independent from other sys.events
    // producers, so seq=1 is newer here despite being numerically lower.
    pushEvent({ seq: 1, ts: "2026-08-02T01:00:01Z", kind: "chart-ready", detail: "US.AAPL" });
    await act(async () => { await Promise.resolve(); });

    expect(commands.sendQuery).toHaveBeenCalledWith("QueryChartWindow", expect.objectContaining({
      symbol: "US.AAPL", timeframe: "D", indicatorSeriesKeys: ["c1:EMA-0"],
    }));
  });

  it("waits for chart-ready before loading a reused VWAP after a symbol switch", async () => {
    const key = "c1:VWAP-0";
    const { stores, commands, linkGroups } = renderChart("c1", undefined, undefined, {
      timeframe: "D",
      indicators: [{ instanceId: key, type: "VWAP", params: {} }],
    });
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();
    commands.sendQuery.mockImplementation(async (_name: string, raw: unknown) => {
      if (_name === "QueryFills") return [];
      const args = raw as { symbol: string; timeframe: string; indicatorSeriesKeys: string[] };
      return {
        symbol: args.symbol, timeframe: args.timeframe, fromMs: 1, toMs: 3, bars: [],
        indicators: args.indicatorSeriesKeys.length
          ? [{ seriesKey: key, points: [{ timeMs: 2, value: 4.25 }] }]
          : [],
      };
    });

    act(() => linkGroups.focus("green", "US.YYAI"));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const immediate = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(immediate).toHaveLength(0);

    // An old live point can arrive after the controller reset but before the
    // engine's ordered replacement snapshot reaches the mirror.
    act(() => stores.indicators.apply({ kind: "delta", topic: "md.indicator", key,
      payload: { timeMs: 1, value: 99 } }));
    expect(stores.indicators.series(key)).toEqual([{ timeMs: 1, value: 99 }]);
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);

    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.YYAI",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    expect(commands.sendQuery).toHaveBeenLastCalledWith("QueryChartWindow", expect.objectContaining({
      symbol: "US.YYAI", indicatorSeriesKeys: [key],
    }));
    expect(stores.indicators.series(key)).toEqual([{ timeMs: 2, value: 4.25 }]);
  });

  it("keeps the displayed group symbol without reloading when pinned", async () => {
    const { commands, linkGroups, rerenderGroup } = renderChart();
    act(() => linkGroups.focus("green", "US.NVDA"));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();

    rerenderGroup(null);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);
  });

  it("loads a newly typed pinned symbol without remounting", async () => {
    const { commands, stores, rerenderPinnedSymbol } = renderChart();
    rerenderPinnedSymbol("US.NVDA");
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.NVDA",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(commands.sendQuery).toHaveBeenCalledWith("QueryChartWindow", expect.objectContaining({ symbol: "US.NVDA" }));
  });

  it("uses generated 10s display bars for the legend logical index", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-09T13:30:20Z"));
    try {
      const first: Bar = { symbol: "US.AAPL", timeframe: "10s", bucketStart: "2026-07-09T13:30:00Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false };
      const next: Bar = { ...first, bucketStart: "2026-07-09T13:30:20Z", o: 100.5, h: 12, l: 100, c: 12 };
      const { getByTestId, stores, getSurface } = renderChartCapturingSurface({ timeframe: "10s" }, {
        symbol: "US.AAPL", timeframe: "10s", fromMs: Date.parse(first.bucketStart), toMs: Date.parse(next.bucketStart), bars: [first, next], indicators: [], historyRevision: 1,
      });
      act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
        seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
      } }));
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      act(() => getSurface().paint());
      const crosshair = chartApi.subscribeCrosshairMove.mock.calls[0][0] as (param: { logical?: number }) => void;
      act(() => crosshair({ logical: 1 })); // generated :10 slot, not raw :20 bar
      expect(getByTestId("legend-c").textContent).toContain("100.5");
      expect(getByTestId("legend-vol").textContent).toContain("0");
    } finally {
      now.mockRestore();
    }
  });

  it("camera button calls the chart's takeScreenshot", () => {
    const { getByRole } = renderChart();
    fireEvent.click(getByRole("button", { name: "screenshot" }));
    expect(chartApi.takeScreenshot).toHaveBeenCalled();
  });

  it("adding an indicator via the picker persists it", () => {
    const { getByRole, onConfigChange } = renderChart();
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({
      indicators: expect.arrayContaining([expect.objectContaining({ type: "EMA" })]),
    }));
  });

  it("keeps an indicator hover target content-sized so blank chart space stays pannable", () => {
    const { getByRole } = renderChart();
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    expect((screen.getByTestId("legend-row-c1:VWAP-0") as HTMLElement).style.alignSelf).toBe("flex-start");
  });

  it("hydrates a VWAP added after the chart snapshot is already loaded", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const { container, stores, commands, onConfigChange } = renderChart("c1", undefined, undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [], historyRevision: 1,
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const barsRev = stores.bars.getRev("US.AAPL", "1m");
    commands.sendQuery.mockClear();

    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    type Persisted = { indicators: { instanceId: string }[] };
    const instanceId = ((onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators[0]).instanceId;
    expect(commands.sendCommand).toHaveBeenCalledWith("SubscribeIndicator", expect.objectContaining({ instanceId }));
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);

    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "history-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); });
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);

    const points = [{ timeMs: fromMs, value: 100.25 }, { timeMs: Date.parse("2026-07-09T13:31:00.000Z"), value: 101.1 }];
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { symbol: string; timeframe: string; fromMs: number; toMs: number };
      return {
        symbol: args.symbol, timeframe: args.timeframe, fromMs: args.fromMs, toMs: args.toMs, bars: [],
        indicators: [{ seriesKey: instanceId, points }], historyRevision: 2,
      };
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 3, ts: "2026-08-03T01:00:02Z", kind: "indicator-ready", detail: instanceId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    const queryCalls = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(queryCalls).toHaveLength(1);
    expect(queryCalls[0][1]).toEqual(expect.objectContaining({
      symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, tailBars: 0,
      indicatorSeriesKeys: [instanceId], skipBars: true,
    }));
    expect(stores.indicators.series(instanceId)).toEqual(points);
    expect(stores.bars.getRev("US.AAPL", "1m")).toBe(barsRev);
  });

  it("hydrates a ready indicator after an in-flight initial snapshot that missed it", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    let resolveInitial!: (result: object) => void;
    const initial = new Promise<object>((resolve) => { resolveInitial = resolve; });
    const { container, stores, commands, onConfigChange } = renderChart();
    commands.sendQuery.mockClear();
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      return (raw as { skipBars?: boolean }).skipBars
        ? { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: [{ seriesKey: (raw as { indicatorSeriesKeys: string[] }).indicatorSeriesKeys[0], points: [{ timeMs: fromMs, value: 100.25 }] }], historyRevision: 2 }
        : initial;
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); });

    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    type Persisted = { indicators: { instanceId: string }[] };
    const instanceId = ((onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators[0]).instanceId;
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: instanceId,
    } }));
    await act(async () => { await Promise.resolve(); });
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(1);

    resolveInitial({ symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [], historyRevision: 1 });
    await act(async () => { await initial; await Promise.resolve(); await Promise.resolve(); });

    const queryCalls = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(queryCalls).toHaveLength(2);
    expect(queryCalls[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [instanceId], skipBars: true, tailBars: 0 }));
    expect(stores.indicators.series(instanceId)).toEqual([{ timeMs: fromMs, value: 100.25 }]);
  });

  it("hydrates an indicator whose initial snapshot only returned an empty series", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const { container, stores, commands, onConfigChange } = renderChart();
    commands.sendQuery.mockClear();
    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    type Persisted = { indicators: { instanceId: string }[] };
    const instanceId = ((onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators[0]).instanceId;
    const points = [{ timeMs: fromMs, value: 100.25 }];
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { skipBars?: boolean };
      return args.skipBars
        ? { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: [{ seriesKey: instanceId, points }], historyRevision: 2 }
        : { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [{ seriesKey: instanceId, points: [] }], historyRevision: 1 };
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(stores.indicators.series(instanceId)).toEqual([]);
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: instanceId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    const queryCalls = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(queryCalls).toHaveLength(2);
    expect(queryCalls[0][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [instanceId] }));
    expect(queryCalls[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [instanceId], skipBars: true, tailBars: 0 }));
    expect(stores.indicators.series(instanceId)).toEqual(points);
  });

  it("retries targeted hydration once after a disconnect", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const { container, stores, commands, onConfigChange } = renderChart("c1", undefined, undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [], historyRevision: 1,
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();

    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    type Persisted = { indicators: { instanceId: string }[] };
    const instanceId = ((onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators[0]).instanceId;
    const points = [{ timeMs: fromMs, value: 100.25 }];
    let resolveRetry!: (result: object) => void;
    const retry = new Promise<object>((resolve) => { resolveRetry = resolve; });
    let attempts = 0;
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { skipBars?: boolean };
      if (!args.skipBars) return { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [], historyRevision: 1 };
      if (attempts++ === 0) throw new Error("websocket disconnected");
      return retry;
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: instanceId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); });

    const targeted = commands.sendQuery.mock.calls.filter(([name, raw]) => name === "QueryChartWindow" && (raw as { skipBars?: boolean }).skipBars);
    expect(targeted).toHaveLength(2);
    expect(targeted[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [instanceId], skipBars: true, tailBars: 0 }));

    resolveRetry({ symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: [{ seriesKey: instanceId, points }], historyRevision: 2 });
    await act(async () => { await retry; await Promise.resolve(); });
    expect(stores.indicators.series(instanceId)).toEqual(points);
  });

  it("ignores indicator-ready after an indicator is removed", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const bar: Bar = { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false };
    const { container, stores, commands, onConfigChange } = renderChart("c1", undefined, undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [bar], indicators: [], historyRevision: 1,
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();
    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    type Persisted = { indicators: { instanceId: string }[] };
    const instanceId = ((onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators[0]).instanceId;
    fireEvent.mouseEnter(screen.getByTestId(`legend-row-${instanceId}`));
    fireEvent.click(screen.getByRole("button", { name: `remove ${instanceId}` }));
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: instanceId,
    } }));
    await act(async () => { await Promise.resolve(); });

    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(0);
    expect(stores.indicators.series(instanceId)).toEqual([]);
    expect(onConfigChange).toHaveBeenLastCalledWith({ indicators: [] });
  });

  it("hydrates multiple indicators independently by instance and series key", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const bars: Bar[] = [{ symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false }];
    const { container, stores, commands, onConfigChange } = renderChart("c1", undefined, undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [], historyRevision: 1,
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    commands.sendQuery.mockClear();
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { indicatorSeriesKeys: string[] };
      return { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: [{ seriesKey: args.indicatorSeriesKeys[0], points: [{ timeMs: fromMs, value: args.indicatorSeriesKeys[0].includes("EMA") ? 9 : 100 }] }], historyRevision: 2 };
    });

    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    fireEvent.click(within(container).getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));
    type Persisted = { indicators: { instanceId: string; type: string }[] };
    const persisted = (onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators;
    const emaId = persisted.find((i) => i.type === "EMA")!.instanceId;
    const vwapId = persisted.find((i) => i.type === "VWAP")!.instanceId;

    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: emaId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const first = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(first).toHaveLength(1);
    expect(first[0][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [emaId], skipBars: true }));

    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 3, ts: "2026-08-03T01:00:02Z", kind: "indicator-ready", detail: vwapId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const all = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(all).toHaveLength(2);
    expect(all[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [vwapId], skipBars: true }));
    expect(stores.indicators.series(emaId)).toEqual([{ timeMs: fromMs, value: 9 }]);
    expect(stores.indicators.series(vwapId)).toEqual([{ timeMs: fromMs, value: 100 }]);
  });

  it("resets and rehydrates EMA history after a period edit", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const emaId = "c1:EMA-1";
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const oldPoints = [{ timeMs: fromMs, value: 9 }, { timeMs: toMs - 1, value: 9.5 }];
    const newPoints = [{ timeMs: fromMs, value: 20 }];
    const { stores, commands } = renderChart("c1", undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", indicators: [{ instanceId: emaId, type: "EMA", params: { period: 9 } }],
    });
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { skipBars?: boolean };
      return args.skipBars
        ? { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: [{ seriesKey: emaId, points: newPoints }], historyRevision: 2 }
        : { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [{ seriesKey: emaId, points: oldPoints }], historyRevision: 1 };
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(stores.indicators.series(emaId)).toEqual(oldPoints);

    fireEvent.mouseEnter(screen.getByTestId(`legend-row-${emaId}`));
    fireEvent.click(screen.getByRole("button", { name: `settings ${emaId}` }));
    fireEvent.change(screen.getByLabelText("Period"), { target: { value: "20" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));

    expect(stores.indicators.series(emaId)).toEqual([]);
    expect(commands.sendCommand.mock.calls).toContainEqual([
      "SubscribeIndicator", expect.objectContaining({ instanceId: emaId, type: "EMA", params: { period: 20 } }),
    ]);
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: emaId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    const queryCalls = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(queryCalls).toHaveLength(2);
    expect(queryCalls[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [emaId], skipBars: true, tailBars: 0 }));
    expect(stores.indicators.series(emaId)).toEqual(newPoints);
  });

  it("waits for every readiness barrier across rapid EMA edits", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const emaId = "c1:EMA-1";
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const oldPoints = [{ timeMs: fromMs, value: 9 }];
    const ema20Points = [{ timeMs: fromMs, value: 20 }];
    const ema50Points = [{ timeMs: fromMs, value: 50 }];
    const { stores, commands } = renderChart("c1", undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", indicators: [{ instanceId: emaId, type: "EMA", params: { period: 9 } }],
    });
    let hydrationQueries = 0;
    let readinessEvents = 0;
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { skipBars?: boolean };
      if (!args.skipBars) return { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: [{ seriesKey: emaId, points: oldPoints }], historyRevision: 1 };
      hydrationQueries++;
      const points = readinessEvents === 1 ? ema20Points : ema50Points;
      return { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: [{ seriesKey: emaId, points }], historyRevision: 2 };
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(stores.indicators.series(emaId)).toEqual(oldPoints);

    const editPeriod = (period: string) => {
      fireEvent.mouseEnter(screen.getByTestId(`legend-row-${emaId}`));
      fireEvent.click(screen.getByRole("button", { name: `settings ${emaId}` }));
      fireEvent.change(screen.getByLabelText("Period"), { target: { value: period } });
      fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    };
    editPeriod("20");
    editPeriod("50");
    expect(stores.indicators.series(emaId)).toEqual([]);

    readinessEvents++;
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: emaId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow")).toHaveLength(1);
    expect(hydrationQueries).toBe(0);

    readinessEvents++;
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 3, ts: "2026-08-03T01:00:02Z", kind: "indicator-ready", detail: emaId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    const queryCalls = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(queryCalls).toHaveLength(2);
    expect(queryCalls[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: [emaId], skipBars: true, tailBars: 0 }));
    expect(hydrationQueries).toBe(1);
    expect(stores.indicators.series(emaId)).toEqual(ema50Points);
  });

  it("resets and rehydrates all MACD slots after a parameter edit", async () => {
    const fromMs = Date.parse("2026-07-09T13:30:00.000Z");
    const toMs = Date.parse("2026-07-09T13:31:00.000Z") + 1;
    const macdId = "c1:MACD-1";
    const keys = [`${macdId}#macd`, `${macdId}#signal`, `${macdId}#hist`];
    const bars: Bar[] = [
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:30:00.000Z", o: 100, h: 101, l: 99, c: 100.5, v: 100, inProgress: false },
      { symbol: "US.AAPL", timeframe: "1m", bucketStart: "2026-07-09T13:31:00.000Z", o: 100.5, h: 102, l: 100, c: 101.5, v: 120, inProgress: true },
    ];
    const oldPoints = keys.map((seriesKey, i) => ({ seriesKey, points: [{ timeMs: fromMs, value: i + 1 }, { timeMs: toMs - 1, value: i + 2 }] }));
    const newPoints = keys.map((seriesKey, i) => ({ seriesKey, points: [{ timeMs: fromMs, value: i + 10 }] }));
    const { stores, commands } = renderChart("c1", undefined, undefined, {
      symbol: "US.AAPL", timeframe: "1m", indicators: [{ instanceId: macdId, type: "MACD", params: { fast: 12, slow: 26, signal: 9 } }],
    });
    commands.sendQuery.mockImplementation(async (name: string, raw: unknown) => {
      if (name !== "QueryChartWindow") return [];
      const args = raw as { skipBars?: boolean };
      return args.skipBars
        ? { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars: [], indicators: newPoints, historyRevision: 2 }
        : { symbol: "US.AAPL", timeframe: "1m", fromMs, toMs, bars, indicators: oldPoints, historyRevision: 1 };
    });
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    for (const { seriesKey, points } of oldPoints) expect(stores.indicators.series(seriesKey)).toEqual(points);

    fireEvent.mouseEnter(screen.getByTestId(`legend-row-${macdId}`));
    fireEvent.click(screen.getByRole("button", { name: `settings ${macdId}` }));
    fireEvent.change(screen.getByLabelText("Fast"), { target: { value: "8" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));

    for (const key of keys) expect(stores.indicators.series(key)).toEqual([]);
    expect(commands.sendCommand.mock.calls).toContainEqual([
      "SubscribeIndicator", expect.objectContaining({ instanceId: macdId, type: "MACD", params: { fast: 8, slow: 26, signal: 9 } }),
    ]);
    act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
      seq: 2, ts: "2026-08-03T01:00:01Z", kind: "indicator-ready", detail: macdId,
    } }));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    const queryCalls = commands.sendQuery.mock.calls.filter(([name]) => name === "QueryChartWindow");
    expect(queryCalls).toHaveLength(2);
    expect(queryCalls[1][1]).toEqual(expect.objectContaining({ indicatorSeriesKeys: keys, skipBars: true, tailBars: 0 }));
    for (const { seriesKey, points } of newPoints) expect(stores.indicators.series(seriesKey)).toEqual(points);
  });

  it("MACD sub-pane's close button removes all 3 of its series and persists the removal", () => {
    const { getByRole, onConfigChange } = renderChart();
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add MACD" }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({
      indicators: expect.arrayContaining([expect.objectContaining({ type: "MACD" })]),
    }));

    chartApi.removeSeries.mockClear();
    fireEvent.click(getByRole("button", { name: "close pane 1" }));
    expect(chartApi.removeSeries).toHaveBeenCalledTimes(3); // macd, signal, hist
    expect(onConfigChange).toHaveBeenLastCalledWith(expect.objectContaining({ indicators: [] }));
    expect(screen.queryByRole("button", { name: "close pane 1" })).toBeNull(); // pane gone from the legend
  });

  it("MACD sub-pane's collapse button shrinks the pane's stretch factor; clicking again restores it", () => {
    const { getByRole } = renderChart();
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add MACD" }));

    fireEvent.click(getByRole("button", { name: "collapse pane 1" }));
    expect(paneApis[1].setStretchFactor).toHaveBeenCalledTimes(1);
    expect(paneApis[1].setStretchFactor.mock.calls[0][0]).toBeLessThan(0.5);
    expect(getByRole("button", { name: "expand pane 1" })).toBeTruthy();

    fireEvent.click(getByRole("button", { name: "expand pane 1" }));
    expect(paneApis[1].setStretchFactor).toHaveBeenLastCalledWith(1); // restores the pre-collapse factor (mock's default)
    expect(getByRole("button", { name: "collapse pane 1" })).toBeTruthy();
  });

  it("repositions the MACD legend + pane-control buttons after a manual pane-separator drag, even though no store revision changed", () => {
    // Regression: dragging the pane divider changes LWC's internal pane heights
    // directly — no bar/indicator/fill/drawing revision bumps and no crosshair
    // move — so isDirty() must independently notice a pane-geometry change,
    // otherwise paint() never runs and paneOffsets (which the legend + button
    // cluster are positioned from) stays stuck at its pre-drag value.
    const scheduler = new Scheduler(browserRaf, () => {});
    let surface: Surface | undefined;
    vi.spyOn(scheduler, "register").mockImplementation((s: Surface) => { surface = s; return vi.fn(); });
    const { getByRole } = renderChart("c1", undefined, scheduler);
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add MACD" }));

    surface!.isDirty(); // baseline the store-rev + pane-geometry cursors (mirrors the Ladder/Tape pattern)

    // Simulate a manual drag: main pane shrinks, MACD sub-pane grows — heights change,
    // nothing else does.
    paneApis[0].getHeight.mockReturnValue(350);
    paneApis[1].getHeight.mockReturnValue(170);
    try {
      expect(surface!.isDirty()).toBe(true); // the pane-geometry fingerprint alone must trip dirty
      act(() => { surface!.paint(); });

      const controlBox = getByRole("button", { name: "close pane 1" }).parentElement as HTMLElement;
      expect(controlBox.style.top).toBe("356px"); // paneOffsets[1] (= new heights[0], 350) + 6
    } finally {
      paneApis[0].getHeight.mockReturnValue(400); // restore defaults for later tests in this file
      paneApis[1].getHeight.mockReturnValue(120);
    }
  });

	it("renders the bar-close-timer badge for an in-progress bar on an intraday timeframe when the setting is on", async () => {
		const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-09T13:31:05Z"));
		try {
		const { stores, getSurface, getByTestId } = renderChartCapturingSurface();
		act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
			seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
		} }));
		await act(async () => { await Promise.resolve(); await Promise.resolve(); });
		pushLiveBar(stores, "US.AAPL", "1m", 100, 100.5);
    act(() => { getSurface().paint(); });
    expect(getByTestId("bar-close-timer")).toBeTruthy();
		} finally {
			now.mockRestore();
		}
  });

  it("does not render the badge when chartSettings.barCloseTimer is off, even with an in-progress bar", () => {
    const { stores, getSurface, queryByTestId } = renderChartCapturingSurface({
      chartSettings: { ...DEFAULT_CHART_SETTINGS, barCloseTimer: false },
    });
    pushLiveBar(stores, "US.AAPL", "1m", 100, 100.5);
    act(() => { getSurface().paint(); });
    expect(queryByTestId("bar-close-timer")).toBeNull();
  });

  it("does not render the badge on a D/W/M timeframe, even with an in-progress bar and the setting on", () => {
    const { stores, getSurface, queryByTestId } = renderChartCapturingSurface({ timeframe: "D" });
    pushLiveBar(stores, "US.AAPL", "D", 100, 100.5);
    act(() => { getSurface().paint(); });
    expect(queryByTestId("bar-close-timer")).toBeNull();
  });

  it("does not render the badge when the bar is closed, even with the setting on and an intraday timeframe", () => {
    const { stores, getSurface, queryByTestId } = renderChartCapturingSurface();
    pushLiveBar(stores, "US.AAPL", "1m", 100, 100.5, false);
    act(() => { getSurface().paint(); });
    expect(queryByTestId("bar-close-timer")).toBeNull();
  });

  it("keeps the 10s badge on the last confirmed price after a quiet bucket", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-09T13:31:11Z"));
    try {
      const { stores, getSurface, getByTestId } = renderChartCapturingSurface({ timeframe: "10s" });
      act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
        seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
      } }));
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      pushLiveBar(stores, "US.AAPL", "10s", 100, 100.5);
      act(() => { getSurface().paint(); });
      expect(getByTestId("bar-close-timer")).toBeTruthy();
      expect(getByTestId("bar-close-timer-price").textContent).toBe("100.50");
    } finally {
      now.mockRestore();
    }
  });

  it("keeps the badge visible when the real next 10s bar arrives slightly early", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-09T13:31:08Z"));
    try {
      const { stores, getSurface, getByTestId } = renderChartCapturingSurface({ timeframe: "10s" });
      act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
        seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
      } }));
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      pushLiveBar(stores, "US.AAPL", "10s", 100, 101.25, true, "2026-07-09T13:31:10.000Z");
      act(() => { getSurface().paint(); });
      expect(getByTestId("bar-close-timer")).toBeTruthy();
      expect(getByTestId("bar-close-timer-price").textContent).toBe("101.25");
    } finally {
      now.mockRestore();
    }
  });

  it("hides a 10s badge for a candidate that arrives too far ahead", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-09T13:31:04Z"));
    try {
      const { stores, getSurface, queryByTestId } = renderChartCapturingSurface({ timeframe: "10s" });
      act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
        seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
      } }));
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      pushLiveBar(stores, "US.AAPL", "10s", 100, 101.25, true, "2026-07-09T13:31:10.000Z");
      act(() => { getSurface().paint(); });
      expect(queryByTestId("bar-close-timer")).toBeNull();
    } finally {
      now.mockRestore();
    }
  });

  it("falls back to the previous same-session price when a far-future 10s bar is present", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-09T13:31:04Z"));
    try {
      const { stores, getSurface, getByTestId } = renderChartCapturingSurface({ timeframe: "10s" });
      act(() => stores.health.apply({ kind: "delta", topic: "sys.events", payload: {
        seq: 1, ts: "2026-08-03T01:00:00Z", kind: "chart-ready", detail: "US.AAPL",
      } }));
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      pushLiveBar(stores, "US.AAPL", "10s", 100, 100, false, "2026-07-09T13:31:00.000Z");
      pushLiveBar(stores, "US.AAPL", "10s", 101, 101.25, true, "2026-07-09T13:31:20.000Z");
      act(() => { getSurface().paint(); });
      expect(getByTestId("bar-close-timer")).toBeTruthy();
      expect(getByTestId("bar-close-timer-price").textContent).toBe("100.00");
    } finally {
      now.mockRestore();
    }
  });

  it("isDirty reacts only to its own pinned symbol's bar revision and its own indicator instances' revisions — not a foreign symbol's bar delta or an unrelated instance's update (per-key scoping regression)", () => {
    const { getByRole, stores, getSurface, onConfigChange } = renderChartCapturingSurface(); // pinned to US.AAPL/1m via config.settings

    // Add 2 indicator instances (the picker auto-closes after each add — see
    // ChartHeaderControls — so reopen it before the second add).
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add VWAP" }));

    // Recover this chart's own EMA instanceId from the persisted config patch
    // (mirrors the "scopes indicator instanceIds" test above).
    type Persisted = { indicators: { instanceId: string; type: string }[] };
    const persisted = (onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators;
    const emaId = persisted.find((i) => i.type === "EMA")!.instanceId;

    getSurface().isDirty(); // baseline: consume the mount + indicator-add dirty state

    // A different symbol's bar delta must NOT dirty a chart pinned to US.AAPL —
    // this is the actual bug this task fixes (isDirty used to read global revs).
    pushLiveBar(stores, "US.NVDA", "1m", 100, 100.5);
    expect(getSurface().isDirty()).toBe(false);

    // The pinned symbol's own bar delta must dirty it.
    pushLiveBar(stores, "US.AAPL", "1m", 100, 100.5);
    expect(getSurface().isDirty()).toBe(true);

    // An update to one of this chart's OWN indicator instances must dirty it.
    stores.indicators.apply({ kind: "delta", topic: "md.indicator", key: emaId, payload: { timeMs: Date.now(), value: 1 } });
    expect(getSurface().isDirty()).toBe(true);

    // An update to an unrelated indicator instance's key (not one of this
    // chart's active instances) must NOT dirty it.
    stores.indicators.apply({ kind: "delta", topic: "md.indicator", key: "other-panel:EMA-0", payload: { timeMs: Date.now(), value: 1 } });
    expect(getSurface().isDirty()).toBe(false);
  });

  it("dirties a 10s chart when the wall-clock bucket advances without a store revision", () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-06T13:30:01Z"));
    const { getSurface } = renderChartCapturingSurface({ timeframe: "10s" });
    getSurface().isDirty();
    expect(getSurface().isDirty()).toBe(false);
    now.mockReturnValue(Date.parse("2026-07-06T13:30:11Z"));
    expect(getSurface().isDirty()).toBe(true);
    now.mockRestore();
  });

  it("dirties a 10s chart when OpenD health changes", () => {
    const { stores, getSurface } = renderChartCapturingSurface({ timeframe: "10s" });
    getSurface().isDirty();
    act(() => stores.health.apply({ kind: "snapshot", topic: "sys.health", payload: {
      links: [{ link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" }],
    } }));
    expect(getSurface().isDirty()).toBe(true);
  });

  it("does not dirty a 1m chart when OpenD health changes", () => {
    const { stores, getSurface } = renderChartCapturingSurface({ timeframe: "1m" });
    getSurface().isDirty();
    act(() => stores.health.apply({ kind: "snapshot", topic: "sys.health", payload: {
      links: [{ link: "engine-moomoo", ms: null, min: null, avg: null, max: null, status: "down" }],
    } }));
    expect(getSurface().isDirty()).toBe(false);
  });

  it("dirties a 1m chart when the wall-clock bucket advances without a store revision", () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-07-06T13:30:58Z"));
    const { getSurface } = renderChartCapturingSurface({ timeframe: "1m" });
    getSurface().isDirty();
    expect(getSurface().isDirty()).toBe(false);
    now.mockReturnValue(Date.parse("2026-07-06T13:31:00Z"));
    expect(getSurface().isDirty()).toBe(true);
    now.mockRestore();
  });

  it("isDirty tracks MACD's multi-slot sub-keys, not just the base instanceId (multi-slot indicator regression)", () => {
    // The headline "isDirty reacts only to its own pinned symbol" test above only
    // exercises EMA/VWAP — both single-slot indicators whose describeIndicator()
    // key IS the base instanceId. MACD is the multi-slot case: describeIndicator
    // produces 3 keys — the base instanceId plus `${instanceId}#signal` and
    // `${instanceId}#hist` (see indicatorSeries.ts's describeIndicator: entry.slots
    // has length 3 for MACD, so `single` is false and every slot's key is
    // `${inst.instanceId}#${s.slot}`). A delta keyed to one of the SUFFIXED
    // sub-keys — not the base id — must still dirty this chart, proving isDirty()'s
    // sum-over-active-keys loop genuinely iterates every slot describeIndicator
    // returns for the instance, not just its base id.
    const { getByRole, stores, getSurface, onConfigChange } = renderChartCapturingSurface();

    fireEvent.click(getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "add MACD" }));

    type Persisted = { indicators: { instanceId: string; type: string }[] };
    const persisted = (onConfigChange.mock.calls.at(-1)![0] as Persisted).indicators;
    const macdId = persisted.find((i) => i.type === "MACD")!.instanceId;

    getSurface().isDirty(); // baseline: consume the mount + indicator-add dirty state

    stores.indicators.apply({ kind: "delta", topic: "md.indicator", key: `${macdId}#signal`, payload: { timeMs: Date.now(), value: 1 } });
    expect(getSurface().isDirty()).toBe(true);
  });

  it("does not call perf.recordScan when perf is disabled (mirrors TapePanel's guard — avoids the `chart:${config.id}` template-literal allocation on every hot-path paint)", () => {
    const { stores, getSurface } = renderChartCapturingSurface();
    pushLiveBar(stores, "US.AAPL", "1m", 100, 100.5);
    expect(perf.enabled).toBe(false); // sanity: shared singleton's default state
    const spy = vi.spyOn(perf, "recordScan");
    act(() => { getSurface().paint(); });
    expect(spy).not.toHaveBeenCalled();
    spy.mockRestore();
  });

  it("reports controller.lastSyncDaySegmentBuilds() to the shared perf singleton, keyed by the chart:<id> surface id, while perf is enabled (Task 6 diagnostic probe)", () => {
    const { stores, getSurface } = renderChartCapturingSurface();
    pushLiveBar(stores, "US.AAPL", "1m", 100, 100.5);
    const spy = vi.spyOn(perf, "recordScan");
    perf.enabled = true;
    try {
      act(() => { getSurface().paint(); });
      expect(spy).toHaveBeenCalledWith("chart:c1", expect.any(Number));
    } finally {
      perf.enabled = false; // restore the shared singleton's default for other tests
      spy.mockRestore();
    }
  });

});
