// Resolve a PlaceOrderTemplate against a live quote/account/position into a concrete
// venue-tagged SubmitOrderArgs + a human-readable flash string ("BUY 1,428 AAPL @ 3.50 LMT")
// + the pre-check result. Pure; nowMs decides the ET session for RTH coercion.
import type { Quote, SubmitOrderArgs, VenueID } from "../../wire/contract";
import type { PlaceOrderTemplate } from "./actionTemplate";
import { resolveShares } from "./sizing";
import { resolveLimitCushionPrice, resolvePrice } from "./priceSource";
import { preCheck, type PreCheckResult, type DraftOrder } from "./preChecks";
import { sideLabel, bareSymbol, abbrevType } from "./orderStatus";

export interface ResolveContext {
  venue: VenueID; symbol: string; quote: Quote;
  buyingPower: number; availableCash: number; positionQty: number; nowMs: number;
  extHoursMarketBufferPct: number;
}
export interface ResolvedPlace { args: SubmitOrderArgs; flash: string; preCheck: PreCheckResult }

function displayPrice(price: number): string { return price < 1 ? price.toFixed(4) : price.toFixed(2); }

export function resolvePlaceTemplate(t: PlaceOrderTemplate, ctx: ResolveContext): ResolvedPlace {
  const sourcePrice = resolvePrice(t.priceSource, t.priceOffset, t.priceOffsetUnit, ctx.quote);
  const limitPrice = t.type === "STOP_LIMIT"
    ? resolveLimitCushionPrice(t.side, sourcePrice, t.limitCushion ?? 0, t.limitCushionUnit)
    : sourcePrice;
  const sizingPrice = t.type === "STOP_LIMIT" ? limitPrice : sourcePrice;
  const deferredPositionPct = t.side === "SELL" && t.type === "STOP_LIMIT" && t.sizing.mode === "PositionFraction"
    ? t.sizing.pct ?? (t.sizing.fraction === "half" ? 50 : 100)
    : undefined;
  const { qty, reason } = deferredPositionPct === undefined
    ? resolveShares(t.sizing, {
      price: sizingPrice, buyingPower: ctx.buyingPower, availableCash: ctx.availableCash, positionQty: ctx.positionQty,
    })
    : { qty: 0 };
  const draft: DraftOrder = {
    symbol: ctx.symbol, side: t.side, type: t.type, tif: t.tif, session: t.session ?? "AUTO", qty,
    ...(deferredPositionPct !== undefined ? { deferredPositionPct } : {}),
    limitPrice: t.type === "MARKET" ? 0 : limitPrice,
    stopPrice: t.type === "STOP" || t.type === "STOP_LIMIT" ? sourcePrice : 0,
  };
  const pc = preCheck(draft, ctx.quote, ctx.nowMs, ctx.extHoursMarketBufferPct, reason);
  const o = pc.order;
  const args: SubmitOrderArgs = {
    venue: ctx.venue, symbol: ctx.symbol, side: o.side, type: o.type, tif: o.tif, session: o.session,
    qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice,
    ...(o.deferredPositionPct !== undefined ? { deferredPositionPct: o.deferredPositionPct } : {}),
  };
  const tail = o.type === "MARKET" ? "MKT" : o.type === "STOP_LIMIT"
    ? `${displayPrice(o.stopPrice)}→${displayPrice(o.limitPrice)} ${abbrevType(o.type)}`
    : `${o.limitPrice.toFixed(2)} ${abbrevType(o.type)}`;
  const size = o.deferredPositionPct === undefined
    ? o.qty.toLocaleString("en-US")
    : `${o.deferredPositionPct}% position`;
  const flash = `${sideLabel(o.side)} ${size} ${bareSymbol(ctx.symbol)} @ ${tail}`;
  return { args, flash, preCheck: pc };
}
