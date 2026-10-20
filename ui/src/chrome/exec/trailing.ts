import type { Order, TrailConfig } from "../../wire/contract";

export function newTrailConfig(side: "SELL" | "COVER" = "SELL"): TrailConfig {
  return {mode:"PRICE",value:0,sessions:{pre:true,rth:true,post:true},pricing:"QUOTE",quoteSide:side==="SELL"?"BID":"ASK",
    cushion:{value:0,unit:"$"},offset:{value:0,unit:"$"},timeframe:"1m",buffer:{value:0,unit:"$"}};
}
export function newConversionConfig():TrailConfig {return {...newTrailConfig(),pricing:"CUSHION",cushion:{value:0,unit:""}};}
export function trailError(c: TrailConfig, positionPct = 100): string | undefined {
  if (!(positionPct>0 && positionPct<=100)) return "Position percentage must be in (0, 100].";
  if (!["PRICE","PERCENT","CANDLE"].includes(c.mode)) return "Choose a supported trail mode.";
  if (c.mode!=="CANDLE" && !(Number.isFinite(c.value) && c.value>0)) return "Enter a positive trail distance.";
  if (c.mode==="CANDLE" && !["10s","1m","5m","15m","30m","60m"].includes(c.timeframe??"1m")) return "Choose an intraday candle timeframe.";
  if (![c.cushion,c.buffer,c.offset].every(x=>Number.isFinite(x.value) && ["$","%", ""].includes(x.unit))) return "Enter finite amounts in dollars or percent.";
  if (c.cushion.value<0 || c.buffer.value<0) return "Cushion and buffer cannot be negative.";
  if (!c.sessions.pre && !c.sessions.rth && !c.sessions.post) return "Select at least one session.";
  if (c.pricing!=="CUSHION" && c.pricing!=="QUOTE" || c.pricing==="QUOTE" && c.quoteSide!=="BID" && c.quoteSide!=="ASK") return "Choose a valid execution recipe.";
  return undefined;
}
export function canConvertProtection(order: Order, orders: Order[]): boolean {
  const entry=orders.find(e=>e.id===order.riskEntryId && e.venue===order.venue && e.symbol===order.symbol);
  return !!entry?.riskEntry && entry.riskEntry.stopId===order.id && entry.executedQty>0 && !entry.riskEntry.closing && !entry.riskEntry.failure && !entry.riskEntry.protectionCanceled &&
    ["SUBMITTED","ACCEPTED","PARTIALLY_FILLED"].includes(order.status) && !order.held?.trail && !!order.held && ["WAITING","ARMED"].includes(order.held.phase) && !order.held.childClientId && !order.held.cancelRequested && !["REQUESTED","UNKNOWN"].includes(order.action?.phase??"");
}
export function trailDetails(order: Order): string {
  const t=order.held?.trail;if(!t)return "";
  const c=t.config;
  const bound=t.floor?` · protection bound ${t.floor}`:"";
  return `${c.mode} trail · ${order.held?.phase}${order.held?.pausedReason?` · ${order.held.pausedReason}`:""}\n${c.mode==="CANDLE"?`${c.timeframe} candle ${t.reference?new Date(t.reference.endMs).toLocaleTimeString():"awaiting verified reference"} · buffer ${c.buffer.value}${c.buffer.unit}`:`Watermark ${t.watermark || "waiting"} · distance ${c.value}${c.mode==="PERCENT"?"%":"$"}`}\n${c.pricing==="QUOTE"?`${c.quoteSide} + ${c.offset.value}${c.offset.unit}`:`Stop cushion ${c.cushion.value}${c.cushion.unit}`} · expires ${new Date(order.held!.deadlineMs).toLocaleTimeString()}\n${order.deferredPositionPct?`${order.deferredPositionPct}% position at trigger`:`${order.leavesQty} owned shares`} · run-bound${bound}`;
}

export function trailModeLabel(order:Order):string {
  const trail=order.held?.trail;
  return trail?.config.mode==="CANDLE" && !trail.reference && (trail.floor??0)>0?"FIXED FALLBACK":trail?.config.mode??"";
}
