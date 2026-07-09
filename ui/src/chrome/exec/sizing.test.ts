import { describe, it, expect } from "vitest";
import { resolveShares } from "./sizing";

const ctx = { price: 3.5, buyingPower: 10_000, positionQty: 428 };

describe("resolveShares", () => {
  it("Dollar → floor($/price)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, ctx)).toBe(1428); // floor(5000/3.5)
  });
  it("BuyingPowerPct → floor(BP*pct%/price)", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, ctx)).toBe(1428); // floor(5000/3.5)
  });
  it("Shares → explicit floor, never negative", () => {
    expect(resolveShares({ mode: "Shares", shares: 300 }, ctx)).toBe(300);
    expect(resolveShares({ mode: "Shares", shares: -5 }, ctx)).toBe(0);
  });
  it("guards a zero/negative price (no division blowup)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, { ...ctx, price: 0 })).toBe(0);
  });
  it("Dollar → never negative, even with a negative dollar amount", () => {
    expect(resolveShares({ mode: "Dollar", dollar: -5000 }, ctx)).toBe(0);
  });
  it("BuyingPowerPct → never negative, even with a negative pct", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: -50 }, ctx)).toBe(0);
  });
  it("BuyingPowerPct → never negative, even with a negative buyingPower", () => {
    expect(resolveShares({ mode: "BuyingPowerPct", pct: 50 }, { ...ctx, buyingPower: -10_000 })).toBe(0);
  });
});

describe("resolveShares PositionFraction reads pct", () => {
  const ctx2 = { price: 10, buyingPower: 0, positionQty: 300 };
  it("100 pct = full position", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, ctx2)).toBe(300);
  });
  it("50 pct = half, floored", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 50 }, { ...ctx2, positionQty: 3 })).toBe(1);
  });
  it("missing pct = 0 shares", () => {
    expect(resolveShares({ mode: "PositionFraction" }, ctx2)).toBe(0);
  });
  it("uses absolute position for shorts", () => {
    expect(resolveShares({ mode: "PositionFraction", pct: 100 }, { ...ctx2, positionQty: -300 })).toBe(300);
  });
});
