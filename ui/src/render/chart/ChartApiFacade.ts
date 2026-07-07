import type { Band } from "./sessions";
import type { FillMarker } from "./diamondMarker";

export interface LwcSeries {
  setData(data: unknown[]): void;
  update(bar: unknown): void;
  applyOptions(options: unknown): void;
}

// The minimal slice of Lightweight Charts v5 the controller drives. ChartPanel
// implements this over a real IChartApi; ChartController.test.ts implements a fake.
export interface ChartApiFacade {
  addSeries(kind: "candle" | "line" | "histogram", options: unknown, paneIndex: number): LwcSeries;
  removeSeries(series: LwcSeries): void;
  setSessionBands(bands: Band[]): void;  // forwarded to the session pane-primitive
  setFillMarkers(markers: FillMarker[]): void; // forwarded to the diamond series-primitive
  timeToCoordinate(timeMs: number): number | null;
  priceToCoordinate(price: number): number | null;
  scrollToRealTime(): void;
  resize(width: number, height: number): void;
  applyOptions(options: unknown): void;
  remove(): void;
}
