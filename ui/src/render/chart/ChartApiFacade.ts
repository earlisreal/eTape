import type { Band } from "./sessions";
import type { FillMarker } from "./diamondMarker";

export interface LwcSeries {
  setData(data: unknown[]): void;
  update(bar: unknown): void;
  applyOptions(options: unknown): void;
  // Zero-based draw-order index within the series' pane — higher renders on top.
  // Used to keep the candle painted over overlay indicator lines (LWC v5.0.6+).
  setSeriesOrder(order: number): void;
}

export type MainKind = "candle" | "bar" | "line" | "area";

// The minimal slice of Lightweight Charts v5 the controller drives. ChartPanel
// implements this over a real IChartApi; ChartController.test.ts implements a fake.
export interface ChartApiFacade {
  // The main price series (candle/bar/line/area). Recreated in place on a
  // chart-type change; always carries the diamond + drawings primitives and is the
  // series used for price<->coordinate conversion.
  setMainSeries(kind: MainKind, options: unknown): LwcSeries;
  // Indicator + volume series only (never the main series — no primitives attached).
  addSeries(kind: "line" | "histogram", options: unknown, paneIndex: number): LwcSeries;
  removeSeries(series: LwcSeries): void;
  // Configure the margins of a price scale by id ("" is the volume overlay scale).
  // The right (candle) scale is configured via chart options, not this method.
  setPriceScaleMargins(priceScaleId: string, margins: { top: number; bottom: number }): void;
  setSessionBands(bands: Band[]): void;  // forwarded to the session pane-primitive
  setFillMarkers(markers: FillMarker[]): void; // forwarded to the diamond series-primitive
  timeToCoordinate(timeMs: number): number | null;
  priceToCoordinate(price: number): number | null;
  logicalToCoordinate(logical: number): number | null;
  coordinateToLogical(x: number): number | null;
  coordinateToPrice(y: number): number | null;
  setPanZoomEnabled(on: boolean): void;
  scrollToRealTime(): void;
  resetTimeScale(): void; // default bar spacing + scroll to the latest bar
  resize(width: number, height: number): void;
  applyOptions(options: unknown): void;
  // TV chrome additions:
  takeScreenshot(): HTMLCanvasElement;               // PNG export (camera button)
  subscribeCrosshairMove(cb: (logical: number | null) => void): () => void; // legend value tracking
  paneHeights(): number[];                            // legend per-pane vertical offsets
  remove(): void;
}
