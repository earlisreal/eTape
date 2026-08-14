// Pure view math for the time & sales tape. Rows are indexed by ring seq —
// y = rowIndex × TAPE_ROW_H — no viewport classes (Plan-1 roadmap). The pause
// anchor is a (seq, generation) pair: seqs are stable while ticks stream, and
// a generation bump (snapshot rebuild on reconnect) invalidates the anchor so
// a stale frozen view is never rendered as if it were still meaningful.
import type {
  SignificanceLevel, Tick, TickDeliverySource, TickDirection, TickTradeReportCondition,
} from "../../wire/contract";
import type { Palette } from "../palette";
import { formatPrice, formatSize, formatTapeTime, QUOTE_DECIMALS } from "../format";

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
  significance: SignificanceLevel;
  condition: TickTradeReportCondition;
  rawType: number;
  rawTypeSign: number;
  deliverySource: TickDeliverySource;
  rangeEligible: boolean;
  lastEligible: boolean;
  volumeEligible: boolean;
  badge: string;
}

export interface TapePaintState {
  rows: TapeRow[]; // newest first — rows[0] is the top row
  paused: boolean;
  width: number;
  height: number;
  palette: Palette;
}

const CONDITION_META: Record<TickTradeReportCondition, { label: string; badge: string }> = {
  unknown: { label: "Unknown", badge: "UNKNOWN" },
  automaticMatch: { label: "Automatic match", badge: "" },
  late: { label: "Premarket / late", badge: "LATE" },
  nonAutomaticMatch: { label: "Non-automatic match", badge: "NON-AUTO" },
  sameBrokerAutomaticMatch: { label: "Same-broker automatic match", badge: "SAME BRK" },
  sameBrokerNonAutomaticMatch: { label: "Same-broker non-automatic match", badge: "SAME BRK" },
  oddLot: { label: "Odd lot", badge: "ODD" },
  auction: { label: "Auction", badge: "AUCT" },
  bunchedTrade: { label: "Bunched trade", badge: "BUNCH" },
  cashSale: { label: "Cash sale", badge: "CASH" },
  intermarketSweep: { label: "Intermarket sweep", badge: "SWP" },
  bunchedSold: { label: "Bunched sold trade", badge: "BUNCH SOLD" },
  priceVariation: { label: "Price-variation trade", badge: "PRICE VAR" },
  rule127Or155: { label: "Rule 127/155", badge: "RULE 127" },
  delayed: { label: "Delayed / out-of-sequence", badge: "DELAY" },
  marketCenterOfficialClose: { label: "Market-center official close", badge: "MKT CLOSE" },
  nextDaySettlement: { label: "Next-day settlement", badge: "NEXT DAY" },
  marketCenterOpening: { label: "Market-center opening trade", badge: "MKT OPEN" },
  priorReferencePrice: { label: "Prior-reference price", badge: "PRIOR REF" },
  marketCenterOfficialOpen: { label: "Market-center official open", badge: "MKT OPEN" },
  seller: { label: "Seller", badge: "SELLER" },
  formT: { label: "Form T", badge: "FORM T" },
  extendedHours: { label: "Extended trading hours", badge: "EXT" },
  contingent: { label: "Contingent trade", badge: "CONTING" },
  averagePrice: { label: "Average-price trade", badge: "AVG" },
  otcSold: { label: "OTC sold", badge: "OTC SOLD" },
  oddLotIntermarketSweep: { label: "Odd-lot intermarket sweep", badge: "ODD SWP" },
  derivativelyPriced: { label: "Derivatively priced", badge: "DERIV" },
  reopeningPrice: { label: "Reopening price", badge: "REOPEN" },
  closingPrice: { label: "Closing price", badge: "CLOSE" },
  correctedComprehensiveLatePrice: { label: "Corrected comprehensive late price", badge: "CORR LATE" },
  overseas: { label: "Overseas", badge: "OVERSEAS" },
};

export function conditionLabel(condition: TickTradeReportCondition): string {
  return CONDITION_META[condition]?.label ?? CONDITION_META.unknown.label;
}

export function deliveryLabel(source: TickDeliverySource): string {
  switch (source) {
    case "realtime": return "Realtime";
    case "disconnectBackfill": return "Disconnect backfill";
    case "cache": return "Cache";
    default: return "Unknown";
  }
}

function rowBadge(condition: TickTradeReportCondition): string {
  return CONDITION_META[condition]?.badge ?? CONDITION_META.unknown.badge;
}

export function liveView(src: TapeSource): TapeView {
  return { anchorSeq: null, generation: src.generation() };
}

export function buildTapeRows(
  src: TapeSource,
  view: TapeView,
  opts: { symbol: string; minSize: number; maxRows: number },
): { rows: TapeRow[]; paused: boolean; scanned: number } {
  const last = src.lastSeq();
  // An anchor is only meaningful if it is still within the retained window:
  // same generation, not ahead of the newest tick, and not aged out below the
  // oldest retained tick. An anchor that fell below oldestSeq() (evicted from
  // the ring during a long pause) is treated the same as a stale-generation
  // anchor — resume live rather than render a degenerate partial window.
  const anchorValid =
    view.anchorSeq !== null &&
    view.generation === src.generation() &&
    view.anchorSeq >= src.oldestSeq() &&
    view.anchorSeq < last;
  // An anchor that fell off the ring tail clamps to the oldest retained tick.
  const start = Math.max(anchorValid ? (view.anchorSeq as number) : last, src.oldestSeq());
  const raw: Tick[] = [];
  const seqs: number[] = [];
  // scanned = loop iterations, i.e. ring slots visited regardless of whether
  // they matched opts.symbol/minSize. Temporary perf stat (Task 0): once the
  // ring is scoped per-symbol (Tasks 1-4), this should shrink toward
  // rows.length and this field goes away.
  let scanned = 0;
  for (let s = start; s >= src.oldestSeq() && raw.length < opts.maxRows; s--) {
    scanned++;
    const t = src.tickBySeq(s);
    if (!t || t.symbol !== opts.symbol) continue;
    if (t.size < opts.minSize) continue;
    raw.push(t);
    seqs.push(s);
  }
  const rows = raw.map((t, i) => {
    const condition = t.condition;
    const rangeEligible = t.rangeEligible;
    const lastEligible = t.lastEligible;
    const volumeEligible = t.volumeEligible;
    return {
      seq: seqs[i],
      time: formatTapeTime(t.ts),
      price: formatPrice(t.price, QUOTE_DECIMALS),
      size: formatSize(t.size),
      direction: t.direction,
      significance: t.significance ?? "none",
      condition,
      rawType: t.rawType,
      rawTypeSign: t.rawTypeSign,
      deliverySource: t.deliverySource,
      rangeEligible,
      lastEligible,
      volumeEligible,
      badge: rowBadge(condition),
    } satisfies TapeRow;
  });
  return { rows, paused: anchorValid, scanned };
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
