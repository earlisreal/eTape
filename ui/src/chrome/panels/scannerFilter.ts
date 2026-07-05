import type { ScannerRow } from "../../wire/contract";

export interface ScannerThresholds {
  minChangePct: number;          // magnitude floor on % change (0 = off)
  floatCapShares: number | null; // max float in shares (null = off)
  minVolume: number;             // min session volume (0 = off)
}

/** Client-side filter atop the engine's coarse server filters. A row with no
 *  print yet (null changePct) fails any positive min-%-change floor. A row with
 *  unknown float (null) is never excluded by the float cap. */
export function applyScannerFilters<T extends ScannerRow>(rows: T[], t: ScannerThresholds): T[] {
  return rows.filter((r) => {
    if (r.volume < t.minVolume) return false;
    if (t.floatCapShares !== null && r.floatShares !== null && r.floatShares > t.floatCapShares) return false;
    if (t.minChangePct > 0 && (r.changePct === null || Math.abs(r.changePct) < t.minChangePct)) return false;
    return true;
  });
}

/** Highest % change first; no-print rows (null) sort last. Pure (copies input). */
export function sortByChangeDesc<T extends ScannerRow>(rows: T[]): T[] {
  return [...rows].sort((a, b) => (b.changePct ?? -Infinity) - (a.changePct ?? -Infinity));
}
