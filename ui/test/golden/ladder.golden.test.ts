import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette } from "../../src/render/palette";
import type { Book, Order } from "../../src/wire/contract";
import { buildLadderState, FLASH_MS } from "../../src/render/ladder/ladderState";
import { paintLadder } from "../../src/render/ladder/paintLadder";

const W = 300;
const H = 486; // header 20 + 10×22 + center 26 + 10×22
const NOW = 1_000_000; // fixed clock — flash age is NOW - atMs

// Deterministic fixture generators (no Math.random — multiplicative patterns).
function fullBook(): Book {
  return {
    symbol: "US.AAPL",
    bids: Array.from({ length: 10 }, (_, i) => ({ price: 3.49 - i * 0.01, size: 300 + ((i * 137) % 900) })),
    asks: Array.from({ length: 10 }, (_, i) => ({ price: 3.51 + i * 0.01, size: 250 + ((i * 211) % 1100) })),
    ts: "2026-07-06T13:35:00Z",
  };
}

// Order prices must land EXACTLY on generated level prices (marks match with
// ===): 3.49 - 2*0.01 === 3.47 and 3.51 + 2*0.01 === 3.53 hold in IEEE-754 —
// re-verify exact equality if you change the fixture's step or order prices.
const ord = (o: Partial<Order>): Order => ({
  venue: "alpaca-paper", id: "x", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY",
  qty: 100, limitPrice: 0, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 0,
  avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 0, updatedMs: 0, ...o,
});
const workingOrders: Order[] = [
  ord({ id: "o1", side: "BUY", limitPrice: 3.47, qty: 100, leavesQty: 100, status: "ACCEPTED" }),
  ord({ id: "o2", side: "SHORT", limitPrice: 3.53, qty: 80, leavesQty: 50, status: "PARTIALLY_FILLED" }),
];

describe("paintLadder goldens", () => {
  for (const mode of ["light", "dark"] as const) {
    const palette = getPalette(mode);
    const base = { orders: [] as Order[], flash: null, last: null, nowMs: NOW, width: W, height: H, palette };

    it(`full book with working-order marks — ${mode}`, () => {
      const s = buildLadderState({
        ...base, symbol: "US.AAPL", book: fullBook(), orders: workingOrders,
        last: { price: 3.51, direction: "BUY" },
      });
      expectGolden(`ladder-full-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });

    it(`empty book (waiting for depth) — ${mode}`, () => {
      const s = buildLadderState({ ...base, symbol: "US.AAPL", book: undefined });
      expectGolden(`ladder-empty-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });

    it(`last-trade flash mid-decay — ${mode}`, () => {
      const s = buildLadderState({
        ...base, symbol: "US.AAPL", book: fullBook(),
        last: { price: 3.51, direction: "BUY" },
        flash: { price: 3.51, direction: "BUY", atMs: NOW - FLASH_MS / 2 }, // alpha 0.5
      });
      expectGolden(`ladder-flash-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });

    it(`no depth entitlement (non-US) — ${mode}`, () => {
      const s = buildLadderState({ ...base, symbol: "HK.00700", book: undefined });
      expectGolden(`ladder-noentitle-${mode}`, renderScene(W, H, (ctx) => paintLadder(ctx, s)));
    });
  }
});
