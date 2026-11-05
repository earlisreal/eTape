import type { ScreenerFilters } from "../wire/contract";

export const DEFAULT_SCREENER_FILTERS: ScreenerFilters = {
  lookback: "all-time", tradingDays: 126, mode: "either", nearPct: 5, requireClose: true,
  minPrice: 0, maxPrice: 0, minDollarVolume: 0, minMarketCap: 0, maxMarketCap: 0,
  aboveSma20: false, aboveSma50: false, aboveSma200: false, extensionSma: 0, maxExtensionPct: 0,
  minAtrPct: 0, maxAtrPct: 0,
};

export function readScreenerFilters(value: unknown): ScreenerFilters {
  const filters = { ...DEFAULT_SCREENER_FILTERS };
  if (!value || typeof value !== "object" || Array.isArray(value)) return filters;
  const raw = value as Record<string, unknown>;
  for (const key of [...Object.keys(filters), "minChangePct", "maxChangePct"] as (keyof ScreenerFilters)[]) {
    const candidate = raw[key];
    if ((typeof candidate === typeof filters[key] && (typeof candidate !== "number" || Number.isFinite(candidate)))
      || ((key === "minChangePct" || key === "maxChangePct") && (candidate === null || (typeof candidate === "number" && Number.isFinite(candidate))))) {
      if (candidate !== null && candidate !== undefined) Object.assign(filters, { [key]: candidate });
    }
  }
  if (!["all-time", "6-month", "1-year", "trading-days"].includes(filters.lookback)) filters.lookback = "all-time";
  if (!["near", "new", "either"].includes(filters.mode)) filters.mode = "either";
  return filters;
}

export interface ScreenerPreset { name: string; filters: ScreenerFilters }
export function readScreenerPresets(value: unknown): ScreenerPreset[] {
  if (!Array.isArray(value)) return [];
  const presets = new Map<string, ScreenerPreset>();
  for (const item of value) {
    if (!item || typeof item !== "object" || typeof item.name !== "string") continue;
    const name = item.name.trim().slice(0, 60);
    if (name) presets.set(name, { name, filters: readScreenerFilters(item.filters) });
    if (presets.size >= 20) break;
  }
  return [...presets.values()];
}
