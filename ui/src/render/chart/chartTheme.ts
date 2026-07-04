import type { Palette } from "../palette";

// Loose structural types — these mirror the subset of LWC v5 ChartOptions /
// series options we set, without importing LWC's option types (keeps the module
// pure + trivially testable). ChartPanel passes them to createChart/addSeries as-is.
export interface DeepChartOptions {
  layout?: { background?: { type: "solid"; color: string }; textColor?: string };
  grid?: { vertLines?: { color: string }; horzLines?: { color: string } };
  crosshair?: { mode?: number; vertLine?: { color: string }; horzLine?: { color: string } };
  rightPriceScale?: { borderColor: string };
  timeScale?: { borderColor: string; rightOffset: number; secondsVisible: boolean; timeVisible: boolean };
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
}

// CrosshairMode.Magnet === 1 in LWC — crosshair snaps to the nearest bar
// vertically while floating horizontally (the wickplot convention).
const CROSSHAIR_MAGNET = 1;

export function chartOptions(p: Palette): DeepChartOptions {
  return {
    layout: { background: { type: "solid", color: p.bg }, textColor: p.text },
    grid: { vertLines: { color: p.grid }, horzLines: { color: p.grid } },
    crosshair: {
      mode: CROSSHAIR_MAGNET,
      vertLine: { color: p.crosshair },
      horzLine: { color: p.crosshair },
    },
    rightPriceScale: { borderColor: p.border },
    timeScale: { borderColor: p.border, rightOffset: 5, secondsVisible: true, timeVisible: true },
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
  return { priceScaleId: "", priceFormat: { type: "volume" } };
}
