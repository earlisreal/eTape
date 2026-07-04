// Pure mirror of the engine's session-anchored bar bucketing. Intraday buckets
// are anchored to 09:30 ET (TradingView-style), NOT to midnight. Used to validate
// fixtures and to let the chart controller reason about in-progress vs new buckets
// without depending on message arrival order. ET conversion via Intl (DST-correct).
export type Timeframe = "10s" | "1m" | "5m" | "15m" | "30m" | "60m" | "D" | "W" | "M";

const ET = new Intl.DateTimeFormat("en-US", {
  timeZone: "America/New_York", hour12: false,
  year: "numeric", month: "numeric", day: "numeric",
  hour: "2-digit", minute: "2-digit", second: "2-digit", weekday: "short",
});
const WDAY: Record<string, number> = { Sun: 0, Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6 };

export interface EtParts { y: number; mo: number; d: number; h: number; mi: number; s: number; wday: number }

export function etParts(tsMs: number): EtParts {
  const parts = ET.formatToParts(new Date(tsMs));
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "0";
  let h = Number(get("hour"));
  if (h === 24) h = 0; // hour12:false can yield "24" at midnight in some engines
  return {
    y: Number(get("year")), mo: Number(get("month")), d: Number(get("day")),
    h, mi: Number(get("minute")), s: Number(get("second")),
    wday: WDAY[get("weekday")] ?? 0,
  };
}

// ET midnight (00:00 ET) for the ET calendar day containing tsMs, as an epoch ms.
function etMidnightMs(tsMs: number): number {
  const p = etParts(tsMs);
  const secsSinceEtMidnight = p.h * 3600 + p.mi * 60 + p.s;
  return tsMs - secsSinceEtMidnight * 1000 - (new Date(tsMs).getUTCMilliseconds());
}

// Finds the epoch ms for 00:00 ET on a specific ET calendar date (targetY/targetMo/targetD),
// starting from a nearby ET-midnight guess. Steps day-by-day, re-snapping to the true ET
// midnight of the landed-on day each time — this self-corrects for the 23h/25h DST-transition
// day instead of assuming every day is a fixed 86,400,000ms (which drifts month bucket starts
// by an hour whenever the walked range crosses a DST transition).
function etMidnightForDate(guessMs: number, targetY: number, targetMo: number, targetD: number): number {
  let ms = etMidnightMs(guessMs);
  for (let i = 0; i < 40; i++) {
    const p = etParts(ms);
    if (p.y === targetY && p.mo === targetMo && p.d === targetD) return ms;
    const cur = Date.UTC(p.y, p.mo - 1, p.d);       // calendar-date comparison only, not a real instant
    const target = Date.UTC(targetY, targetMo - 1, targetD);
    ms = etMidnightMs(ms + (cur < target ? 1 : -1) * 86400 * 1000);
  }
  throw new Error("etMidnightForDate: failed to converge");
}

const ANCHOR_SECS = 9 * 3600 + 30 * 60; // 09:30 ET session anchor

export function bucketStartMs(tsMs: number, tf: Timeframe): number {
  const midnight = etMidnightMs(tsMs);
  const secsIntoDay = Math.floor((tsMs - midnight) / 1000);

  const floorTo = (spanSecs: number, anchorSecs: number): number => {
    const rel = secsIntoDay - anchorSecs;
    const bucketRel = Math.floor(rel / spanSecs) * spanSecs;
    return midnight + (anchorSecs + bucketRel) * 1000;
  };

  switch (tf) {
    case "10s": return floorTo(10, 0);        // aligned to the minute grid
    case "1m":  return floorTo(60, 0);
    case "5m":  return floorTo(5 * 60, ANCHOR_SECS);
    case "15m": return floorTo(15 * 60, ANCHOR_SECS);
    case "30m": return floorTo(30 * 60, ANCHOR_SECS);
    case "60m": return floorTo(60 * 60, ANCHOR_SECS);
    case "D":   return midnight;
    case "W": {
      // Week starts Monday 00:00 ET. Resolve via etMidnightForDate (not a fixed
      // day-count subtraction) so this stays correct across a DST transition;
      // in practice the US transition always falls on a non-trading Sunday so
      // no real week's span crosses it, but this keeps the logic uniform with "M".
      const p = etParts(tsMs);
      const daysFromMonday = (p.wday + 6) % 7;
      if (daysFromMonday === 0) return midnight;
      const target = new Date(Date.UTC(p.y, p.mo - 1, p.d - daysFromMonday));
      return etMidnightForDate(midnight, target.getUTCFullYear(), target.getUTCMonth() + 1, target.getUTCDate());
    }
    case "M": {
      const p = etParts(tsMs);
      // First of the ET month at 00:00 ET, resolved by day-snapping (not a fixed
      // day-count subtraction) so it doesn't drift by an hour across the March/
      // November DST transition.
      return etMidnightForDate(midnight, p.y, p.mo, 1);
    }
  }
}
