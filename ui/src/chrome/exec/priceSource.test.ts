import { describe, it, expect } from "vitest";
import { resolvePrice } from "./priceSource";
import type { Quote } from "../../wire/contract";

const q: Quote = { symbol: "US.AAPL", bid: 3.40, ask: 3.50, last: 3.45, ts: "" };

describe("resolvePrice", () => {
  it("resolves each source and applies the signed offset", () => {
    expect(resolvePrice("Bid", 0, q)).toBeCloseTo(3.40);
    expect(resolvePrice("Ask", 0, q)).toBeCloseTo(3.50);
    expect(resolvePrice("Last", 0, q)).toBeCloseTo(3.45);
    expect(resolvePrice("Mid", 0, q)).toBeCloseTo(3.45);
    expect(resolvePrice("Ask", 0.02, q)).toBeCloseTo(3.52);
    expect(resolvePrice("Bid", -0.01, q)).toBeCloseTo(3.39);
  });
});
