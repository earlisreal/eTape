import { bucketStartMs, type Timeframe } from "./barBucket";
import { timeframeToMs } from "./drawings/geometry";

// Countdown target: ms until the current bar closes (wall-clock time, not tick arrival).
// Bar close instant = bucketStartMs(now, tf) + timeframeToMs(tf); this returns close - now.
export function remainingToBarCloseMs(tf: Timeframe, nowMs: number): number {
  const bucketStart = bucketStartMs(nowMs, tf);
  const timeframeMs = timeframeToMs(tf);
  return bucketStart + timeframeMs - nowMs;
}

// Gates the bar-close countdown to intraday timeframes. Daily/weekly/monthly bar
// close is fuzzy (depends on session hours), so the countdown UI only shows on intraday.
export function isIntradayTimeframe(tf: Timeframe): boolean {
  return tf !== "D" && tf !== "W" && tf !== "M";
}

// Format countdown: clamped to 0 (no negatives), "mm:ss" under 1 hour, "h:mm:ss" at or above.
export function formatCountdown(ms: number): string {
  const clamped = Math.max(0, ms);
  const totalSeconds = Math.floor(clamped / 1000);

  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  if (hours > 0) {
    return `${hours}:${minutes.toString().padStart(2, "0")}:${seconds.toString().padStart(2, "0")}`;
  } else {
    return `${minutes}:${seconds.toString().padStart(2, "0")}`;
  }
}
