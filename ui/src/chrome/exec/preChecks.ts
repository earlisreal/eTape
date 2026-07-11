// Client-side pre-checks before the wire (ui-design §Trigger flow step 2):
//   qty > 0; stop/stop-limit price coherence (TZ does not validate — inverted
//   stop-limits sit unfilled); Market outside RTH auto-converted to an
//   aggressive marketable limit (ask/bid ± a buffer%, tick-rounded) + a
//   visible notice (avoids TZ R78). Pure; nowMs decides the ET session.
//
// This conversion is keyed on the ACTUAL clock (sessionAt(nowMs)), never on
// order.session: it exists purely to stop a naked Market order from reaching
// a broker while the market genuinely isn't open for regular trading (TZ
// hard-rejects one with R78), which is a broker-safety concern independent
// of which session the trader picked for TIF/extended_hours purposes. The
// engine-side session override (exec.ExtendedHoursFor) already only applies
// to Limit/StopLimit orders — Market orders ignore session entirely on the
// wire — so gating this conversion on order.session would let an explicit RTH
// choice skip the safety net while the market is actually closed.
import type { OrderType, Side, TIF, OrderSession } from "../../wire/contract";
import { sessionAt } from "../../render/chart/sessions";

export interface DraftOrder {
  symbol: string; side: Side; type: OrderType; tif: TIF; session: OrderSession;
  qty: number; limitPrice: number; stopPrice: number;
}
export interface PreCheckResult {
  ok: boolean;
  order: DraftOrder;    // possibly coerced (Market→Limit outside RTH)
  errors: string[];     // blocking
  notices: string[];    // non-blocking (coercions applied)
}

// SEC sub-penny rule: $0.01 tick at/above $1.00, $0.0001 below. Buys round UP
// and sells round DOWN so a converted marketable limit never lands on an
// invalid price increment and never loses marketability to the rounding.
function tickOf(price: number): number {
  return price >= 1 ? 0.01 : 0.0001;
}
function roundUpToTick(price: number): number {
  const t = tickOf(price);
  return Number((Math.ceil(price / t) * t).toFixed(t === 0.01 ? 2 : 4));
}
function roundDownToTick(price: number): number {
  const t = tickOf(price);
  return Number((Math.floor(price / t) * t).toFixed(t === 0.01 ? 2 : 4));
}

export function preCheck(
  draft: DraftOrder,
  quote: { bid: number; ask: number; last: number },
  nowMs: number,
  extBufferPct: number,
): PreCheckResult {
  const errors: string[] = [];
  const notices: string[] = [];
  let order: DraftOrder = { ...draft };

  if (!(order.qty > 0)) errors.push("Quantity must be greater than 0.");

  // Market outside RTH → aggressive marketable limit (ask×(1+pct) buys /
  // bid×(1−pct) sells), tick-rounded. Falls back to last for a one-sided book.
  if (order.type === "MARKET" && sessionAt(nowMs) !== "rth") {
    const buyish = order.side === "BUY" || order.side === "COVER";
    const leg = buyish ? quote.ask : quote.bid;
    const usedFallback = !(leg > 0);
    const base = usedFallback ? quote.last : leg;
    if (base > 0) {
      const mult = buyish ? 1 + extBufferPct / 100 : 1 - extBufferPct / 100;
      const limitPrice = buyish ? roundUpToTick(base * mult) : roundDownToTick(base * mult);
      order = { ...order, type: "LIMIT", limitPrice };
      const legName = buyish ? "ask" : "bid";
      const sign = buyish ? "+" : "-";
      const shown = limitPrice >= 1 ? limitPrice.toFixed(2) : limitPrice.toFixed(4);
      notices.push(
        usedFallback
          ? `Market outside RTH → Limit @ ${shown} (no ${legName}; last ${sign}${extBufferPct}%).`
          : `Market outside RTH → Limit @ ${shown} (${legName} ${sign}${extBufferPct}%).`,
      );
    } else {
      errors.push("Market order outside RTH and no price to coerce to.");
    }
  }

  if (order.type === "STOP" && !(order.stopPrice > 0)) errors.push("Stop price must be greater than 0.");
  if (order.type === "LIMIT" && !(order.limitPrice > 0)) errors.push("Limit price must be greater than 0.");
  if (order.type === "STOP_LIMIT") {
    if (!(order.stopPrice > 0)) errors.push("Stop price must be greater than 0.");
    if (!(order.limitPrice > 0)) errors.push("Limit price must be greater than 0.");
    if (order.stopPrice > 0 && order.limitPrice > 0) {
      const buyish = order.side === "BUY" || order.side === "COVER";
      if (buyish && order.limitPrice < order.stopPrice) errors.push("Inverted buy stop-limit: limit is below stop (would sit unfilled).");
      if (!buyish && order.limitPrice > order.stopPrice) errors.push("Inverted sell stop-limit: limit is above stop (would sit unfilled).");
    }
  }

  return { ok: errors.length === 0, order, errors, notices };
}
