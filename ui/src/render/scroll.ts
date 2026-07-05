/** Result of scrollAccumulate: whole rows to scroll now plus the sub-row remainder to carry forward. */
export interface ScrollDelta {
  rows: number;
  remainder: number;
}

/**
 * Port of wickplot's CandlestickChartMath.accumulatePan, re-signed for row
 * scrolling. Wheel events arrive a few pixels at a time; rounding each event
 * to rows independently discards movement smaller than a row, which makes slow
 * scrolls do nothing. Feed the previous remainder back in on every event so
 * slow movement accumulates. Positive deltaPx yields positive rows — direction
 * semantics belong to the caller (the original's drag-inversion is dropped).
 */
export function scrollAccumulate(remainder: number, deltaPx: number, rowPx: number): ScrollDelta {
  if (rowPx <= 0) return { rows: 0, remainder };
  const total = remainder + deltaPx / rowPx;
  const rows = Math.trunc(total); // truncate toward zero; the fraction is carried forward
  return { rows, remainder: total - rows };
}
