// Pure paint-state math for the L2 ladder. No DOM, no clocks — nowMs and the
// palette arrive in the state so painting is deterministic (goldens).
import type { Book, BookLevel, TickDirection, Order } from "../../wire/contract";
import type { Palette } from "../palette";
import { QUOTE_DECIMALS } from "../format";
import { isWorking, sideIsSell } from "../../wire/orderStatus";

export const MIN_LADDER_LEVELS = 1;
export const DEFAULT_LADDER_LEVELS = 10;
export const MAX_LADDER_LEVELS = 60;
/** @deprecated Use DEFAULT_LADDER_LEVELS for the default, not a projection cap. */
export const LADDER_LEVELS = DEFAULT_LADDER_LEVELS;
export const LADDER_SPREAD_H = 18;
export const LADDER_HEADER_H = 18;
export const LADDER_ROW_H = 22;
export const LADDER_CHROME_H = LADDER_SPREAD_H + LADDER_HEADER_H;
export const FLASH_MS = 400;

export interface LadderRow {
  price: number;
  size: number;
  sizeFraction: number;
}

export interface OrderMark {
  price: number;
  side: "buy" | "sell";
  qty: number;
}

export interface TradeFlash {
  price: number;
  direction: TickDirection;
  atMs: number;
}

export interface LastTrade {
  price: number;
  direction: TickDirection;
}

export interface LadderPaintState {
  symbol: string;
  entitled: boolean;
  /** Best-first: asks[0] = best ask, bids[0] = best bid. Empty when no book yet. */
  asks: LadderRow[];
  bids: LadderRow[];
  decimals: number;
  spread: number | null;
  last: LastTrade | null;
  flash: TradeFlash | null;
  orders: OrderMark[];
  nowMs: number;
  width: number;
  height: number;
  rowOffset: number;
  palette: Palette;
}

/** The volumeToHeight normalization idiom from wickplot's ChartViewport: value/max with a zero-max guard. */
export function depthFraction(value: number, max: number): number {
  return max <= 0 ? 0 : value / max;
}

/** Full-depth order book is a US LV3 entitlement (CLAUDE.md scope); every other market renders the no-depth state. */
export function entitledForDepth(symbol: string): boolean {
  return symbol.startsWith("US.");
}

export function normalizeLadderLevels(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return DEFAULT_LADDER_LEVELS;
  return Math.min(MAX_LADDER_LEVELS, Math.max(MIN_LADDER_LEVELS, Math.floor(value)));
}

function normalizeRowOffset(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
}

function accumulate(levels: BookLevel[], count: number): LadderRow[] {
  return levels.slice(0, count).map((l) => ({ price: l.price, size: l.size, sizeFraction: 0 }));
}

/** Book sides (best-first, as delivered) → ladder rows, each bar length proportional to
 *  that row's own size, normalized against the largest single level across BOTH sides. */
export function buildLadderSides(book: Book | undefined, levels: unknown = DEFAULT_LADDER_LEVELS): { asks: LadderRow[]; bids: LadderRow[] } {
  const count = normalizeLadderLevels(levels);
  const asks = accumulate(book?.asks ?? [], count);
  const bids = accumulate(book?.bids ?? [], count);
  const maxSize = Math.max(0, ...asks.map((r) => r.size), ...bids.map((r) => r.size));
  for (const r of asks) r.sizeFraction = depthFraction(r.size, maxSize);
  for (const r of bids) r.sizeFraction = depthFraction(r.size, maxSize);
  return { asks, bids };
}

/** Number of complete depth rows that fit below the fixed spread and column headers. */
export function visibleLadderRows(height: number): number {
  const contentHeight = Number.isFinite(height) ? Math.max(0, height - LADDER_CHROME_H) : 0;
  return Math.floor(contentHeight / LADDER_ROW_H);
}

/** Maximum logical row offset for the current book, setting, and canvas height. */
export function maxLadderOffset(book: Book | undefined, levels: unknown, height: number): number {
  const availableDepth = Math.min(
    normalizeLadderLevels(levels),
    Math.max(book?.bids.length ?? 0, book?.asks.length ?? 0),
  );
  return Math.max(0, availableDepth - Math.max(1, visibleLadderRows(height)));
}

export function clampLadderOffset(offset: number, maxOffset: number): number {
  const max = Number.isFinite(maxOffset) ? Math.max(0, Math.floor(maxOffset)) : 0;
  return Math.min(max, normalizeRowOffset(offset));
}

/**
 * Display-only projection of working orders onto the ladder: an order marks the
 * ladder iff it names this symbol, is in a working state, and carries a positive
 * price at its relevant level (limit price for limit/stop-limit, stop price for
 * stop) and remaining quantity. Sell/Short → sell.
 */
export function workingOrderMarks(orders: Order[], symbol: string): OrderMark[] {
  const marks: OrderMark[] = [];
  for (const o of orders) {
    if (o.symbol !== symbol || !isWorking(o.status)) continue;
    const price = o.type === "STOP" ? o.stopPrice : o.limitPrice;
    if (!Number.isFinite(price) || price <= 0) continue;
    const qty = o.leavesQty > 0 ? o.leavesQty : o.qty;
    if (!Number.isFinite(qty) || qty <= 0) continue;
    marks.push({ price, side: sideIsSell(o.side) ? "sell" : "buy", qty });
  }
  return marks;
}

/** 1 at the moment of the trade, linear to 0 at FLASH_MS. 0 for no flash or a skewed clock. */
export function flashAlpha(flash: TradeFlash | null, nowMs: number): number {
  if (!flash) return 0;
  const age = nowMs - flash.atMs;
  if (age < 0 || age >= FLASH_MS) return 0;
  return 1 - age / FLASH_MS;
}

export function buildLadderState(args: {
  symbol: string;
  book: Book | undefined;
  orders: Order[];
  flash: TradeFlash | null;
  last: LastTrade | null;
  nowMs: number;
  width: number;
  height: number;
  palette: Palette;
  levels?: unknown;
  rowOffset?: number;
}): LadderPaintState {
  const entitled = entitledForDepth(args.symbol);
  const { asks, bids } = buildLadderSides(entitled ? args.book : undefined, args.levels);
  const spread = asks.length > 0 && bids.length > 0 ? asks[0].price - bids[0].price : null;
  return {
    symbol: args.symbol,
    entitled,
    asks,
    bids,
    decimals: QUOTE_DECIMALS,
    spread,
    last: args.last,
    flash: args.flash,
    orders: workingOrderMarks(args.orders, args.symbol),
    nowMs: args.nowMs,
    width: args.width,
    height: args.height,
    rowOffset: normalizeRowOffset(args.rowOffset),
    palette: args.palette,
  };
}
