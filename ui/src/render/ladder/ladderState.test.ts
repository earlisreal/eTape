import { describe, it, expect } from "vitest";
import type { Book, Order } from "../../wire/contract";
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
  it("scales each row's bar to its own size, normalized against the largest level on either side", () => {
    const { asks, bids } = buildLadderSides(book());
    // Largest single level across both sides is bids[1] at 500 — every fraction is /500.
    expect(bids.map((r) => r.sizeFraction)).toEqual([300 / 500, 1, 200 / 500]);
    expect(asks.map((r) => r.sizeFraction)).toEqual([400 / 500, 100 / 500]);
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

const ord = (over: Partial<Order>): Order => ({
  venue: "v", id: "1", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY",
  qty: 100, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 100,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...over,
});

describe("workingOrderMarks (typed Order, Plan 5)", () => {
  it("marks working limit orders for this symbol; sell/short → sell", () => {
    const marks = workingOrderMarks(
      [ord({ id: "1", side: "BUY", limitPrice: 3.5 }),
       ord({ id: "2", side: "SELL", limitPrice: 3.6 }),
       ord({ id: "3", side: "SHORT", limitPrice: 3.7 })],
      "US.AAPL");
    expect(marks).toEqual([
      { price: 3.5, side: "buy", qty: 100 },
      { price: 3.6, side: "sell", qty: 100 },
      { price: 3.7, side: "sell", qty: 100 },
    ]);
  });
  it("excludes filled/terminal, other symbols, and uses stop price for STOP", () => {
    expect(workingOrderMarks([ord({ status: "FILLED" })], "US.AAPL")).toEqual([]);
    expect(workingOrderMarks([ord({ symbol: "US.NVDA" })], "US.AAPL")).toEqual([]);
    expect(workingOrderMarks([ord({ type: "STOP", stopPrice: 3.0, limitPrice: 0, leavesQty: 50 })], "US.AAPL"))
      .toEqual([{ price: 3.0, side: "buy", qty: 50 }]);
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
  it("derives spread from all visible prices; decimals are fixed at 3 (no flicker as sub-penny ticks arrive)", () => {
    const s = buildLadderState(base);
    expect(s.spread).toBeCloseTo(0.02, 9);
    expect(s.decimals).toBe(3);
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
