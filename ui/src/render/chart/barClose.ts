import { bucketStartMs, type Timeframe } from "./barBucket";
import { timeframeToMs } from "./drawings/geometry";

export function remainingToBarCloseMs(tf: Timeframe, nowMs: number): number {
  const bucketStart = bucketStartMs(nowMs, tf);
  const timeframeMs = timeframeToMs(tf);
  return bucketStart + timeframeMs - nowMs;
}

export function isIntradayTimeframe(tf: Timeframe): boolean {
  return tf !== "D" && tf !== "W" && tf !== "M";
}

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
