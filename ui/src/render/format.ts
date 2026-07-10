// Shared numeric formatting for the canvas data surfaces (ladder + tape here;
// the Plan-5 order ticket reuses priceDecimals/formatPrice). axisDecimals is a
// direct port of wickplot's CandlestickChartMath.axisDecimals.

/** Fractional digits needed to print step exactly: 0.05 → 2, 0.5 → 1, 2.5 → 1, 1 → 0, 10 → 0. */
export function axisDecimals(step: number): number {
  let d = 0;
  let s = step;
  while (d < 8 && Math.abs(s - Math.round(s)) > 1e-9) {
    s *= 10;
    d++;
  }
  return d;
}

/**
 * Uniform decimal count for a column of prices (a book's levels, a tape
 * window): enough digits that every price prints exactly, floored at the US
 * equity convention of 2 and capped at the sub-penny tick limit of 4.
 */
export function priceDecimals(prices: number[]): number {
  let d = 2;
  for (const p of prices) d = Math.max(d, axisDecimals(p));
  return Math.min(d, 4);
}

export function formatPrice(price: number, decimals: number): string {
  return price.toFixed(decimals);
}

/**
 * Fixed decimal count for live quote/limit-price display (ladder bid/ask, order
 * ticket, tape prints): a value-derived count (priceDecimals) flickers as ticks
 * cross a sub-penny boundary, so these surfaces pin to 3 instead.
 */
export const QUOTE_DECIMALS = 3;

// Module-level Intl formatter singletons. These surfaces (ladder, tape) call
// their formatXxx helper on every visible row on every paint -- up to a few
// hundred calls/sec during active trading -- and constructing an
// Intl.NumberFormat/DateTimeFormat is far costlier than calling an existing
// one's .format(): each per-call toLocaleString/toLocaleTimeString was
// silently building (and discarding) a fresh formatter every time. Caching
// them once here made no measurable difference on macOS (fast ICU) but was a
// real, refresh-rate-independent per-paint bottleneck on Windows/Chrome,
// where it showed up as ladder/tape lag even after lowering the monitor's
// refresh rate. Options must exactly match the toLocaleString*/toLocaleTimeString
// calls they replace so output is byte-identical.
const SIZE_FMT = new Intl.NumberFormat("en-US");
// toLocaleTimeString implicitly defaults to hour/minute/second fields; a bare
// Intl.DateTimeFormat with no date-or-time fields specified defaults to a
// date-only format instead (verified: {hour12:false, timeZone} alone formats
// as "7/6/2026", not a time) -- so hour/minute/second must be spelled out
// explicitly to reproduce toLocaleTimeString's output byte-for-byte.
const ET_TIME_FMT = new Intl.DateTimeFormat("en-US", {
  hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZone: "America/New_York",
});

/** Integer share sizes with thousands separators: 12345 → "12,345". */
export function formatSize(size: number): string {
  return SIZE_FMT.format(Math.round(size));
}

// date.toLocaleTimeString() on an invalid Date returns the string "Invalid
// Date" -- it never throws. Intl.DateTimeFormat.prototype.format on the same
// invalid Date instead throws RangeError("Invalid time value") (confirmed:
// this surfaced as a real crash -- StockInfoPanel renders a news item with an
// occasionally malformed/missing publish timestamp, which used to degrade to
// literal "Invalid Date" text and now crashed the panel). formatTime
// preserves the old, non-throwing fallback for both callers below.
function formatTime(d: Date): string {
  return Number.isNaN(d.getTime()) ? "Invalid Date" : ET_TIME_FMT.format(d);
}

/** Exchange timestamp (ISO string) → ET wall-clock HH:MM:SS for tape rows. */
export function formatTapeTime(ts: string): string {
  return formatTime(new Date(ts));
}

/** Epoch-ms timestamp → ET wall-clock HH:MM:SS, for exec surfaces whose timestamps are
 * numbers (unlike formatTapeTime's ISO-string tape timestamps). */
export function formatClock(ms: number): string {
  return formatTime(new Date(ms));
}

/** Duration in ms → a compact human string: hours+minutes once >= 1h ("1h 04m"),
 * minutes+seconds below that ("03m 12s"), zero-padded. */
export function formatDuration(ms: number): string {
  const totalSec = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(totalSec / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const s = totalSec % 60;
  const pad2 = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}h ${pad2(m)}m` : `${pad2(m)}m ${pad2(s)}s`;
}
