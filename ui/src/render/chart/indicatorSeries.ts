import type { Palette } from "../palette";
import type { LineStyleName } from "./lineStyle";
import { INDICATOR_LINE_WIDTH } from "./chartTheme";

export type IndicatorType = "VWAP" | "EMA" | "SMA" | "MACD" | "VOLUME";

// A per-chart indicator instance. `params` and `colors` are the customizable state,
// persisted with the workspace (Task 9). `colors` is keyed by slot; unset slots use
// the palette default (so they re-theme automatically on light/dark switch).
export interface IndicatorInstance {
  instanceId: string;
  type: IndicatorType;
  params: Record<string, number>;
  colors?: Record<string, string>;
  styles?: Record<string, SlotStyle>; // per-slot style overrides (color/width/lineStyle)
  hidden?: boolean;                    // legend 👁 toggle — mapped to LWC series `visible`
}

export interface SlotStyle { color?: string; width?: number; lineStyle?: LineStyleName }

export interface ParamSpec { key: string; label: string; default: number; min: number; max: number }
export interface SlotSpec { slot: string; kind: "line" | "histogram"; paneIndex: number; paletteKey: keyof Palette }
export interface CatalogEntry { type: IndicatorType; label: string; params: ParamSpec[]; slots: SlotSpec[] }

export interface SeriesDescriptor {
  key: string;         // unique LWC series id: instanceId (single-slot) or `${instanceId}#${slot}`
  slot: string;        // stable slot name — the persistable color key
  kind: "line" | "histogram";
  paneIndex: number;   // 0 = main pane, 1 = MACD sub-pane
  color: string;       // resolved: inst.colors?.[slot] ?? palette[slot's default key]
  width: number;           // resolved: styles[slot].width ?? INDICATOR_LINE_WIDTH
  lineStyle: LineStyleName; // resolved: styles[slot].lineStyle ?? "solid"
}

const MAIN = 0, SUBPANE = 1;

// The v1 indicator catalog: every type's editable params (defaults + bounds) and
// drawable slots (with the palette key each defaults to). The management UI (Task 9)
// renders inputs from `params` and color pickers from `slots`.
export const INDICATOR_CATALOG: Record<IndicatorType, CatalogEntry> = {
  VWAP:   { type: "VWAP",   label: "VWAP",       params: [], slots: [{ slot: "line", kind: "line", paneIndex: MAIN, paletteKey: "indVwap" }] },
  EMA:    { type: "EMA",    label: "EMA",        params: [{ key: "period", label: "Period", default: 9,  min: 1, max: 400 }], slots: [{ slot: "line", kind: "line", paneIndex: MAIN, paletteKey: "indEma" }] },
  SMA:    { type: "SMA",    label: "SMA",        params: [{ key: "period", label: "Period", default: 20, min: 1, max: 400 }], slots: [{ slot: "line", kind: "line", paneIndex: MAIN, paletteKey: "indSma" }] },
  VOLUME: { type: "VOLUME", label: "Volume",     params: [], slots: [{ slot: "hist", kind: "histogram", paneIndex: MAIN, paletteKey: "indMacdHist" }] },
  MACD:   { type: "MACD",   label: "MACD",
            params: [
              { key: "fast",   label: "Fast",   default: 12, min: 1, max: 200 },
              { key: "slow",   label: "Slow",   default: 26, min: 1, max: 400 },
              { key: "signal", label: "Signal", default: 9,  min: 1, max: 200 },
            ],
            slots: [
              { slot: "macd",   kind: "line",      paneIndex: SUBPANE, paletteKey: "indMacdLine" },
              { slot: "signal", kind: "line",      paneIndex: SUBPANE, paletteKey: "indMacdSignal" },
              { slot: "hist",   kind: "histogram", paneIndex: SUBPANE, paletteKey: "indMacdHist" },
            ] },
};

// Fill any params the user hasn't set with the catalog defaults.
export function withDefaultParams(type: IndicatorType, params: Record<string, number> = {}): Record<string, number> {
  const out = { ...params };
  for (const p of INDICATOR_CATALOG[type].params) if (out[p.key] === undefined) out[p.key] = p.default;
  return out;
}

export function describeIndicator(inst: IndicatorInstance, p: Palette): SeriesDescriptor[] {
  const entry = INDICATOR_CATALOG[inst.type];
  const single = entry.slots.length === 1;
  return entry.slots.map((s) => {
    const style = inst.styles?.[s.slot];
    return {
      key: single ? inst.instanceId : `${inst.instanceId}#${s.slot}`,
      slot: s.slot,
      kind: s.kind,
      paneIndex: s.paneIndex,
      color: style?.color ?? inst.colors?.[s.slot] ?? p[s.paletteKey],
      width: style?.width ?? INDICATOR_LINE_WIDTH,
      lineStyle: style?.lineStyle ?? "solid",
    };
  });
}
