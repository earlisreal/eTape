// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, within, screen } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";

// Mock lightweight-charts so the panel test never touches a real canvas.
// timeScaleApi is a stable object (not a fresh literal per call) so a test can hold
// a reference to e.g. resetTimeScale and assert it was invoked by the SUT.
const timeScaleApi = { timeToCoordinate: vi.fn(() => 0), scrollToRealTime: vi.fn(), scrollPosition: vi.fn(() => 0),
  coordinateToLogical: vi.fn(() => 0), logicalToCoordinate: vi.fn(() => 0), resetTimeScale: vi.fn(),
  scrollToPosition: vi.fn(), subscribeVisibleLogicalRangeChange: vi.fn(), unsubscribeVisibleLogicalRangeChange: vi.fn() };
const chartApi = {
  addSeries: vi.fn(() => ({ setData: vi.fn(), update: vi.fn(), applyOptions: vi.fn(), setSeriesOrder: vi.fn(),
    attachPrimitive: vi.fn(), priceToCoordinate: vi.fn(() => 0), coordinateToPrice: vi.fn(() => 0) })),
  removeSeries: vi.fn(),
  panes: vi.fn(() => [{ attachPrimitive: vi.fn(), getHeight: vi.fn(() => 400) }]),
  priceScale: vi.fn(() => ({ applyOptions: vi.fn() })),
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
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg } from "../../wire/contract";
import { FakeDrawingBus, FakeDrawingBusHub } from "../../../test/fakes";

// jsdom has no ResizeObserver; ChartPanel's resize wiring only needs observe/disconnect.
class MockResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
vi.stubGlobal("ResizeObserver", MockResizeObserver);

beforeEach(() => { vi.clearAllMocks(); cleanup(); });

function renderChart(id = "c1", sharedStores?: ReturnType<typeof makeStores>) {
  const stores = sharedStores ?? makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const commands = {
    sendCommand: vi.fn(async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" })),
    sendQuery: vi.fn(async () => []),
  };
  const config = { id, panelId: "chart", group: "green" as const, settings: { symbol: "US.AAPL", timeframe: "1m" } };
  const onConfigChange = vi.fn();
  const utils = render(
    <ThemeProvider>
      <ChartPanel config={config} stores={stores} scheduler={scheduler} width={400} height={300}
        linkGroups={linkGroups} commands={commands} onConfigChange={onConfigChange} />
    </ThemeProvider>,
  );
  return { ...utils, stores, onConfigChange };
}

describe("ChartPanel", () => {
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

  it("caps rightward panning at RIGHT_OFFSET_BARS by subscribing a visible-range clamp, and unsubscribes on unmount", () => {
    const { unmount } = renderChart();
    expect(timeScaleApi.subscribeVisibleLogicalRangeChange).toHaveBeenCalledTimes(1);
    const clampRight = timeScaleApi.subscribeVisibleLogicalRangeChange.mock.calls[0][0] as () => void;

    // Panned past the cap: snap back to RIGHT_OFFSET_BARS (4), no bar-spacing change.
    timeScaleApi.scrollPosition.mockReturnValue(20);
    clampRight();
    expect(timeScaleApi.scrollToPosition).toHaveBeenCalledWith(4, false);

    // Within bounds (resting position or scrolled into history): no snap.
    timeScaleApi.scrollToPosition.mockClear();
    timeScaleApi.scrollPosition.mockReturnValue(4);
    clampRight();
    timeScaleApi.scrollPosition.mockReturnValue(-2);
    clampRight();
    expect(timeScaleApi.scrollToPosition).not.toHaveBeenCalled();

    unmount();
    expect(timeScaleApi.unsubscribeVisibleLogicalRangeChange).toHaveBeenCalledWith(clampRight);
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

  it("right-click opens a context menu; Reset chart view calls the chart's resetTimeScale", () => {
    const { getByTestId, getByRole } = renderChart();
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    fireEvent.click(getByRole("button", { name: "Reset chart view" }));
    expect(timeScaleApi.resetTimeScale).toHaveBeenCalledTimes(1);
  });

  it("right-click menu's Remove all drawings clears this symbol's drawings", () => {
    const stores = makeStores();
    stores.drawings.upsert({ id: "d", symbol: "US.AAPL", kind: "hline", anchors: [{ timeMs: 0, price: 1 }], createdMs: 1, updatedMs: 1 });
    const { getByTestId, getByRole } = renderChart("c1", stores);
    fireEvent.contextMenu(getByTestId("chart-host"), { clientX: 20, clientY: 30 });
    fireEvent.click(getByRole("button", { name: "Remove all drawings" }));
    expect(stores.drawings.forSymbol("US.AAPL")).toHaveLength(0);
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

  it("renders the TV toolbar and persists a timeframe change", () => {
    const { getByRole, onConfigChange } = renderChart();
    fireEvent.click(getByRole("button", { name: "timeframe 5m" }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({ timeframe: "5m" }));
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
});
