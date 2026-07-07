import { describe, it, expect } from "vitest";
import { normalizeSymbol } from "./symbol";

describe("normalizeSymbol", () => {
  it("uppercases and US-prefixes a bare ticker", () => expect(normalizeSymbol("aapl")).toBe("US.AAPL"));
  it("leaves an already-qualified symbol", () => expect(normalizeSymbol("HK.00700")).toBe("HK.00700"));
  it("US-prefixes a dotted ticker rather than treating the dot as a market", () =>
    expect(normalizeSymbol("brk.b")).toBe("US.BRK.B"));
});
