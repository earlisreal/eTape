import { describe, it, expect } from "vitest";
import { FillStore } from "./FillStore";
import type { Fill } from "../wire/contract";

const fill = (o: Partial<Fill>): Fill => ({ venue: "alpaca-paper", orderId: "ET1", symbol: "US.AAPL", side: "BUY", qty: 10, price: 3.5, tsMs: 1000, ...o });
const snap = (payload: Fill[]) => ({ kind: "snapshot" as const, topic: "exec.fills" as never, payload });
const delta = (payload: Fill) => ({ kind: "delta" as const, topic: "exec.fills" as never, payload });

describe("FillStore", () => {
  it("buckets fills by symbol and maps to buy/sell FillMarkers", () => {
    const s = new FillStore();
    s.apply(snap([fill({ tsMs: 1000, price: 3.5, side: "BUY" }), fill({ symbol: "US.NVDA", tsMs: 1100, price: 9, side: "SELL" })]));
    expect(s.forSymbol("US.AAPL")).toEqual([{ timeMs: 1000, price: 3.5, side: "buy" }]);
    expect(s.forSymbol("US.NVDA")).toEqual([{ timeMs: 1100, price: 9, side: "sell" }]);
    expect(s.forSymbol("US.TSLA")).toEqual([]);
  });
  it("SHORT/COVER map to sell/buy", () => {
    const s = new FillStore();
    s.apply(delta(fill({ side: "SHORT", orderId: "ET2" })));
    s.apply(delta(fill({ side: "COVER", orderId: "ET3", tsMs: 1200 })));
    expect(s.forSymbol("US.AAPL").map((m) => m.side)).toEqual(["sell", "buy"]);
  });
  it("append-only, deduped by identity (a re-snapshot never doubles or wipes)", () => {
    const s = new FillStore();
    const f = fill({ orderId: "ET1", tsMs: 1000, price: 3.5, qty: 10 });
    s.ingest([f]);
    s.ingest([f]);                                  // duplicate — ignored
    s.ingest([fill({ orderId: "ET4", symbol: "US.MSFT", tsMs: 1300 })]);
    s.apply(snap([f]));                             // reconnect re-snapshot merges, doesn't wipe MSFT
    expect(s.forSymbol("US.AAPL")).toHaveLength(1);
    expect(s.forSymbol("US.MSFT")).toHaveLength(1);
  });
  it("keeps each symbol's markers sorted by time", () => {
    const s = new FillStore();
    s.ingest([fill({ orderId: "b", tsMs: 2000 }), fill({ orderId: "a", tsMs: 1000 })]);
    expect(s.forSymbol("US.AAPL").map((m) => m.timeMs)).toEqual([1000, 2000]);
  });
});
