import { etParts } from "./barBucket";

export type Session = "pre" | "rth" | "post" | "closed";
export interface Band { startMs: number; endMs: number; session: Session }

const PRE = 4 * 60, RTH = 9 * 60 + 30, POST = 16 * 60, CLOSE = 20 * 60; // minutes into ET day

export function sessionAt(tsMs: number): Session {
  const p = etParts(tsMs);
  const m = p.h * 60 + p.mi;
  if (p.wday === 0 || p.wday === 6) return "closed"; // weekend (holidays: engine may override later)
  if (m < PRE) return "closed";
  if (m < RTH) return "pre";
  if (m < POST) return "rth";
  if (m < CLOSE) return "post";
  return "closed";
}

// Contiguous session bands covering [fromMs, toMs). Steps at each ET boundary by
// walking forward and snapping to the next transition; bounded, no unbounded loop.
export function sessionBands(fromMs: number, toMs: number): Band[] {
  const bands: Band[] = [];
  let cursor = fromMs;
  let guard = 0;
  while (cursor < toMs && guard++ < 10_000) {
    const session = sessionAt(cursor);
    const next = Math.min(nextBoundaryMs(cursor), toMs);
    bands.push({ startMs: cursor, endMs: next, session });
    cursor = next;
  }
  return bands;
}

// The next ET session-boundary instant strictly after tsMs.
function nextBoundaryMs(tsMs: number): number {
  const p = etParts(tsMs);
  const m = p.h * 60 + p.mi;
  const secOffset = p.s * 1000 + (tsMs % 1000);
  const minutesToBoundary = (() => {
    for (const b of [PRE, RTH, POST, CLOSE, 24 * 60]) if (b > m) return b - m;
    return 24 * 60 - m;
  })();
  // align to the boundary minute exactly
  return tsMs - secOffset + minutesToBoundary * 60_000 - (secOffset > 0 ? 0 : 0);
}

// --- Cheap per-day session classifier --------------------------------------
//
// sessionAt() above calls etParts() — i.e. Intl.DateTimeFormat.formatToParts(),
// one of the most expensive routine JS calls — on every timestamp. That's fine
// for occasional lookups but prohibitively slow when called once per bar across
// a chart's full history (thousands of bars) on every repaint.
//
// The split below amortizes that cost: buildDaySegment(tsMs) calls etParts()
// EXACTLY ONCE to compute one calendar day's session boundaries as absolute
// epoch-ms values. classify(tsMs, seg) is then pure numeric comparison against
// those boundaries — zero Intl calls once a DaySegment exists. A caller (e.g.
// ChartController, in a later task) holds one DaySegment and only calls
// buildDaySegment() again when a new bar's tsMs falls outside
// [seg.dayStartMs, seg.dayEndMs).
//
// DST correctness — dayEndMs is deliberately just `dayStartMs + 24h`, not a
// DST-aware "next real ET midnight" lookup (that would cost a second etParts
// call). This is safe because:
//   1. US DST transitions occur at 02:00 ET on a Sunday (2nd Sunday of March,
//      1st Sunday of November). No trading day (Mon-Fri) ever has its own
//      local-midnight-to-next-local-midnight span cross a transition instant —
//      the transition always falls inside a Sunday's own 24h span (Sunday
//      00:00 ET to Monday 00:00 ET), never inside a weekday's span. So for any
//      WEEKDAY buildDaySegment() call, dayStartMs + 24h lands exactly on the
//      next real ET midnight, and all four {pre,rth,post,close}Ms thresholds
//      are exactly correct.
//   2. The only day whose dayEndMs = dayStartMs + 24h can be imprecise (off by
//      +-1h) is Sunday itself — but classify() returns "closed" unconditionally
//      for a weekend segment BEFORE checking any minute threshold, so an
//      imprecise dayEndMs on Sunday causes no misclassification (Sunday is
//      closed regardless of exactly where within it a boundary falls). At most
//      it causes one extra/early segment rebuild right around the transition —
//      a harmless efficiency nicety, not a correctness bug.
//   3. Each buildDaySegment() call is independent and self-correcting: it
//      always derives dayStartMs/weekend fresh from a single etParts(tsMs) call
//      on the ACTUAL timestamp passed in, never by extrapolating from a
//      previous segment. So even if a previous segment's dayEndMs boundary was
//      imprecise, the moment a caller passes a timestamp outside
//      [dayStartMs, dayEndMs) and rebuilds, the new segment is computed fresh
//      and correct for wherever that timestamp actually falls — there is no
//      cumulative drift.
// Note: on the transition Sunday itself, dayStartMs (not just dayEndMs) can also
// differ by up to 1h depending on which side of the 2am jump the sampled tsMs
// falls on (mirrors etMidnightMs's same-day-subtraction math, which assumes a
// day's offset is uniform). Still harmless for the same reason as point 2 —
// classify() never looks past seg.weekend for either value on a weekend day.
// Verified empirically in sessions.test.ts against the real 2026 US transitions:
// spring-forward Sunday 2026-03-08 (02:00 EST -> 03:00 EDT) and fall-back Sunday
// 2026-11-01 (02:00 EDT -> 01:00 EST).

export interface DaySegment {
  dayStartMs: number; // ET midnight for the calendar day containing the tsMs buildDaySegment was called with
  dayEndMs: number;   // dayStartMs + 24h (see DST note above for why this simple fixed offset is safe)
  weekend: boolean;   // true if this calendar day is Saturday or Sunday
  preMs: number;
  rthMs: number;
  postMs: number;
  closeMs: number;
}

const DAY_MS = 24 * 60 * 60 * 1000;

// Calls etParts() exactly once. Derives ET midnight from that single call's
// {h, mi, s} using the same arithmetic as barBucket.ts's private etMidnightMs
// (subtract seconds-since-ET-midnight, in ms, from tsMs) so it stays consistent
// with the rest of this codebase's ET-conversion logic without a second Intl call.
export function buildDaySegment(tsMs: number): DaySegment {
  const p = etParts(tsMs);
  const secsSinceEtMidnight = p.h * 3600 + p.mi * 60 + p.s;
  const msWithinSecond = tsMs % 1000;
  const dayStartMs = tsMs - secsSinceEtMidnight * 1000 - msWithinSecond;
  return {
    dayStartMs,
    dayEndMs: dayStartMs + DAY_MS,
    weekend: p.wday === 0 || p.wday === 6,
    preMs: dayStartMs + PRE * 60_000,
    rthMs: dayStartMs + RTH * 60_000,
    postMs: dayStartMs + POST * 60_000,
    closeMs: dayStartMs + CLOSE * 60_000,
  };
}

// Pure numeric comparison, zero Intl calls. Mirrors sessionAt()'s exact
// boundary semantics (strict `<` at every threshold).
export function classify(tsMs: number, seg: DaySegment): Session {
  if (seg.weekend) return "closed";
  if (tsMs < seg.preMs) return "closed";
  if (tsMs < seg.rthMs) return "pre";
  if (tsMs < seg.postMs) return "rth";
  if (tsMs < seg.closeMs) return "post";
  return "closed";
}
