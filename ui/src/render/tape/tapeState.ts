// Pure view math for the time & sales tape. Rows are indexed by ring seq —
// y = rowIndex × TAPE_ROW_H — no viewport classes (Plan-1 roadmap). The pause
// anchor is a (seq, generation) pair: seqs are stable while ticks stream, and
// a generation bump (snapshot rebuild on reconnect) invalidates the anchor so
// a stale frozen view is never rendered as if it were still meaningful.
import type { Tick, TickDirection } from "../../wire/contract";
import type { Palette } from "../palette";
import { formatPrice, formatSize, formatTapeTime, priceDecimals } from "../format";

export const TAPE_ROW_H = 18;

/** What the tape needs from TapeRing (satisfied structurally; tests use plain fakes). */
export interface TapeSource {
  lastSeq(): number;
  oldestSeq(): number;
  generation(): number;
  tickBySeq(s: number): Tick | undefined;
}

export interface TapeView {
  anchorSeq: number | null; // seq of the top visible row; null = following live
  generation: number;
}

export interface TapeRow {
  seq: number;
  time: string;
  price: string;
  size: string;
  direction: TickDirection;
}

export interface TapePaintState {
  rows: TapeRow[]; // newest first — rows[0] is the top row
  paused: boolean;
  width: number;
  height: number;
  palette: Palette;
}

export function liveView(src: TapeSource): TapeView {
  return { anchorSeq: null, generation: src.generation() };
}

export function buildTapeRows(
  src: TapeSource,
  view: TapeView,
  opts: { symbol: string; minSize: number; maxRows: number },
): { rows: TapeRow[]; paused: boolean } {
  const last = src.lastSeq();
  const anchorValid = view.anchorSeq !== null && view.generation === src.generation() && view.anchorSeq < last;
  // An anchor that fell off the ring tail clamps to the oldest retained tick.
  const start = Math.max(anchorValid ? (view.anchorSeq as number) : last, src.oldestSeq());
  const raw: Tick[] = [];
  const seqs: number[] = [];
  for (let s = start; s >= src.oldestSeq() && raw.length < opts.maxRows; s--) {
    const t = src.tickBySeq(s);
    if (!t || t.symbol !== opts.symbol) continue;
    if (t.size < opts.minSize) continue;
    raw.push(t);
    seqs.push(s);
  }
  const decimals = priceDecimals(raw.map((t) => t.price));
  const rows = raw.map((t, i) => ({
    seq: seqs[i],
    time: formatTapeTime(t.ts),
    price: formatPrice(t.price, decimals),
    size: formatSize(t.size),
    direction: t.direction,
  }));
  return { rows, paused: anchorValid };
}

/**
 * Move the view by deltaRows visible rows (negative = older). Steps through
 * ticks matching the symbol + filter so one wheel row always moves one
 * on-screen row regardless of filter density. Reaching the live edge resumes
 * following; hitting the retained tail clamps to the oldest match.
 */
export function adjustAnchor(
  src: TapeSource,
  view: TapeView,
  deltaRows: number,
  opts: { symbol: string; minSize: number },
): TapeView {
  const gen = src.generation();
  const last = src.lastSeq();
  const oldest = src.oldestSeq();
  let seq = view.anchorSeq !== null && view.generation === gen ? view.anchorSeq : last;
  const step = deltaRows < 0 ? -1 : 1;
  let remaining = Math.abs(deltaRows);
  while (remaining > 0) {
    let q = seq + step;
    while (q >= oldest && q <= last) {
      const t = src.tickBySeq(q);
      if (t && t.symbol === opts.symbol && t.size >= opts.minSize) break;
      q += step;
    }
    if (q < oldest) break; // tail — stay on the oldest match found so far
    if (q >= last) return { anchorSeq: null, generation: gen }; // live edge — resume
    seq = q;
    remaining--;
  }
  if (seq >= last) return { anchorSeq: null, generation: gen };
  return { anchorSeq: seq, generation: gen };
}
