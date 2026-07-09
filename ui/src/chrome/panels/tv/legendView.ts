// ui/src/chrome/panels/tv/legendView.ts
import type { Bar } from "../../../wire/contract";
import type { IndicatorReader } from "../../../render/chart/ChartController";
import type { Palette } from "../../../render/palette";
import { INDICATOR_CATALOG, withDefaultParams, describeIndicator, type IndicatorInstance } from "../../../render/chart/indicatorSeries";

export interface LegendIndicatorRow {
  instanceId: string; label: string; paneIndex: number; values: (number | null)[]; colors: string[];
  // MACD only: "open" when the fast (macd) line is at/above the slow (signal)
  // line at this bar, "close" when below, null when either value is missing
  // or the indicator isn't MACD.
  signal?: "open" | "close" | null;
}
export interface LegendView {
  o: number | null; h: number | null; l: number | null; c: number | null; changePct: number | null; up: boolean;
  volume: number | null; indicators: LegendIndicatorRow[];
}

function valueAt(points: { timeMs: number; value: number }[], ms: number): number | null {
  let val: number | null = null;
  for (const p of points) { if (p.timeMs <= ms) val = p.value; else break; }
  return val;
}

function labelOf(inst: IndicatorInstance): string {
  const entry = INDICATOR_CATALOG[inst.type];
  const params = withDefaultParams(inst.type, inst.params);
  const nums = entry.params.map((sp) => params[sp.key]).join(" ");
  const src = inst.type === "EMA" || inst.type === "SMA" ? " close" : "";
  return nums ? `${entry.label} ${nums}${src}` : entry.label;
}

export function computeLegendView(
  bars: readonly Bar[], reader: IndicatorReader, instances: IndicatorInstance[], palette: Palette, logical: number | null,
): LegendView {
  const has = bars.length > 0;
  const i = !has ? -1 : logical === null ? bars.length - 1 : Math.max(0, Math.min(bars.length - 1, Math.round(logical)));
  const b = has ? bars[i] : null;
  const prev = has && i > 0 ? bars[i - 1] : null;
  const barMs = b ? Date.parse(b.bucketStart) : NaN;
  const changePct = b && prev && prev.c !== 0 ? ((b.c - prev.c) / prev.c) * 100 : null;
  const indicators: LegendIndicatorRow[] = instances.map((inst) => {
    const descs = describeIndicator(inst, palette);
    const values = descs.map((d) => (b ? valueAt(reader.series(d.key), barMs) : null));
    // Slot order is fixed by INDICATOR_CATALOG.MACD: [0]=macd (fast), [1]=signal (slow).
    const [fast, slow] = values;
    const signal: "open" | "close" | null =
      inst.type === "MACD" && fast !== null && slow !== null ? (fast >= slow ? "open" : "close") : null;
    return {
      instanceId: inst.instanceId,
      label: labelOf(inst),
      paneIndex: INDICATOR_CATALOG[inst.type].slots[0].paneIndex,
      values,
      colors: descs.map((d) => d.color),
      signal,
    };
  });
  return { o: b?.o ?? null, h: b?.h ?? null, l: b?.l ?? null, c: b?.c ?? null, changePct, up: b ? b.c >= b.o : true, volume: b?.v ?? null, indicators };
}
