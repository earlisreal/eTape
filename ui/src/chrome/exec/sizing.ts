// Pure sizing resolution — resolves at trigger time against a live quote/account.
// (ui-design §Order entry: Dollar → floor($/price); BuyingPowerPct → floor(BP×pct/price);
//  PositionFraction → from the live position.)
export type SizingMode = "Dollar" | "BuyingPowerPct" | "Shares" | "PositionFraction";
export interface SizingSpec {
  mode: SizingMode;
  dollar?: number;                 // Dollar
  pct?: number;                    // BuyingPowerPct (0–100)
  shares?: number;                 // Shares
  fraction?: "all" | "half";       // PositionFraction
}
export interface SizingContext { price: number; buyingPower: number; positionQty: number }

export function resolveShares(spec: SizingSpec, ctx: SizingContext): number {
  switch (spec.mode) {
    case "Dollar":
      return ctx.price > 0 ? Math.floor((spec.dollar ?? 0) / ctx.price) : 0;
    case "BuyingPowerPct":
      return ctx.price > 0 ? Math.floor((ctx.buyingPower * (spec.pct ?? 0) / 100) / ctx.price) : 0;
    case "Shares":
      return Math.max(0, Math.floor(spec.shares ?? 0));
    case "PositionFraction": {
      const held = Math.abs(ctx.positionQty);
      return spec.fraction === "half" ? Math.floor(held / 2) : Math.floor(held);
    }
  }
}
