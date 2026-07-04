// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";

// Mock lightweight-charts so the panel test never touches a real canvas.
const chartApi = {
  addSeries: vi.fn(() => ({ setData: vi.fn(), update: vi.fn(), applyOptions: vi.fn(),
    attachPrimitive: vi.fn(), priceToCoordinate: vi.fn(() => 0) })),
  removeSeries: vi.fn(),
  panes: vi.fn(() => [{ attachPrimitive: vi.fn() }]),
  timeScale: vi.fn(() => ({ timeToCoordinate: vi.fn(() => 0), scrollToRealTime: vi.fn(), scrollPosition: vi.fn(() => 0) })),
  applyOptions: vi.fn(), resize: vi.fn(), remove: vi.fn(),
};
vi.mock("lightweight-charts", () => ({
  createChart: vi.fn(() => chartApi),
  CandlestickSeries: "Candlestick", HistogramSeries: "Histogram", LineSeries: "Line", CrosshairMode: { Magnet: 1 },
}));

import { ChartPanel } from "./ChartPanel";
import { makeStores } from "../../data/registry";
import { Scheduler } from "../../render/Scheduler";
import { browserRaf } from "../../render/surface";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";

// jsdom has no ResizeObserver; ChartPanel's resize wiring only needs observe/disconnect.
class MockResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
vi.stubGlobal("ResizeObserver", MockResizeObserver);

beforeEach(() => { vi.clearAllMocks(); cleanup(); });

function renderChart() {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const commands = { sendCommand: vi.fn(async () => ({ status: "accepted" })) };
  const config = { id: "c1", panelId: "chart", group: "green" as const, settings: { symbol: "US.AAPL", timeframe: "1m" } };
  return render(
    <ThemeProvider>
      <ChartPanel config={config} stores={stores} scheduler={scheduler} width={400} height={300}
        linkGroups={linkGroups} commands={commands} onConfigChange={vi.fn()} />
    </ThemeProvider>,
  );
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
});
