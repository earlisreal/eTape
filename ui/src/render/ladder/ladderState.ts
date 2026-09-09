// Pure paint-state math for the L2 ladder. No DOM, no clocks — nowMs and the
// palette arrive in the state so painting is deterministic (goldens).
import type { Book, BookLevel, EstimatedLULD, TickDirection, Order } from "../../wire/contract";
import type { Palette } from "../palette";
import { formatPrice, quoteDecimals, QUOTE_DECIMALS } from "../format";
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

export interface LULDBoundaryRow {
  kind: "luld";
  price: number;
  frozen: boolean;
}

export type LadderEntry = LadderRow | LULDBoundaryRow;

export function isLULDBoundaryRow(row: LadderEntry): row is LULDBoundaryRow {
  return "kind" in row && row.kind === "luld";
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
  /** Real rows are best-first; virtual LULD rows may sit beside them. */
  asks: LadderEntry[];
  bids: LadderEntry[];
  askFallback: LULDBoundaryRow | null;
  bidFallback: LULDBoundaryRow | null;
  decimals: number;
  spread: number | null;
  luld: EstimatedLULD | null;
  averageEntryPrice: number | null;
  averageEntryRowVisible: boolean;
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

/** Depth rendering is currently enabled for U.S. symbols; every other market renders the no-depth state. */
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

function normalizeAverageEntryPrice(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : null;
}

function accumulate(levels: BookLevel[], count: number): LadderRow[] {
  return levels.slice(0, count).map((l) => ({ price: l.price, size: l.size, sizeFraction: 0 }));
}

function pricedLuld(luld: EstimatedLULD | undefined): EstimatedLULD | null {
  if (!luld || (luld.state !== "estimated" && luld.state !== "frozen")) return null;
  if (!Number.isFinite(luld.lower) || !Number.isFinite(luld.upper) || luld.lower <= 0 || luld.upper <= 0 || luld.lower >= luld.upper) return null;
  return luld;
}

function boundaryRow(luld: EstimatedLULD, price: number): LULDBoundaryRow {
  return { kind: "luld", price, frozen: luld.state === "frozen" };
}

function projectBoundary(
  rows: LadderRow[],
  count: number,
  luld: EstimatedLULD | null,
  price: number | undefined,
  side: "bid" | "ask",
): { rows: LadderEntry[]; fallback: LULDBoundaryRow | null } {
  if (!luld || price === undefined) return { rows, fallback: null };
  const equalIndex = rows.findIndex((row) => row.price === price);
  if (equalIndex >= 0 && equalIndex < count) {
    const boundary = boundaryRow(luld, price);
    return { rows: [...rows.slice(0, equalIndex + 1), boundary, ...rows.slice(equalIndex + 1)], fallback: null };
  }
  const insertion = rows.findIndex((row) => side === "bid" ? row.price < price : row.price > price);
  const index = insertion < 0 ? rows.length : insertion;
  if (index >= count) return { rows, fallback: boundaryRow(luld, price) };
  const boundary = boundaryRow(luld, price);
  return { rows: [...rows.slice(0, index), boundary, ...rows.slice(index)], fallback: null };
}

/** Book sides (best-first, as delivered) → ladder rows, each bar length proportional to
 *  that row's own size, normalized against the largest single level across BOTH sides. */
export function buildLadderSides(book: Book | undefined, levels: unknown = DEFAULT_LADDER_LEVELS): {
  asks: LadderEntry[];
  bids: LadderEntry[];
  askFallback: LULDBoundaryRow | null;
  bidFallback: LULDBoundaryRow | null;
} {
  const count = normalizeLadderLevels(levels);
  const asks = accumulate(book?.asks ?? [], count);
  const bids = accumulate(book?.bids ?? [], count);
  const maxSize = Math.max(0, ...asks.map((r) => r.size), ...bids.map((r) => r.size));
  for (const r of asks) r.sizeFraction = depthFraction(r.size, maxSize);
  for (const r of bids) r.sizeFraction = depthFraction(r.size, maxSize);
  const luld = pricedLuld(book?.estimatedLuld);
  const bid = projectBoundary(bids, count, luld, luld?.lower, "bid");
  const ask = projectBoundary(asks, count, luld, luld?.upper, "ask");
  return { asks: ask.rows, bids: bid.rows, askFallback: ask.fallback, bidFallback: bid.fallback };
}

/** Number of complete depth rows that fit below the fixed spread and column headers. */
export function visibleLadderRows(height: number, reservedRows = 0): number {
  const contentHeight = Number.isFinite(height) ? Math.max(0, height - LADDER_CHROME_H) : 0;
  const rows = Math.floor(contentHeight / LADDER_ROW_H);
  const reserve = Number.isFinite(reservedRows) ? Math.max(0, Math.floor(reservedRows)) : 0;
  return Math.max(0, rows - reserve);
}

/** Maximum logical row offset for the current book, setting, and canvas height. */
export function maxLadderOffset(book: Book | undefined, levels: unknown, height: number): number {
  const sides = buildLadderSides(book, levels);
  const sideOffset = (rows: LadderEntry[], fallback: LULDBoundaryRow | null): number => {
    const visible = Math.max(1, visibleLadderRows(height, fallback ? 1 : 0));
    return Math.max(0, rows.length - visible);
  };
  return Math.max(sideOffset(sides.bids, sides.bidFallback), sideOffset(sides.asks, sides.askFallback));
}

export function clampLadderOffset(offset: number, maxOffset: number): number {
  const max = Number.isFinite(maxOffset) ? Math.max(0, Math.floor(maxOffset)) : 0;
  return Math.min(max, normalizeRowOffset(offset));
}

function hasVisibleAverageEntryRow(
  rows: LadderEntry[],
  fallback: LULDBoundaryRow | null,
  price: number | null,
  rowOffset: number,
  visibleRows: number,
): boolean {
  if (price === null || visibleRows <= 0) return false;
  const logicalRows = Math.max(0, visibleRows - (fallback ? 1 : 0));
  return rows.some((row, index) =>
    index >= rowOffset && index - rowOffset < logicalRows && !isLULDBoundaryRow(row) && row.price === price,
  );
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

function luldReason(reason: string): string {
  switch (reason) {
    case "outside_rth": return "OUTSIDE RTH";
    case "tier_unknown": return "TIER UNKNOWN";
    case "registry_expired": return "REGISTRY EXPIRED";
    case "previous_close_unavailable": return "PREV CLOSE UNAVAILABLE";
    case "transport_interrupted": return "CONNECTION INTERRUPTED";
    case "provider_status": return "PROVIDER STATUS";
    default: return reason.replaceAll("_", " ").toUpperCase();
  }
}

function luldValues(luld: EstimatedLULD, decimals: number): string {
  return `${formatPrice(luld.lower, decimals)}–${formatPrice(luld.upper, decimals)}`;
}

export function luldAccessibleText(
  symbol: string,
  luld: EstimatedLULD | null | undefined,
  averageEntryRowVisible = false,
  decimals = QUOTE_DECIMALS,
): string {
  const averageEntry = averageEntryRowVisible ? "; Average-Entry Row visible" : "";
  if (!luld) return `DOM ladder ${symbol}${averageEntry}`;
  const values = luld.state === "estimated" || luld.state === "frozen" ? `; values ${luldValues(luld, decimals)}` : "";
  const registry = luld.registryAsOf ? `; registry as of ${luld.registryAsOf}` : "";
  const reason = luld.reason ? `; reason ${luldReason(luld.reason)}` : "";
  return `DOM ladder ${symbol}; Estimated LULD state ${luld.state}${values}; tier ${luld.tier}${registry}${reason}${averageEntry}`;
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
  averageEntryPrice?: number | null;
  levels?: unknown;
  rowOffset?: number;
}): LadderPaintState {
  const entitled = entitledForDepth(args.symbol);
  const sides = buildLadderSides(entitled ? args.book : undefined, args.levels);
  const rowOffset = normalizeRowOffset(args.rowOffset);
  const averageEntryPrice = normalizeAverageEntryPrice(args.averageEntryPrice);
  const visibleRows = visibleLadderRows(args.height);
  const spread = entitled && args.book?.asks[0] && args.book?.bids[0]
    ? args.book.asks[0].price - args.book.bids[0].price
    : null;
  return {
    symbol: args.symbol,
    entitled,
    asks: sides.asks,
    bids: sides.bids,
    askFallback: sides.askFallback,
    bidFallback: sides.bidFallback,
    decimals: quoteDecimals(args.last?.price),
    spread,
    luld: args.book?.estimatedLuld ?? null,
    averageEntryPrice,
    averageEntryRowVisible: hasVisibleAverageEntryRow(sides.bids, sides.bidFallback, averageEntryPrice, rowOffset, visibleRows)
      || hasVisibleAverageEntryRow(sides.asks, sides.askFallback, averageEntryPrice, rowOffset, visibleRows),
    last: args.last,
    flash: args.flash,
    orders: workingOrderMarks(args.orders, args.symbol),
    nowMs: args.nowMs,
    width: args.width,
    height: args.height,
    rowOffset,
    palette: args.palette,
  };
}
