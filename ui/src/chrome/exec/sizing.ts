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

// A 0-share result is a legitimate, deterministic outcome (e.g. a $100 Dollar
// order on a >$100 stock floors to 0) — `reason` says exactly why so callers
// can surface it instead of a generic "quantity must be greater than 0."
export interface SizedShares { qty: number; reason?: string }

export function resolveShares(spec: SizingSpec, ctx: SizingContext): SizedShares {
  switch (spec.mode) {
    case "Dollar": {
      const dollar = spec.dollar ?? 0;
      const qty = ctx.price > 0 ? Math.max(0, Math.floor(dollar / ctx.price)) : 0;
      if (qty > 0) return { qty };
      const reason = dollar <= 0
        ? "Dollar amount must be greater than 0."
        : ctx.price <= 0
        ? "No live price yet to size a dollar order."
        : `$${dollar} is less than one share at $${ctx.price.toFixed(2)}.`;
      return { qty, reason };
    }
    case "BuyingPowerPct": {
      const pct = spec.pct ?? 0;
      const qty = ctx.price > 0
        ? Math.max(0, Math.floor((ctx.buyingPower * pct / 100) / ctx.price))
        : 0;
      if (qty > 0) return { qty };
      const reason = ctx.buyingPower <= 0
        ? "No buying power available to size the order."
        : pct <= 0
        ? "Buying-power % must be greater than 0."
        : ctx.price <= 0
        ? "No live price yet to size the order."
        : `${pct}% of buying power ($${(ctx.buyingPower * pct / 100).toFixed(2)}) is less than one share at $${ctx.price.toFixed(2)}.`;
      return { qty, reason };
    }
    case "Shares": {
      const qty = Math.max(0, Math.floor(spec.shares ?? 0));
      if (qty > 0) return { qty };
      return { qty, reason: "Share size must be at least 1." };
    }
    case "PositionFraction": {
      const held = Math.abs(ctx.positionQty);
      const pct = spec.pct ?? 0;
      const qty = Math.max(0, Math.floor((held * pct) / 100));
      if (qty > 0) return { qty };
      const reason = ctx.positionQty === 0
        ? "No open position to size from."
        : pct <= 0
        ? "Position % must be greater than 0."
        : `${pct}% of ${held} shares rounds to 0.`;
      return { qty, reason };
    }
    default: {
      const _exhaustive: never = spec.mode;
      throw new Error(`resolveShares: unhandled sizing mode ${_exhaustive}`);
    }
  }
}
