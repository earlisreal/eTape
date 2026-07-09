// Client-side pre-checks before the wire (ui-design §Trigger flow step 2):
//   qty > 0; stop/stop-limit price coherence (TZ does not validate — inverted
//   stop-limits sit unfilled); Market outside RTH auto-coerced to Limit-at-last
//   + a visible notice (avoids TZ R78). Pure; nowMs decides the ET session.
//
// This coercion is keyed on the ACTUAL clock (sessionAt(nowMs)), never on
// order.session: it exists purely to stop a naked Market order from reaching
// a broker while the market genuinely isn't open for regular trading (TZ
// hard-rejects one with R78), which is a broker-safety concern independent
// of which session the trader picked for TIF/extended_hours purposes. The
// engine-side session override (exec.ExtendedHoursFor) already only applies
// to Limit/StopLimit orders — Market orders ignore session entirely on the
// wire — so gating this coercion on order.session would let an explicit RTH
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

export function preCheck(draft: DraftOrder, last: number, nowMs: number): PreCheckResult {
  const errors: string[] = [];
  const notices: string[] = [];
  let order: DraftOrder = { ...draft };

  if (!(order.qty > 0)) errors.push("Quantity must be greater than 0.");

  if (order.type === "MARKET" && sessionAt(nowMs) !== "rth") {
    if (last > 0) { order = { ...order, type: "LIMIT", limitPrice: last }; notices.push(`Market outside RTH coerced to Limit @ ${last.toFixed(2)}.`); }
    else errors.push("Market order outside RTH and no last price to coerce to.");
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
