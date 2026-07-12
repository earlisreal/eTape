// Resolve a PlaceOrderTemplate against a live quote/account/position into a concrete
// venue-tagged SubmitOrderArgs + a human-readable flash string ("BUY 1,428 AAPL @ 3.50 LMT")
// + the pre-check result. Pure; nowMs decides the ET session for RTH coercion.
import type { Quote, SubmitOrderArgs, VenueID } from "../../wire/contract";
import type { PlaceOrderTemplate } from "./actionTemplate";
import { resolveShares } from "./sizing";
import { resolvePrice } from "./priceSource";
import { preCheck, type PreCheckResult, type DraftOrder } from "./preChecks";
import { sideLabel, bareSymbol, abbrevType } from "./orderStatus";

export interface ResolveContext {
  venue: VenueID; symbol: string; quote: Quote;
  buyingPower: number; positionQty: number; nowMs: number;
  extHoursMarketBufferPct: number;
}
export interface ResolvedPlace { args: SubmitOrderArgs; flash: string; preCheck: PreCheckResult }

export function resolvePlaceTemplate(t: PlaceOrderTemplate, ctx: ResolveContext): ResolvedPlace {
  const price = resolvePrice(t.priceSource, t.priceOffset, t.priceOffsetUnit, ctx.quote);
  const { qty, reason } = resolveShares(t.sizing, { price, buyingPower: ctx.buyingPower, positionQty: ctx.positionQty });
  const draft: DraftOrder = {
    symbol: ctx.symbol, side: t.side, type: t.type, tif: t.tif, session: t.session ?? "AUTO", qty,
    limitPrice: t.type === "MARKET" ? 0 : price,
    stopPrice: t.type === "STOP" || t.type === "STOP_LIMIT" ? price : 0,
  };
  const pc = preCheck(draft, ctx.quote, ctx.nowMs, ctx.extHoursMarketBufferPct, reason);
  const o = pc.order;
  const args: SubmitOrderArgs = {
    venue: ctx.venue, symbol: ctx.symbol, side: o.side, type: o.type, tif: o.tif, session: o.session,
    qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice,
  };
  const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(2)} ${abbrevType(o.type)}`;
  const flash = `${sideLabel(o.side)} ${o.qty.toLocaleString("en-US")} ${bareSymbol(ctx.symbol)} @ ${tail}`;
  return { args, flash, preCheck: pc };
}
