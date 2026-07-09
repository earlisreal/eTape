// TradingView-faithful canvas palettes + DOM chrome tokens for the chart panel.
// TV_LIGHT/TV_DARK satisfy the existing Palette interface so ChartController,
// chartTheme, and the primitives re-theme with zero changes — the chart panel
// simply hands them a TV palette instead of the Daylight-Ledger one.
import type { Palette, ThemeMode } from "../palette";

export const TV_LIGHT: Palette = {
  bg: "#FFFFFF",
  surface: "#FFFFFF",
  border: "#E0E3EB",
  borderStrong: "#D1D4DC",
  text: "#131722",
  textMuted: "#787B86",
  grid: "rgba(42,46,57,.06)",
  crosshair: "#787B86",
  up: "#089981",
  down: "#F23645",
  volUp: "rgba(8,153,129,.5)",
  volDown: "rgba(242,54,69,.5)",
  buyFill: "rgba(8,153,129,.25)",
  sellFill: "rgba(242,54,69,.25)",
  fillOutline: "#131722",
  neutral: "#787B86",
  depthBid: "rgba(8,153,129,.12)",
  depthAsk: "rgba(242,54,69,.12)",
  flashBuy: "rgba(8,153,129,.35)",
  flashSell: "rgba(242,54,69,.35)",
  flashNeutral: "rgba(120,123,134,.25)",
  orderMark: "#2962FF",
  sessionPre: "rgba(120,123,134,.06)",
  sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(120,123,134,.06)",
  sessionClosed: "rgba(120,123,134,.10)",
  indVwap: "#7E57C2",
  indEma: "#2962FF",
  indSma: "#FF6D00",
  indMacdLine: "#2962FF",
  indMacdSignal: "#FF6D00",
  indMacdHist: "rgba(8,153,129,.5)",
  linkRed: "#F23645",
  linkGreen: "#089981",
  linkBlue: "#2962FF",
  linkYellow: "#F7A600",
  accent: "#2962FF",
  ok: "#089981",
  warn: "#F7A600",
  danger: "#F23645",
};

export const TV_DARK: Palette = {
  bg: "#131722",
  surface: "#1E222D",
  border: "#2A2E39",
  borderStrong: "#2A2E39",
  text: "#D1D4DC",
  textMuted: "#787B86",
  grid: "rgba(240,243,250,.06)",
  crosshair: "#787B86",
  up: "#089981",
  down: "#F23645",
  volUp: "rgba(8,153,129,.5)",
  volDown: "rgba(242,54,69,.5)",
  buyFill: "rgba(8,153,129,.28)",
  sellFill: "rgba(242,54,69,.28)",
  fillOutline: "#D1D4DC",
  neutral: "#787B86",
  depthBid: "rgba(8,153,129,.14)",
  depthAsk: "rgba(242,54,69,.14)",
  flashBuy: "rgba(8,153,129,.4)",
  flashSell: "rgba(242,54,69,.4)",
  flashNeutral: "rgba(240,243,250,.18)",
  orderMark: "#2962FF",
  sessionPre: "rgba(240,243,250,.05)",
  sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(240,243,250,.05)",
  sessionClosed: "rgba(240,243,250,.09)",
  indVwap: "#7E57C2",
  indEma: "#2962FF",
  indSma: "#FF6D00",
  indMacdLine: "#2962FF",
  indMacdSignal: "#FF6D00",
  indMacdHist: "rgba(8,153,129,.5)",
  linkRed: "#F23645",
  linkGreen: "#089981",
  linkBlue: "#2962FF",
  linkYellow: "#F7A600",
  accent: "#2962FF",
  ok: "#089981",
  warn: "#F7A600",
  danger: "#F23645",
};

export function getTvPalette(mode: ThemeMode): Palette {
  return mode === "dark" ? TV_DARK : TV_LIGHT;
}

// DOM chrome tokens for the toolbar/legend/menu/dialogs. These are NOT the same
// as the CSS vars the app-wide ThemeProvider publishes (those are Daylight-Ledger
// colors) — TV chrome components import these directly so the chart panel stays a
// self-contained visual island.
export interface TvChrome {
  bg: string;
  surface: string;
  border: string;
  text: string;
  muted: string;
  hover: string;
  accent: string;
  up: string;
  down: string;
}

const CHROME_LIGHT: TvChrome = {
  bg: "#FFFFFF",
  surface: "#FFFFFF",
  border: "#E0E3EB",
  text: "#131722",
  muted: "#787B86",
  hover: "#F0F3FA",
  accent: "#2962FF",
  up: "#089981",
  down: "#F23645",
};

const CHROME_DARK: TvChrome = {
  bg: "#131722",
  surface: "#1E222D",
  border: "#2A2E39",
  text: "#D1D4DC",
  muted: "#787B86",
  hover: "#2A2E39",
  accent: "#2962FF",
  up: "#089981",
  down: "#F23645",
};

export function getTvChrome(mode: ThemeMode): TvChrome {
  return mode === "dark" ? CHROME_DARK : CHROME_LIGHT;
}

// Preset color swatches shared by every style editor in the TV chrome (drawing
// floating toolbar, indicator settings dialog) — one palette, no color wheel.
export const TV_SWATCHES = ["#2962FF", "#089981", "#F23645", "#FF6D00", "#7E57C2", "#787B86", "#131722", "#FFFFFF"] as const;

export const TV_FONT = `-apple-system, "Trebuchet MS", Roboto, Ubuntu, sans-serif`;

export const TV_GEOM = {
  iconBtn: 28,
  radius: 6,
  separator: 1,
  uiFont: 12,
  axisFont: 11,
} as const;
