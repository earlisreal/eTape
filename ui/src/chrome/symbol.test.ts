import { describe, it, expect } from "vitest";
import { normalizeSymbol } from "./symbol";

describe("normalizeSymbol", () => {
  it("uppercases and US-prefixes a bare ticker", () => expect(normalizeSymbol("aapl")).toBe("US.AAPL"));
  it("leaves an already-qualified symbol", () => expect(normalizeSymbol("HK.00700")).toBe("HK.00700"));
  it("qualifies a MEXC perpetual without changing bare stock tickers", () => {
    expect(normalizeSymbol("mexc:btc/usdt")).toBe("PERP.MEXC.BTC_USDT");
    expect(normalizeSymbol("mexc.btc.usdt")).toBe("PERP.MEXC.BTC_USDT");
    expect(normalizeSymbol("perp.mexc.eth_usdt")).toBe("PERP.MEXC.ETH_USDT");
    expect(normalizeSymbol("btcusdt")).toBe("US.BTCUSDT");
  });
  it("US-prefixes a dotted ticker rather than treating the dot as a market", () =>
    expect(normalizeSymbol("brk.b")).toBe("US.BRK.B"));
});
