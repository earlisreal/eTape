import { bucketStartMs, TIMEFRAMES, type Timeframe } from "./barBucket";

export function mapCrosshairBar(
  sourceTimeMs: number,
  sourceTimeframe: Timeframe,
  destinationBars: readonly { bucketStart: string; dataGap?: boolean }[],
  destinationTimeframe: Timeframe,
): number | null {
  if (!Number.isFinite(sourceTimeMs)) return null;
  const sourcePeriod = bucketStartMs(sourceTimeMs, sourceTimeframe);
  const sourceOrder = TIMEFRAMES.indexOf(sourceTimeframe);
  const destinationOrder = TIMEFRAMES.indexOf(destinationTimeframe);
  const containsSource = sourceOrder <= destinationOrder;
  const soughtTime = containsSource ? bucketStartMs(sourceTimeMs, destinationTimeframe) : sourcePeriod;

  let low = 0;
  let high = destinationBars.length;
  while (low < high) {
    const mid = (low + high) >>> 1;
    if (Date.parse(destinationBars[mid].bucketStart) < soughtTime) low = mid + 1;
    else high = mid;
  }

  if (containsSource) {
    return low < destinationBars.length && Date.parse(destinationBars[low].bucketStart) === soughtTime
      && !destinationBars[low].dataGap ? low : null;
  }

  for (let i = low; i < destinationBars.length; i++) {
    const timeMs = Date.parse(destinationBars[i].bucketStart);
    if (bucketStartMs(timeMs, sourceTimeframe) !== sourcePeriod) return null;
    if (!destinationBars[i].dataGap) return i;
  }
  return null;
}
