import { describe, it, expect } from "vitest";
import { resolvePrice } from "./priceSource";

const q = { symbol: "X", bid: 100, ask: 102, last: 101, ts: "" };

describe("resolvePrice", () => {
  it("dollar offset (default when unit undefined) adds absolute", () => {
    expect(resolvePrice("Ask", 0.05, undefined, q)).toBeCloseTo(102.05);
    expect(resolvePrice("Bid", -0.05, "$", q)).toBeCloseTo(99.95);
  });
  it("percent offset scales with base, signed both ways", () => {
    expect(resolvePrice("Ask", 1, "%", q)).toBeCloseTo(102 + 1.02); // +1% of 102
    expect(resolvePrice("Bid", -2, "%", q)).toBeCloseTo(100 - 2); // -2% of 100
  });
});
