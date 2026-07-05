// Chrome-layer formatters for the monitoring tables (scanner/movers/news).
// The canvas surfaces format via render/format.ts; these are the React-table
// equivalents: 3-digit-safe %, compact float/volume, and ET-midnight math for
// the scanner's dedup reset. Pure and DOM-free.

/** Signed, one-decimal, 3-digit-safe percent: 4.23 → "+4.2%", -12.35 → "−12.3%",
 *  234.5 → "+234.5%". null / NaN (no print yet) → "—" (never a fabricated 0%). */
export function formatChangePct(pct: number | null): string {
  if (pct === null || Number.isNaN(pct)) return "—";
  const sign = pct > 0 ? "+" : pct < 0 ? "−" : ""; // U+2212 for negatives
  return `${sign}${Math.abs(pct).toFixed(1)}%`;
}

/** Compact share/volume count: 2_100_000 → "2.1M", 950_000 → "950K",
 *  3_200_000_000 → "3.2B", 640 → "640". null (unknown) → "—"; 0 → "0". */
export function formatCompactShares(n: number | null): string {
  if (n === null || Number.isNaN(n)) return "—";
  const abs = Math.abs(n);
  if (abs >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (abs >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (abs >= 1e3) return `${(n / 1e3).toFixed(0)}K`;
  return `${Math.round(n)}`;
}

/** Milliseconds from `now` until the next 00:00 America/New_York — the dedup
 *  reset boundary. Uses Intl so it tracks EST/EDT automatically. */
export function msUntilEtMidnight(now: Date): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: "America/New_York", hour12: false,
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  }).formatToParts(now);
  const get = (t: string) => Number(parts.find((p) => p.type === t)?.value);
  let h = get("hour");
  if (h === 24) h = 0; // Intl can emit "24" at midnight
  const sinceMidnightMs = ((h * 60 + get("minute")) * 60 + get("second")) * 1000 + now.getMilliseconds();
  const dayMs = 24 * 60 * 60 * 1000;
  const rem = dayMs - sinceMidnightMs;
  return rem <= 0 ? dayMs : rem;
}
