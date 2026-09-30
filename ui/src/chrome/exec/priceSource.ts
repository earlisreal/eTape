import type { Quote } from "../../wire/contract";

export type PriceSource = "Bid" | "Ask" | "Last" | "Mid";
export type PriceOffsetUnit = "$" | "%";

function roundOutwardToTick(price: number, up: boolean): number {
  const tick = price >= 1 ? 0.01 : 0.0001;
  const units = price / tick;
  const rounded = up ? Math.ceil(units - 1e-9) : Math.floor(units + 1e-9);
  return Number((rounded * tick).toFixed(tick === 0.01 ? 2 : 4));
}

export function resolveLimitCushionPrice(side: "BUY" | "SELL" | "SHORT" | "COVER", stopPrice: number, cushion: number, unit: PriceOffsetUnit | undefined): number {
  const distance = stopPrice * (unit === "%" ? Math.max(0, cushion) / 100 : 0) + (unit === "%" ? 0 : Math.max(0, cushion));
  const buyish = side === "BUY" || side === "COVER";
  return roundOutwardToTick(buyish ? stopPrice + distance : stopPrice - distance, buyish);
}

// base = the chosen quote leg; the template's signed offset is added on top.
// unit "$" (or absent) adds an absolute amount; "%" adds base * offset / 100 —
// so the offset scales with price (the marketable-limit lesson from the venue
// latency benchmarks). (ui-design §Order entry.)
export function resolvePrice(source: PriceSource, offset: number, unit: PriceOffsetUnit | undefined, quote: Quote): number {
  const base =
    source === "Bid" ? quote.bid :
    source === "Ask" ? quote.ask :
    source === "Last" ? quote.last :
    (quote.bid + quote.ask) / 2;
  return unit === "%" ? base + (base * offset) / 100 : base + offset;
}
