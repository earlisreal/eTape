import type { ScannerFilters } from "../wire/contract";

export const DEFAULT_SCANNER_FILTERS: ScannerFilters = {
  mode: "gainers",
  minChangePct: 0,
  maxFloatShares: null,
  minVolume: 0,
  minSessionVolume: 0,
  minTurnover: 0,
  minRelativeVolume: 0,
  minPrice: 0,
  maxPrice: 0,
  floatUnit: "M",
  volumeUnit: "K",
  sessionVolumeUnit: "K",
};

export function scannerFiltersFromSettings(settings: Record<string, unknown>): ScannerFilters {
  const raw = settings.scannerFilters as Partial<ScannerFilters> | undefined;
  const mode = raw?.mode;
  return {
    ...DEFAULT_SCANNER_FILTERS,
    ...raw,
    mode: mode === "gainers" || mode === "losers" || mode === "most_active" || mode === "session_volume" ? mode : "gainers",
    floatUnit: raw?.floatUnit === "K" || raw?.floatUnit === "M" ? raw.floatUnit : "M",
    volumeUnit: raw?.volumeUnit === "K" || raw?.volumeUnit === "M" ? raw.volumeUnit : "K",
    sessionVolumeUnit: raw?.sessionVolumeUnit === "K" || raw?.sessionVolumeUnit === "M" ? raw.sessionVolumeUnit : "K",
  };
}
