// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, within } from "@testing-library/react";
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
import type { AckMsg } from "../../wire/contract";

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
  const utils = render(
    <ThemeProvider>
      <ChartPanel config={config} stores={stores} scheduler={scheduler} width={400} height={300}
        linkGroups={linkGroups} commands={commands} onConfigChange={vi.fn()} />
    </ThemeProvider>,
  );
  return { ...utils, stores };
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

  it("scopes indicator instanceIds to the panel, so two panels adding the same indicator type don't collide (Finding 2 regression)", () => {
    // Both panels share ONE store instance, exactly as App.tsx's single makeStores()
    // call shares BarStore/IndicatorStore across every chart panel in a workspace.
    const stores = makeStores();
    const { container: c1 } = renderChart("panel-a", stores);
    const { container: c2 } = renderChart("panel-b", stores);

    const addSelect1 = within(c1).getByLabelText("add indicator") as HTMLSelectElement;
    const addSelect2 = within(c2).getByLabelText("add indicator") as HTMLSelectElement;
    fireEvent.change(addSelect1, { target: { value: "VWAP" } });
    fireEvent.change(addSelect2, { target: { value: "VWAP" } });

    // The color-picker aria-label is `${inst.instanceId} ${slot} color` (ChartControls.tsx),
    // so we can recover the minted instanceId for each panel's VWAP instance from the DOM.
    const colorInput1 = within(c1).getByLabelText(/line color$/i);
    const colorInput2 = within(c2).getByLabelText(/line color$/i);
    const id1 = colorInput1.getAttribute("aria-label")!.replace(/ line color$/, "");
    const id2 = colorInput2.getAttribute("aria-label")!.replace(/ line color$/, "");

    // Before the fix both would be "VWAP-0" (idSeq is per-panel but unscoped) —
    // colliding in the shared IndicatorStore keyed solely by instanceId.
    expect(id1).not.toBe(id2);
    expect(id1.startsWith("panel-a:")).toBe(true);
    expect(id2.startsWith("panel-b:")).toBe(true);
  });
});
