import type { Palette } from "../palette";

// Loose structural types — these mirror the subset of LWC v5 ChartOptions /
// series options we set, without importing LWC's option types (keeps the module
// pure + trivially testable). ChartPanel passes them to createChart/addSeries as-is.
export interface DeepChartOptions {
  layout?: { background?: { type: "solid"; color: string }; textColor?: string };
  grid?: { vertLines?: { color: string }; horzLines?: { color: string } };
  crosshair?: { mode?: number; vertLine?: { color: string }; horzLine?: { color: string } };
  rightPriceScale?: { borderColor: string; scaleMargins?: { top: number; bottom: number }; minimumWidth?: number };
  timeScale?: {
    borderColor: string; rightOffset: number; secondsVisible: boolean; timeVisible: boolean;
    fixRightEdge?: boolean; shiftVisibleRangeOnNewBar?: boolean;
  };
  autoSize?: boolean;
}
export interface CandleOpts {
  upColor: string; downColor: string;
  wickUpColor: string; wickDownColor: string;
  borderUpColor: string; borderDownColor: string;
  borderVisible: boolean;
}
export interface HistogramOpts {
  priceScaleId: string;
  priceFormat: { type: "volume" };
  color?: string;
  lastValueVisible?: boolean;
  priceLineVisible?: boolean;
}

// CrosshairMode.Magnet === 1 in LWC — crosshair snaps to the nearest bar
// vertically while floating horizontally (the wickplot convention).
const CROSSHAIR_MAGNET = 1;

// Volume rides an invisible overlay scale confined to the bottom VOLUME_BAND of
// the main pane; the candle (right) scale reserves that same band at its bottom
// so the two never overlap. Without these margins LWC's default scaleMargins let
// the volume histogram autoscale across ~80% of the pane, swallowing the candles.
export const VOLUME_BAND = 0.25;
export const CANDLE_SCALE_MARGINS = { top: 0.08, bottom: VOLUME_BAND } as const;
export const VOLUME_SCALE_MARGINS = { top: 1 - VOLUME_BAND, bottom: 0 } as const;

// TradingView draws studies as thin lines behind the price action, not the LWC
// default (3px, drawn on top). See ChartController's indicator series creation.
export const INDICATOR_LINE_WIDTH = 1;

export function chartOptions(p: Palette): DeepChartOptions {
  return {
    layout: { background: { type: "solid", color: p.bg }, textColor: p.text },
    grid: { vertLines: { color: p.grid }, horzLines: { color: p.grid } },
    crosshair: {
      mode: CROSSHAIR_MAGNET,
      vertLine: { color: p.crosshair },
      horzLine: { color: p.crosshair },
    },
    // minimumWidth: keeps the right axis column from re-sizing (and shifting the whole
    // plot area) as tick-label widths change with price digit count.
    rightPriceScale: { borderColor: p.border, scaleMargins: CANDLE_SCALE_MARGINS, minimumWidth: 64 },
    timeScale: {
      borderColor: p.border, rightOffset: 5, secondsVisible: true, timeVisible: true,
      // fixRightEdge: max forward pan is the latest bar + the rightOffset padding above.
      // shiftVisibleRangeOnNewBar: once at that edge, new bars auto-scroll into view.
      fixRightEdge: true, shiftVisibleRangeOnNewBar: true,
    },
    autoSize: false, // we drive resize via ResizeObserver → controller.resize()
  };
}

export function candleOptions(p: Palette): CandleOpts {
  return {
    upColor: p.up, downColor: p.down,
    wickUpColor: p.up, wickDownColor: p.down,
    borderUpColor: p.up, borderDownColor: p.down,
    borderVisible: true,
  };
}

export function volumeOptions(p: Palette): HistogramOpts {
  void p; // signature parity with chartOptions/candleOptions; volume color is per-bar, not palette-level
  // Overlaid on the main pane, its own invisible scale; per-bar color is set on
  // each data point (up/down) at setData/update time by the controller.
  // lastValueVisible/priceLineVisible: false — the volume overlay is a background
  // histogram, not a tracked series; its last-value label's width varies with
  // magnitude (e.g. "1.2M" vs "823.4K") and previously made the shared right axis
  // column (and so the whole plot area) resize/shift as new bars streamed in.
  return { priceScaleId: "", priceFormat: { type: "volume" }, lastValueVisible: false, priceLineVisible: false };
}
