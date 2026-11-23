// Pure sizing resolution — resolves at trigger time against a live quote/account.
// (ui-design §Order entry: Dollar → floor($/price); CashPct → floor(cash×pct/price);
//  BuyingPowerPct → floor(BP×pct/price); PositionFraction → from the live position.)
export type SizingMode = "Dollar" | "CashPct" | "BuyingPowerPct" | "Shares" | "PositionFraction";
export interface SizingSpec {
  mode: SizingMode;
  dollar?: number;                 // Dollar
  pct?: number;                    // CashPct/BuyingPowerPct (0–100)
  shares?: number;                 // Shares
  fraction?: "all" | "half";       // PositionFraction
}
export interface SizingContext { price: number; buyingPower: number; availableCash: number; positionQty: number; fractional?: boolean; exactShares?: boolean }

// A 0-share result is a legitimate, deterministic outcome (e.g. a $100 Dollar
// order on a >$100 stock floors to 0) — `reason` says exactly why so callers
// can surface it instead of a generic "quantity must be greater than 0."
export interface SizedShares { qty: number; reason?: string }

export function floorFractionalShares(qty: number): number {
  return Math.floor(qty * 1e9) / 1e9;
}

export function formatExecutionQuantity(qty: number, maximumFractionDigits = 9): string {
  return qty.toLocaleString("en-US", { maximumFractionDigits });
}

export function resolveShares(spec: SizingSpec, ctx: SizingContext): SizedShares {
  const floor = ctx.fractional ? floorFractionalShares : Math.floor;
  switch (spec.mode) {
    case "Dollar": {
      const dollar = spec.dollar ?? 0;
      const qty = ctx.price > 0 ? Math.max(0, floor(dollar / ctx.price)) : 0;
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
        ? Math.max(0, floor((ctx.buyingPower * pct / 100) / ctx.price))
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
    case "CashPct": {
      const pct = spec.pct ?? 0;
      const qty = ctx.price > 0
        ? Math.max(0, floor((ctx.availableCash * pct / 100) / ctx.price))
        : 0;
      if (qty > 0) return { qty };
      const reason = ctx.availableCash <= 0
        ? "No cash available to size the order."
        : pct <= 0
        ? "Cash % must be greater than 0."
        : ctx.price <= 0
        ? "No live price yet to size the order."
        : `${pct}% of cash ($${(ctx.availableCash * pct / 100).toFixed(2)}) is less than one share at $${ctx.price.toFixed(2)}.`;
      return { qty, reason };
    }
    case "Shares": {
      const qty = Math.max(0, ctx.exactShares ? spec.shares ?? 0 : floor(spec.shares ?? 0));
      if (qty > 0) return { qty };
      return { qty, reason: "Share size must be at least 1." };
    }
    case "PositionFraction": {
      const held = Math.abs(ctx.positionQty);
      const pct = spec.pct ?? 0;
      const qty = Math.max(0, pct === 100 ? held : floor((held * pct) / 100));
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
