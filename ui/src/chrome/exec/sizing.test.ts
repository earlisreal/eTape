import { describe, it, expect } from "vitest";
import { resolveShares, type SizingSpec } from "./sizing";

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
  it("PositionFraction all/half of |held|", () => {
    expect(resolveShares({ mode: "PositionFraction", fraction: "all" }, ctx)).toBe(428);
    expect(resolveShares({ mode: "PositionFraction", fraction: "half" }, ctx)).toBe(214);
    expect(resolveShares({ mode: "PositionFraction", fraction: "all" }, { ...ctx, positionQty: -100 })).toBe(100);
  });
  it("guards a zero/negative price (no division blowup)", () => {
    expect(resolveShares({ mode: "Dollar", dollar: 5000 }, { ...ctx, price: 0 })).toBe(0);
  });
});
