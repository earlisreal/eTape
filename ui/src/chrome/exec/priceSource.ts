import type { Quote } from "../../wire/contract";

export type PriceSource = "Bid" | "Ask" | "Last" | "Mid";

// base = the chosen quote leg; the template's signed offset is added on top
// (ui-design §Order entry: "price: Bid|Ask|Last|Mid ± offset").
export function resolvePrice(source: PriceSource, offset: number, quote: Quote): number {
  const base =
    source === "Bid" ? quote.bid :
    source === "Ask" ? quote.ask :
    source === "Last" ? quote.last :
    (quote.bid + quote.ask) / 2;
  return base + offset;
}
