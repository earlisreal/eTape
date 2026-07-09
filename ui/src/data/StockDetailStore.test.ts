import { describe, it, expect } from "vitest";
import { StockDetailStore } from "./StockDetailStore";
import type { StockDetailPayload, SnapshotMsg, DeltaMsg } from "../wire/contract";

const payload = (symbol: string, overrides: Partial<StockDetailPayload> = {}): StockDetailPayload => ({
  symbol,
  name: `${symbol} Inc`,
  industry: "Tech",
  price: 10,
  lastClose: 9.5,
  changePct: 5.2,
  marketCap: 1_000_000,
  floatMarketCap: 900_000,
  sharesOutstanding: 100_000,
  floatShares: 90_000,
  pe: 20,
  peTTM: 21,
  eps: 0.5,
  high52: 15,
  low52: 5,
  volume: 1000,
  refreshedAt: "t1",
  ...overrides,
});
const snap = (p: unknown) => ({ kind: "snapshot", topic: "stock.detail", payload: p } as SnapshotMsg);
const delta = (p: unknown) => ({ kind: "delta", topic: "stock.detail", payload: p } as DeltaMsg);

describe("StockDetailStore", () => {
  it("a snapshot for symbol A followed by a snapshot for symbol B keeps both (snapshot does not clear)", () => {
    const s = new StockDetailStore();
    s.apply(snap(payload("US.AAPL")));
    s.apply(snap(payload("US.NVDA")));
    expect(s.detailFor("US.AAPL")?.symbol).toBe("US.AAPL");
    expect(s.detailFor("US.NVDA")?.symbol).toBe("US.NVDA");
  });

  it("a delta for A overwrites only A's prior values, not merges field-by-field", () => {
    const s = new StockDetailStore();
    s.apply(delta(payload("US.AAPL", { price: 10, refreshedAt: "t1" })));
    s.apply(delta(payload("US.AAPL", { price: 20, refreshedAt: "t2" })));
    expect(s.detailFor("US.AAPL")?.price).toBe(20);
    expect(s.detailFor("US.AAPL")?.refreshedAt).toBe("t2");
  });

  it("ignores a null payload without throwing and without affecting other entries", () => {
    const s = new StockDetailStore();
    s.apply(snap(payload("US.AAPL")));
    expect(() => s.apply(snap(null))).not.toThrow();
    expect(() => s.apply(delta(null))).not.toThrow();
    expect(s.detailFor("US.AAPL")?.symbol).toBe("US.AAPL");
  });

  it("ignores a payload missing/with a non-string symbol without creating a garbage entry", () => {
    const s = new StockDetailStore();
    s.apply(snap(payload("US.AAPL")));
    expect(() => s.apply(delta({ ...payload("US.NVDA"), symbol: undefined }))).not.toThrow();
    expect(() => s.apply(delta({ ...payload("US.NVDA"), symbol: 123 }))).not.toThrow();
    expect(s.detailFor("US.NVDA")).toBeUndefined();
    expect(s.detailFor("US.AAPL")?.symbol).toBe("US.AAPL"); // untouched
  });

  it("detailFor returns undefined for a symbol never seen", () => {
    const s = new StockDetailStore();
    expect(s.detailFor("US.TSLA")).toBeUndefined();
  });
});
