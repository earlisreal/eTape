import { describe, it, expect } from "vitest";
import { resolveShares } from "./sizing";

const ctx = { price: 3.5, buyingPower: 10_000, positionQty: 428 };

describe("resolveShares", () => {
  it("Dollar → floor($/price)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, ctx).qty).toBe(1428); // floor(5000/3.5)
  });
  it("BuyingPowerPct → floor(BP*pct%/price)", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, ctx).qty).toBe(1428); // floor(5000/3.5)
  });
  it("Shares → explicit floor, never negative", () => {
    expect(resolveShares({ mode: "Shares", shares: 300 }, ctx).qty).toBe(300);
    expect(resolveShares({ mode: "Shares", shares: -5 }, ctx).qty).toBe(0);
  });
  it("guards a zero/negative price (no division blowup)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, { ...ctx, price: 0 }).qty).toBe(0);
  });
  it("Dollar → never negative, even with a negative dollar amount", () => {
    expect(resolveShares({ mode: "Dollar", dollar: -5000 }, ctx).qty).toBe(0);
  });
  it("BuyingPowerPct → never negative, even with a negative pct", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: -50 }, ctx).qty).toBe(0);
  });
  it("BuyingPowerPct → never negative, even with a negative buyingPower", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, { ...ctx, buyingPower: -10_000 }).qty).toBe(0);
  });

  // Reason messages — the confirmed real-world case (Case 1): a Dollar order
  // whose amount is less than one share at the live price.
  it("Dollar $100 @ price 150 → reason explains the amount is less than one share", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 100 }, { ...ctx, price: 150 }))
      .toEqual({ qty: 0, reason: "$100 is less than one share at $150.00." });
  });
  it("Dollar <= 0 → reason says the amount itself is invalid", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 0 }, ctx))
      .toEqual({ qty: 0, reason: "Dollar amount must be greater than 0." });
  });
  it("Dollar with no live price → reason says there's no price to size from", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 100 }, { ...ctx, price: 0 }))
      .toEqual({ qty: 0, reason: "No live price yet to size a dollar order." });
  });
  it("BuyingPowerPct with no buying power → reason names that first", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, { ...ctx, buyingPower: 0 }))
      .toEqual({ qty: 0, reason: "No buying power available to size the order." });
  });
  it("BuyingPowerPct pct <= 0 → reason says the pct itself is invalid", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 0 }, ctx))
      .toEqual({ qty: 0, reason: "Buying-power % must be greater than 0." });
  });
  it("BuyingPowerPct with no live price → reason says there's no price to size from", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, { ...ctx, price: 0 }))
      .toEqual({ qty: 0, reason: "No live price yet to size the order." });
  });
  it("BuyingPowerPct floors to 0 → reason spells out the dollar amount vs. price", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 1 }, { ...ctx, buyingPower: 100, price: 150 }))
      .toEqual({ qty: 0, reason: "1% of buying power ($1.00) is less than one share at $150.00." });
  });
  it("Shares 0 → reason: share size must be at least 1", () => {
    expect(resolveShares({ mode: "Shares", shares: 0 }, ctx))
      .toEqual({ qty: 0, reason: "Share size must be at least 1." });
  });
});

describe("resolveShares PositionFraction reads pct", () => {
  const ctx2 = { price: 10, buyingPower: 0, positionQty: 300 };
  it("100 pct = full position", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, ctx2).qty).toBe(300);
  });
  it("50 pct = half, floored", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 50 }, { ...ctx2, positionQty: 3 }).qty).toBe(1);
  });
  it("missing pct = 0 shares", () => {
    expect(resolveShares({ mode: "PositionFraction" }, ctx2).qty).toBe(0);
  });
  it("uses absolute position for shorts", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, { ...ctx2, positionQty: -300 }).qty).toBe(300);
  });

  // Reason messages — the confirmed real-world case (Case 2): flat (no
  // position) so there's nothing to size a fraction of.
  it("PositionFraction pct:100, positionQty:0 → reason: no open position to size from", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, { ...ctx2, positionQty: 0 }))
      .toEqual({ qty: 0, reason: "No open position to size from." });
  });
  it("PositionFraction pct <= 0 with a held position → reason says the pct itself is invalid", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 0 }, ctx2))
      .toEqual({ qty: 0, reason: "Position % must be greater than 0." });
  });
  it("PositionFraction rounds to 0 despite a held position → reason spells out the rounding", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 10 }, { ...ctx2, positionQty: 3 }))
      .toEqual({ qty: 0, reason: "10% of 3 shares rounds to 0." });
  });
});
