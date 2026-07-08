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

/** Integer share sizes with thousands separators: 12345 → "12,345". */
export function formatSize(size: number): string {
  return Math.round(size).toLocaleString("en-US");
}

/** Exchange timestamp (ISO string) → ET wall-clock HH:MM:SS for tape rows. */
export function formatTapeTime(ts: string): string {
  return new Date(ts).toLocaleTimeString("en-US", { hour12: false, timeZone: "America/New_York" });
}
