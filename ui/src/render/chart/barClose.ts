import { bucketStartMs, etParts, type EtParts, type Timeframe } from "./barBucket";
import { timeframeToMs } from "./drawings/geometry";
import { sessionAt } from "./sessions";
import type { Bar } from "../../wire/contract";

export const TEN_SECOND_TIMER_EARLY_ROLLOVER_GRACE_MS = 3_000;

// Exchange-timestamped bars can enter the next bucket slightly before the
// local browser clock reaches it, so allow only that immediately-next bucket
// within a small wall-clock grace period.
export function isTenSecondTimerCandidateLive(candidateBucketStart: string, nowMs: number): boolean {
  const candidateMs = Date.parse(candidateBucketStart);
  if (!Number.isFinite(candidateMs)) return false;

  const currentBucketMs = bucketStartMs(nowMs, "10s");
  if (candidateMs === currentBucketMs) return true;

  const nextBucketMs = currentBucketMs + timeframeToMs("10s");
  return candidateMs === nextBucketMs && candidateMs > nowMs
    && candidateMs - nowMs <= TEN_SECOND_TIMER_EARLY_ROLLOVER_GRACE_MS;
}

// Pick the newest raw exchange bar that can anchor the countdown. A quiet
// bucket must not erase the badge: the last confirmed price remains usable for
// the active ET trading day, while malformed, closed-session, stale-day, and
// excessively-future bars are ignored.
export function latestEligibleCountdownBar(bars: readonly Bar[], tf: Timeframe, nowMs: number): Bar | null {
  if (!isIntradayTimeframe(tf) || !Number.isFinite(nowMs) || sessionAt(nowMs) === "closed") return null;
  const nowDay = etParts(nowMs);
  const currentBucketMs = tf === "10s" ? bucketStartMs(nowMs, "10s") : 0;
  const dayKey = (day: EtParts) => `${day.y}-${day.mo.toString().padStart(2, "0")}-${day.d.toString().padStart(2, "0")}`;
  const nowDayKey = dayKey(nowDay);
  // BarStore keeps raw bars chronologically ordered, so the first eligible
  // bar from the tail is the newest one; far-future entries are skipped until
  // the previous eligible price is found.
  for (let i = bars.length - 1; i >= 0; i--) {
    const bar = bars[i];
    const barMs = Date.parse(bar.bucketStart);
    if (!Number.isFinite(barMs) || !Number.isFinite(bar.o) || !Number.isFinite(bar.c)) continue;
    const barDay = etParts(barMs);
    const barDayKey = dayKey(barDay);
    if (barDayKey < nowDayKey) break;
    if (barDayKey > nowDayKey || sessionAt(barMs) === "closed") continue;
    const future = tf === "10s"
      ? barMs > currentBucketMs && !isTenSecondTimerCandidateLive(bar.bucketStart, nowMs)
      : barMs > nowMs;
    if (!future) return bar;
  }
  return null;
}

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
