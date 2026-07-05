import { describe, it, expect } from "vitest";
import type { Book } from "../../wire/contract";
import { getPalette } from "../palette";
import {
  buildLadderSides, buildLadderState, depthFraction, entitledForDepth,
  flashAlpha, workingOrderMarks, FLASH_MS, LADDER_LEVELS,
} from "./ladderState";

function book(overrides: Partial<Book> = {}): Book {
  return {
    symbol: "US.AAPL",
    bids: [
      { price: 3.49, size: 300 },
      { price: 3.48, size: 500 },
      { price: 3.47, size: 200 },
    ],
    asks: [
      { price: 3.51, size: 400 },
      { price: 3.52, size: 100 },
    ],
    ts: "2026-07-06T13:35:00Z",
    ...overrides,
  };
}

describe("depthFraction (wickplot volumeToHeight idiom)", () => {
  it("normalizes against the max", () => {
    expect(depthFraction(500, 1000)).toBe(0.5);
  });
  it("guards the zero max", () => {
    expect(depthFraction(500, 0)).toBe(0);
  });
});

describe("entitledForDepth", () => {
  it("US symbols have LV3 depth", () => {
    expect(entitledForDepth("US.AAPL")).toBe(true);
  });
  it("everything else does not", () => {
    expect(entitledForDepth("HK.00700")).toBe(false);
  });
});

describe("buildLadderSides", () => {
  it("accumulates cumulative size per side and normalizes fractions across BOTH sides", () => {
    const { asks, bids } = buildLadderSides(book());
    expect(bids.map((r) => r.cum)).toEqual([300, 800, 1000]); // running sums
    expect(asks.map((r) => r.cum)).toEqual([400, 500]);
    // max cum across both sides is 1000 (bid side) — every fraction is /1000
    expect(bids[2].cumFraction).toBe(1);
    expect(asks[1].cumFraction).toBe(0.5);
  });
  it("caps at LADDER_LEVELS per side", () => {
    const levels = Array.from({ length: 15 }, (_, i) => ({ price: 3.49 - i * 0.01, size: 100 }));
    const { bids } = buildLadderSides(book({ bids: levels }));
    expect(bids).toHaveLength(LADDER_LEVELS);
  });
  it("returns empty sides for no book (never fabricated zeros)", () => {
    const { asks, bids } = buildLadderSides(undefined);
    expect(asks).toEqual([]);
    expect(bids).toEqual([]);
  });
});

describe("workingOrderMarks (tolerant until Plan 5 types exec)", () => {
  const orders = [
    { symbol: "US.AAPL", price: 3.47, side: "Buy", qty: 100, status: "New" },
    { symbol: "US.AAPL", price: 3.53, side: "Short", leavesQty: 50, qty: 80, status: "PartiallyFilled" },
    { symbol: "US.AAPL", price: 3.4, side: "Buy", qty: 10, status: "Filled" },   // terminal — hidden
    { symbol: "US.NVDA", price: 9.0, side: "Buy", qty: 10, status: "New" },      // other symbol — hidden
    { symbol: "US.AAPL", side: "Buy", qty: 10, status: "New" },                  // no price (market) — hidden
    "garbage",                                                                    // malformed — hidden
  ];
  it("keeps working orders for this symbol, prefers leavesQty, maps Short to sell", () => {
    expect(workingOrderMarks(orders, "US.AAPL")).toEqual([
      { price: 3.47, side: "buy", qty: 100 },
      { price: 3.53, side: "sell", qty: 50 },
    ]);
  });
});

describe("flashAlpha", () => {
  it("decays linearly from 1 to 0 over FLASH_MS", () => {
    const flash = { price: 3.51, direction: "BUY" as const, atMs: 1000 };
    expect(flashAlpha(flash, 1000)).toBe(1);
    expect(flashAlpha(flash, 1000 + FLASH_MS / 2)).toBeCloseTo(0.5, 6);
    expect(flashAlpha(flash, 1000 + FLASH_MS)).toBe(0);
    expect(flashAlpha(null, 1000)).toBe(0);
    expect(flashAlpha(flash, 999)).toBe(0); // clock skew guard
  });
});

describe("buildLadderState", () => {
  const palette = getPalette("light");
  const base = { symbol: "US.AAPL", book: book(), orders: [], flash: null, last: null, nowMs: 0, width: 300, height: 486, palette };
  it("derives spread and a uniform decimal count from all visible prices", () => {
    const s = buildLadderState(base);
    expect(s.spread).toBeCloseTo(0.02, 9);
    expect(s.decimals).toBe(2);
  });
  it("has null spread when a side is empty", () => {
    const s = buildLadderState({ ...base, book: book({ asks: [] }) });
    expect(s.spread).toBeNull();
  });
  it("drops the book entirely for non-entitled symbols", () => {
    const s = buildLadderState({ ...base, symbol: "HK.00700" });
    expect(s.entitled).toBe(false);
    expect(s.asks).toEqual([]);
    expect(s.bids).toEqual([]);
  });
});
