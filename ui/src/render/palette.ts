// The single color source of truth for eTape. The LWC chart theme, every custom
// painter/primitive, and all chrome derive from this. Painters receive a Palette
// in their paint state — never read one from a global. Light is the app default.
export type ThemeMode = "light" | "dark";

export interface Palette {
  // surfaces / structure
  bg: string;          // panel + chart background
  surface: string;     // headers, controls
  border: string;      // hairlines
  borderStrong: string; // double rules, control borders, ladder center divider
  text: string;        // primary text
  textMuted: string;   // secondary text, axis labels
  grid: string;        // chart grid lines
  crosshair: string;   // crosshair line
  // candles + volume
  up: string;          // bullish candle body/wick/border
  down: string;        // bearish candle body/wick/border
  volUp: string;       // up-bar volume (rgba, semi-transparent)
  volDown: string;     // down-bar volume (rgba, semi-transparent)
  // fills-on-chart (diamond markers)
  buyFill: string;     // buy diamond fill — deeper than `up` so it reads with no outline
  sellFill: string;    // sell diamond fill — deeper than `down` so it reads with no outline
  shortFill: string;   // short-sale diamond fill — distinct blue, never mistaken for a sell
  fillOutline: string; // unused by the diamond marker (no outline drawn); kept for tvTheme parity
  // ladder / tape (Plan 3)
  neutral: string;      // NEUTRAL tick prints + last-trade text with no direction
  depthBid: string;     // ladder cumulative depth bar fill, bid side (rgba, low alpha)
  depthAsk: string;     // ladder cumulative depth bar fill, ask side (rgba, low alpha)
  flashBuy: string;     // last-trade flash row fill at full strength (painter decays via globalAlpha)
  flashSell: string;
  flashNeutral: string;
  orderMark: string;    // display-only working-order marks on the ladder
  // ET session shading (rgba, low alpha — drawn behind bars)
  sessionPre: string;
  sessionRth: string;  // usually transparent
  sessionPost: string;
  sessionClosed: string;
  // indicator default colors
  indVwap: string;
  indEma: string;
  indSma: string;
  indMacdLine: string;
  indMacdSignal: string;
  indMacdHist: string;
  // link-group swatches
  linkRed: string;
  linkGreen: string;
  linkBlue: string;
  linkYellow: string;
  // status
  accent: string;
  ok: string;
  warn: string;
  danger: string;
}

export const LIGHT: Palette = {
  bg: "#FBFAF7", surface: "#F2F0EA", border: "#DDD9CF", borderStrong: "#C9C4B8",
  text: "#171A1E", textMuted: "#6A7280",
  grid: "#E7E3DA", crosshair: "#B8B2A4",
  up: "#177A58", down: "#C2334D",
  volUp: "rgba(23,122,88,0.34)", volDown: "rgba(194,51,77,0.30)",
  buyFill: "#0E5A3F", sellFill: "#931E37", shortFill: "#1565C0", fillOutline: "#FBFAF7",
  neutral: "#6A7280",
  depthBid: "rgba(23,122,88,0.13)", depthAsk: "rgba(194,51,77,0.11)",
  flashBuy: "rgba(23,122,88,0.20)", flashSell: "rgba(194,51,77,0.20)", flashNeutral: "rgba(106,114,128,0.16)",
  orderMark: "#9A6A1B",
  sessionPre: "rgba(154,106,27,0.05)", sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(106,114,128,0.06)", sessionClosed: "rgba(106,114,128,0.10)",
  indVwap: "#9A6A1B", indEma: "#3E7CB1", indSma: "#7A5CA6",
  indMacdLine: "#3E7CB1", indMacdSignal: "#C2334D", indMacdHist: "rgba(106,114,128,0.5)",
  linkRed: "#DB4C56", linkGreen: "#1FA97F", linkBlue: "#3E7CB1", linkYellow: "#CF9A2B",
  accent: "#9A6A1B", ok: "#177A58", warn: "#9A6A1B", danger: "#A81E30",
};

export const DARK: Palette = {
  bg: "#14120E", surface: "#1C1A15", border: "#2E2A22", borderStrong: "#403A2E",
  text: "#ECE7DB", textMuted: "#9A9385",
  grid: "#241F18", crosshair: "#5A5347",
  up: "#35B888", down: "#E5637A",
  volUp: "rgba(53,184,136,0.34)", volDown: "rgba(229,99,122,0.30)",
  buyFill: "#1B8A61", sellFill: "#C23E56", shortFill: "#42A5F5", fillOutline: "#14120E",
  neutral: "#9A9385",
  depthBid: "rgba(53,184,136,0.16)", depthAsk: "rgba(229,99,122,0.14)",
  flashBuy: "rgba(53,184,136,0.24)", flashSell: "rgba(229,99,122,0.24)", flashNeutral: "rgba(154,147,133,0.18)",
  orderMark: "#C79A4B",
  sessionPre: "rgba(199,154,75,0.07)", sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(154,147,133,0.07)", sessionClosed: "rgba(154,147,133,0.12)",
  indVwap: "#C79A4B", indEma: "#6BA6D8", indSma: "#A98BD0",
  indMacdLine: "#6BA6D8", indMacdSignal: "#E5637A", indMacdHist: "rgba(154,147,133,0.5)",
  linkRed: "#E5636D", linkGreen: "#35B88F", linkBlue: "#6BA6D8", linkYellow: "#D9AE52",
  accent: "#C79A4B", ok: "#35B888", warn: "#C79A4B", danger: "#E5455E",
};

export function getPalette(mode: ThemeMode): Palette {
  return mode === "dark" ? DARK : LIGHT;
}

// Type layer — part of the visual identity, kept in the single source of truth.
// Canvas painters put FONTS.mono in their ctx.font strings; chrome uses the CSS
// vars set from these in Task 10. All three faces are the IBM Plex super-family.
export const FONTS = {
  serif: '"IBM Plex Serif", Georgia, serif',       // panel titles, section labels, wordmark
  mono: '"IBM Plex Mono", ui-monospace, monospace', // data surfaces: tape, ladder, prices, axes
  sans: '"IBM Plex Sans", system-ui, sans-serif',   // chrome: labels, menus, buttons
} as const;
