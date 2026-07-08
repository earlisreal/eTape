import type { Quote } from "../../wire/contract";

export type PriceSource = "Bid" | "Ask" | "Last" | "Mid";
export type PriceOffsetUnit = "$" | "%";

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
