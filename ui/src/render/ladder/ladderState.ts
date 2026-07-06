// Pure paint-state math for the L2 ladder. No DOM, no clocks — nowMs and the
// palette arrive in the state so painting is deterministic (goldens).
import type { Book, BookLevel, TickDirection, Order } from "../../wire/contract";
import type { Palette } from "../palette";
import { priceDecimals } from "../format";
import { isWorking, sideIsSell } from "../../wire/orderStatus";

export const LADDER_LEVELS = 10;
export const FLASH_MS = 400;

export interface LadderRow {
  price: number;
  size: number;
  cum: number;
  cumFraction: number;
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

function accumulate(levels: BookLevel[]): LadderRow[] {
  let cum = 0;
  return levels.slice(0, LADDER_LEVELS).map((l) => {
    cum += l.size;
    return { price: l.price, size: l.size, cum, cumFraction: 0 };
  });
}

/** Book sides (best-first, as delivered) → ladder rows with cumulative sums normalized across BOTH sides. */
export function buildLadderSides(book: Book | undefined): { asks: LadderRow[]; bids: LadderRow[] } {
  const asks = accumulate(book?.asks ?? []);
  const bids = accumulate(book?.bids ?? []);
  const maxCum = Math.max(asks.at(-1)?.cum ?? 0, bids.at(-1)?.cum ?? 0);
  for (const r of asks) r.cumFraction = depthFraction(r.cum, maxCum);
  for (const r of bids) r.cumFraction = depthFraction(r.cum, maxCum);
  return { asks, bids };
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
}): LadderPaintState {
  const entitled = entitledForDepth(args.symbol);
  const { asks, bids } = buildLadderSides(entitled ? args.book : undefined);
  const prices = [...asks, ...bids].map((r) => r.price);
  if (args.last) prices.push(args.last.price);
  const spread = asks.length > 0 && bids.length > 0 ? asks[0].price - bids[0].price : null;
  return {
    symbol: args.symbol,
    entitled,
    asks,
    bids,
    decimals: priceDecimals(prices),
    spread,
    last: args.last,
    flash: args.flash,
    orders: workingOrderMarks(args.orders, args.symbol),
    nowMs: args.nowMs,
    width: args.width,
    height: args.height,
    palette: args.palette,
  };
}
