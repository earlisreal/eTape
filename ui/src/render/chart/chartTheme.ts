import type { Palette } from "../palette";

// Loose structural types — these mirror the subset of LWC v5 ChartOptions /
// series options we set, without importing LWC's option types (keeps the module
// pure + trivially testable). ChartPanel passes them to createChart/addSeries as-is.
export interface DeepChartOptions {
  layout?: { background?: { type: "solid"; color: string }; textColor?: string };
  grid?: { vertLines?: { color: string }; horzLines?: { color: string } };
  crosshair?: { mode?: number; vertLine?: { color: string }; horzLine?: { color: string } };
  rightPriceScale?: { borderColor: string; scaleMargins?: { top: number; bottom: number }; minimumWidth?: number };
  localization?: { timeFormatter?: (time: number) => string };
  timeScale?: {
    borderColor: string; rightOffset: number; secondsVisible: boolean; timeVisible: boolean;
    // fixRightEdge deliberately omitted from this surface — see chartOptions()'s
    // comment: LWC hardcodes the max right offset to 0 whenever it's set,
    // clamping any positive rightOffset back to 0.
    fixLeftEdge?: boolean; shiftVisibleRangeOnNewBar?: boolean;
    tickMarkFormatter?: (time: number, tickMarkType: number, locale: string) => string | null;
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

// The chart trades in UTCTimestamp seconds (see ChartController's toLwcTime),
// and Lightweight Charts renders axis/crosshair labels in UTC unless told
// otherwise. eTape is US-equities-only (CLAUDE.md), so every label — axis tick
// marks and the crosshair time — must read US/Eastern instead. Intl formatters
// are built once at module scope (not per tick) and reuse the America/New_York
// idiom already established in render/format.ts / barBucket.ts.
const ET_ZONE = "America/New_York";
const ET_TICK = {
  year: new Intl.DateTimeFormat("en-US", { timeZone: ET_ZONE, year: "numeric" }),
  month: new Intl.DateTimeFormat("en-US", { timeZone: ET_ZONE, month: "short" }),
  day: new Intl.DateTimeFormat("en-US", { timeZone: ET_ZONE, month: "short", day: "numeric" }),
  time: new Intl.DateTimeFormat("en-US", { timeZone: ET_ZONE, hour12: false, hour: "2-digit", minute: "2-digit" }),
  timeWithSeconds: new Intl.DateTimeFormat("en-US", {
    timeZone: ET_ZONE, hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit",
  }),
};
const ET_CROSSHAIR = new Intl.DateTimeFormat("en-US", {
  timeZone: ET_ZONE, hour12: false, month: "short", day: "numeric",
  hour: "2-digit", minute: "2-digit", second: "2-digit",
});

// TickMarkType (LWC v5): Year=0, Month=1, DayOfMonth=2, Time=3, TimeWithSeconds=4.
// `time` is always a UTCTimestamp (seconds) for our data (every timeframe, incl.
// D/W/M, goes through toLwcTime/toLwcTimeMs) — guard defensively anyway so an
// unexpected shape falls back to LWC's own default formatter (`null`) instead
// of throwing mid-paint.
function tickMarkFormatter(time: number, tickMarkType: number): string | null {
  if (typeof time !== "number") return null;
  const ms = time * 1000;
  switch (tickMarkType) {
    case 0: return ET_TICK.year.format(ms);
    case 1: return ET_TICK.month.format(ms);
    case 2: return ET_TICK.day.format(ms);
    case 3: return ET_TICK.time.format(ms);
    case 4: return ET_TICK.timeWithSeconds.format(ms);
    default: return null;
  }
}
function timeFormatter(time: number): string {
  return typeof time === "number" ? ET_CROSSHAIR.format(time * 1000) : String(time);
}

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
    localization: { timeFormatter },
    timeScale: {
      borderColor: p.border, rightOffset: 4, secondsVisible: true, timeVisible: true,
      // rightOffset alone (no fixRightEdge): verified against
      // lightweight-charts.development.mjs — TimeScale._private__maxRightOffset()
      // returns the literal constant 0 whenever fixRightEdge is true, REGARDLESS
      // of rightOffset's value. That clamp runs on every _correctOffset() call
      // (initial load, scrollToRealTime, resetTimeScale, every resize), so
      // fixRightEdge+rightOffset together always collapse to zero padding — the
      // 4-bar right margin never actually appeared with fixRightEdge set. Leaving
      // it unset (default false) lets rightOffset's margin take effect; the
      // tradeoff is the user can scroll further right into blank future space
      // (LWC has no "capped but non-zero" right-edge mode).
      // fixLeftEdge: max backward pan is the first data point. This one DOES work
      // as intended — unlike maxRightOffset, TimeScale._private__minRightOffset()
      // derives its cap from the first data point's actual index rather than a
      // hardcoded constant — so prepending LEFT_PAD_BARS whitespace ahead of the
      // earliest real bar (ChartController.setAllBars) correctly shifts this cap
      // to leave that same empty-bar margin on the left.
      // shiftVisibleRangeOnNewBar: once at the right edge, new bars auto-scroll into view.
      fixLeftEdge: true, shiftVisibleRangeOnNewBar: true,
      tickMarkFormatter,
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
