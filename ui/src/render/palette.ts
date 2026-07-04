// The single color source of truth for eTape. The LWC chart theme, every custom
// painter/primitive, and all chrome derive from this. Painters receive a Palette
// in their paint state — never read one from a global. Light is the app default.
export type ThemeMode = "light" | "dark";

export interface Palette {
  // surfaces / structure
  bg: string;          // panel + chart background
  surface: string;     // headers, controls
  border: string;      // hairlines
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
  buyFill: string;     // buy diamond fill (soft green)
  sellFill: string;    // sell diamond fill (soft pink)
  fillOutline: string; // thin dark outline on both
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
  bg: "#FBFCFD",
  surface: "#EEF1F4",
  border: "#DCE1E7",
  text: "#10151C",
  textMuted: "#5A6672",
  grid: "#E8ECF0",
  crosshair: "#9AA6B2",
  up: "#17A67C",
  down: "#E0526E",
  volUp: "rgba(23,166,124,0.38)",
  volDown: "rgba(224,82,110,0.38)",
  buyFill: "#4CC79E",
  sellFill: "#F58DA1",
  fillOutline: "#10151C",
  sessionPre: "rgba(92,120,160,0.07)",
  sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(198,150,64,0.08)",
  sessionClosed: "rgba(40,50,65,0.05)",
  indVwap: "#6E56CF",
  indEma: "#C0872E",
  indSma: "#3E7CB1",
  indMacdLine: "#3E7CB1",
  indMacdSignal: "#E0526E",
  indMacdHist: "#8A97A6",
  linkRed: "#DB4C56",
  linkGreen: "#1FA97F",
  linkBlue: "#3E7CB1",
  linkYellow: "#CF9A2B",
  accent: "#C0872E",
  ok: "#17A67C",
  warn: "#C0872E",
  danger: "#D93A49",
};

export const DARK: Palette = {
  bg: "#0E1116",
  surface: "#161B22",
  border: "#262D38",
  text: "#DCE3EC",
  textMuted: "#7A8794",
  grid: "#1C222C",
  crosshair: "#55616F",
  up: "#2BB894",
  down: "#F0647E",
  volUp: "rgba(43,184,148,0.34)",
  volDown: "rgba(240,100,126,0.34)",
  buyFill: "#35C79E",
  sellFill: "#F98AA3",
  fillOutline: "#05070A",
  sessionPre: "rgba(120,150,190,0.12)",
  sessionRth: "rgba(0,0,0,0)",
  sessionPost: "rgba(200,155,70,0.10)",
  sessionClosed: "rgba(255,255,255,0.03)",
  indVwap: "#9A86FF",
  indEma: "#E0A64B",
  indSma: "#6BA8D8",
  indMacdLine: "#6BA8D8",
  indMacdSignal: "#F0647E",
  indMacdHist: "#55616F",
  linkRed: "#F0555F",
  linkGreen: "#2BB894",
  linkBlue: "#5AA0D8",
  linkYellow: "#E0B23E",
  accent: "#E0A64B",
  ok: "#2BB894",
  warn: "#E0A64B",
  danger: "#F0555F",
};

export function getPalette(mode: ThemeMode): Palette {
  return mode === "dark" ? DARK : LIGHT;
}

// Type layer — part of the visual identity, kept in the single source of truth.
// Canvas painters put FONTS.mono in their ctx.font strings; chrome uses the CSS
// vars set from these in Task 10. Both faces are the IBM Plex super-family.
export const FONTS = {
  mono: '"IBM Plex Mono", ui-monospace, monospace', // data surfaces: tape, ladder, prices, axes
  sans: '"IBM Plex Sans", system-ui, sans-serif',   // chrome: labels, menus, buttons
} as const;
