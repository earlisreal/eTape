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
