import type { ScannerRow } from "../../wire/contract";
import { formatDollarTurnover } from "../format";

export interface ScannerThresholds {
  minChangePct: number;          // magnitude floor on % change (0 = off)
  floatCapShares: number | null; // max float in shares (null = off)
  minVolume: number;             // min session volume (0 = off)
  minTurnover: number;           // min session dollar turnover (0 = off)
  minRelativeVolume: number;     // min Relative Volume (Daily Rate) (0 = off)
}

/** Client-side filter atop the engine's coarse server filters. A row with no
 *  print yet (null changePct) fails any positive min-%-change floor. A row with
 *  unknown float (null) is never excluded by the float cap. */
export function applyScannerFilters<T extends ScannerRow>(rows: T[], t: ScannerThresholds): T[] {
  return rows.filter((r) => {
    if (r.volume < t.minVolume) return false;
    if (t.minTurnover > 0 && (r.turnover == null || !Number.isFinite(r.turnover) || r.turnover < t.minTurnover)) return false;
    if (t.minRelativeVolume > 0 && (r.relativeVolume == null || r.relativeVolume < t.minRelativeVolume)) return false;
    if (t.floatCapShares !== null && r.floatShares !== null && r.floatShares > t.floatCapShares) return false;
    if (t.minChangePct > 0 && (r.changePct === null || Math.abs(r.changePct) < t.minChangePct)) return false;
    return true;
  });
}

/** Highest % change first; no-print rows (null) sort last. Pure (copies input). */
export function sortByChangeDesc<T extends ScannerRow>(rows: T[]): T[] {
  return [...rows].sort((a, b) => (b.changePct ?? -Infinity) - (a.changePct ?? -Infinity));
}

const compact = (n: number): string =>
  n >= 1_000_000 ? `${+(n / 1_000_000).toFixed(1)}M` : n >= 1_000 ? `${+(n / 1_000).toFixed(0)}k` : `${n}`;

/** One-line mono summary of active thresholds for the panel header, e.g.
 *  "change ≥ 10% · float ≤ 20M · vol ≥ 100k". Off fields (0 / null) are omitted. */
export function formatFilterSummary(t: ScannerThresholds): string {
  const parts: string[] = [];
  if (t.minChangePct > 0) parts.push(`change magnitude ≥ ${t.minChangePct}%`);
  if (t.floatCapShares !== null) parts.push(`float ≤ ${compact(t.floatCapShares)}`);
  if (t.minVolume > 0) parts.push(`vol ≥ ${compact(t.minVolume)}`);
  if (t.minTurnover > 0) parts.push(`turnover ≥ ${formatDollarTurnover(t.minTurnover)}`);
  if (t.minRelativeVolume > 0) parts.push(`rel vol ≥ ${t.minRelativeVolume}`);
  return parts.length ? parts.join(" · ") : "no filters";
}
