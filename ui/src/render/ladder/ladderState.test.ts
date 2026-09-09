import { describe, it, expect } from "vitest";
import type { Book, EstimatedLULD, Order } from "../../wire/contract";
import type { LadderRow } from "./ladderState";
import { getPalette } from "../palette";
import {
  buildLadderSides, buildLadderState, clampLadderOffset, DEFAULT_LADDER_LEVELS,
  depthFraction, entitledForDepth, flashAlpha, maxLadderOffset, normalizeLadderLevels,
  workingOrderMarks, FLASH_MS, MAX_LADDER_LEVELS, MIN_LADDER_LEVELS,
  isLULDBoundaryRow, luldAccessibleText, visibleLadderRows,
} from "./ladderState";
import { paintLadder } from "./paintLadder";

type RectStroke = { x: number; y: number; width: number; height: number; strokeStyle: string; lineWidth: number };

function recordingContext(): {
  ctx: CanvasRenderingContext2D;
  draws: Array<{ text: string; font: string }>;
  rectStrokes: RectStroke[];
} {
  const draws: Array<{ text: string; font: string }> = [];
  const rectStrokes: RectStroke[] = [];
  let font = "";
  let strokeStyle = "";
  let lineWidth = 1;
  const ctx = {
    clearRect() {}, fillRect() {},
    fillText(text: string) { draws.push({ text, font }); },
    strokeRect(x: number, y: number, width: number, height: number) {
      rectStrokes.push({ x, y, width, height, strokeStyle, lineWidth });
    },
    beginPath() {}, moveTo() {}, lineTo() {}, stroke() {}, setLineDash() {},
    get font() { return font; },
    set font(value: string) { font = value; },
    get strokeStyle() { return strokeStyle; },
    set strokeStyle(value: string) { strokeStyle = value; },
    get lineWidth() { return lineWidth; },
    set lineWidth(value: number) { lineWidth = value; },
  } as unknown as CanvasRenderingContext2D;
  return { ctx, draws, rectStrokes };
}

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

function luld(overrides: Partial<EstimatedLULD> = {}): EstimatedLULD {
  return {
    lower: 3.475,
    upper: 3.515,
    reference: 3.5,
    tier: "T1",
    state: "estimated",
    reason: "",
    registryAsOf: "2026-07-01",
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
    const realBids = bids.filter((row): row is LadderRow => !isLULDBoundaryRow(row));
    const realAsks = asks.filter((row): row is LadderRow => !isLULDBoundaryRow(row));
    expect(realBids.map((r) => r.sizeFraction)).toEqual([300 / 500, 1, 200 / 500]);
    expect(realAsks.map((r) => r.sizeFraction)).toEqual([400 / 500, 100 / 500]);
  });
  it("caps at the default depth per side", () => {
    const levels = Array.from({ length: 15 }, (_, i) => ({ price: 3.49 - i * 0.01, size: 100 }));
    const { bids } = buildLadderSides(book({ bids: levels }));
    expect(bids).toHaveLength(DEFAULT_LADDER_LEVELS);
  });
  it("accepts the full configured depth", () => {
    const levels = Array.from({ length: 61 }, (_, i) => ({ price: 3.49 - i * 0.01, size: 100 }));
    const { bids } = buildLadderSides(book({ bids: levels }), MAX_LADDER_LEVELS);
    expect(bids).toHaveLength(MAX_LADDER_LEVELS);
  });
  it("returns empty sides for no book (never fabricated zeros)", () => {
    const { asks, bids } = buildLadderSides(undefined);
    expect(asks).toEqual([]);
    expect(bids).toEqual([]);
  });
});

describe("normalizeLadderLevels", () => {
  it("defaults invalid persisted values and floors/clamps valid values", () => {
    expect(normalizeLadderLevels(undefined)).toBe(DEFAULT_LADDER_LEVELS);
    expect(normalizeLadderLevels("60")).toBe(DEFAULT_LADDER_LEVELS);
    expect(normalizeLadderLevels(Number.NaN)).toBe(DEFAULT_LADDER_LEVELS);
    expect(normalizeLadderLevels(10.8)).toBe(10);
    expect(normalizeLadderLevels(0)).toBe(MIN_LADDER_LEVELS);
    expect(normalizeLadderLevels(-1)).toBe(MIN_LADDER_LEVELS);
    expect(normalizeLadderLevels(100)).toBe(MAX_LADDER_LEVELS);
  });
});

describe("ladder viewport bounds", () => {
  const deepBook = book({
    bids: Array.from({ length: 60 }, (_, i) => ({ price: 3.49 - i * 0.01, size: i + 1 })),
    asks: [{ price: 3.51, size: 10 }],
  });
  const tenRowsHeight = 36 + 10 * 22;

  it("uses the longer side and configured depth for the maximum offset", () => {
    expect(maxLadderOffset(deepBook, 60, tenRowsHeight)).toBe(50);
    expect(maxLadderOffset(deepBook, 10, tenRowsHeight)).toBe(0);
    expect(maxLadderOffset(book(), 60, tenRowsHeight)).toBe(0);
  });
  it("guards tiny dimensions and clamps the current offset", () => {
    expect(maxLadderOffset(deepBook, 60, 0)).toBe(59);
    expect(clampLadderOffset(55, 50)).toBe(50);
    expect(clampLadderOffset(-1, 50)).toBe(0);
  });

  it("reserves a fallback slot without reducing configured depth", () => {
    const shallow = book({
      bids: Array.from({ length: 5 }, (_, i) => ({ price: 100 - i, size: 10 })),
      asks: Array.from({ length: 5 }, (_, i) => ({ price: 101 + i, size: 10 })),
      estimatedLuld: luld({ lower: 90, upper: 110 }),
    });
    const height = 36 + 3 * 22;
    expect(visibleLadderRows(height, 1)).toBe(2);
    expect(maxLadderOffset(shallow, 5, height)).toBe(3);
  });
});

const ord = (over: Partial<Order>): Order => ({
  venue: "v", id: "1", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO",
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
  it("uses four decimals for a sub-dollar latest trade", () => {
    expect(buildLadderState({ ...base, last: { price: 0.5, direction: "BUY" } }).decimals).toBe(4);
  });
  it("keeps the three-decimal fallback at exactly one dollar and without a trade", () => {
    expect(buildLadderState({ ...base, last: { price: 1, direction: "BUY" } }).decimals).toBe(3);
    expect(buildLadderState(base).decimals).toBe(3);
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
  it("normalizes bars against the full configured depth while scrolled", () => {
    const deep = book({
      bids: Array.from({ length: 60 }, (_, i) => ({ price: 3.49 - i * 0.01, size: i === 59 ? 1000 : 10 })),
      asks: [],
    });
    const s = buildLadderState({ ...base, book: deep, levels: 60, rowOffset: 20 });
    expect(s.bids).toHaveLength(60);
    expect(s.bids[0]).toMatchObject({ sizeFraction: 0.01 });
    expect(s.rowOffset).toBe(20);
  });

  it("inserts lower and upper boundaries in their independent sorted sequences", () => {
    const { bids, asks } = buildLadderSides(book({ estimatedLuld: luld() }), 10);
    expect(bids.map((row) => row.price)).toEqual([3.49, 3.48, 3.475, 3.47]);
    expect(asks.map((row) => row.price)).toEqual([3.51, 3.515, 3.52]);
    expect(isLULDBoundaryRow(bids[2])).toBe(true);
    expect(isLULDBoundaryRow(asks[1])).toBe(true);
  });

  it("keeps equal-price real rows first and adds a separate boundary row", () => {
    const { bids, asks } = buildLadderSides(book({ estimatedLuld: luld({ lower: 3.48, upper: 3.51 }) }), 10);
    expect(bids.map((row) => row.price)).toEqual([3.49, 3.48, 3.48, 3.47]);
    expect(asks.map((row) => row.price)).toEqual([3.51, 3.51, 3.52]);
    expect(bids[1]).not.toEqual(bids[2]);
    expect(asks[0]).not.toEqual(asks[1]);
  });

  it("uses bottom fallbacks when a boundary is beyond configured depth", () => {
    const deep = book({
      bids: Array.from({ length: 60 }, (_, i) => ({ price: 100 - i, size: 10 })),
      asks: Array.from({ length: 60 }, (_, i) => ({ price: 101 + i, size: 10 })),
      estimatedLuld: luld({ lower: 80, upper: 130 }),
    });
    const sides = buildLadderSides(deep, 10);
    expect(sides.bids).toHaveLength(10);
    expect(sides.asks).toHaveLength(10);
    expect(sides.bidFallback).toEqual({ kind: "luld", price: 80, frozen: false });
    expect(sides.askFallback).toEqual({ kind: "luld", price: 130, frozen: false });
    const maxOffset = maxLadderOffset(deep, 5, 36 + 3 * 22);
    const scrolled = buildLadderState({
      symbol: "US.AAPL", book: deep, orders: [], flash: null, last: null, nowMs: 0,
      width: 300, height: 36 + 3 * 22, palette: getPalette("light"), levels: 5, rowOffset: maxOffset,
    });
    expect(scrolled.bids.slice(maxOffset, maxOffset + 2).some((row) => !isLULDBoundaryRow(row) && row.price === 96)).toBe(true);
  });

  it("keeps in-range rows scrollable and qualifies frozen rows", () => {
    const state = buildLadderState({
      symbol: "US.AAPL",
      book: book({ estimatedLuld: luld({ state: "frozen", reason: "provider_status" }) }),
      orders: [], flash: null, last: null, nowMs: 0, width: 300, height: 80,
      palette: getPalette("light"), levels: 10, rowOffset: 1,
    });
    expect(state.bids.findIndex(isLULDBoundaryRow)).toBe(2);
    expect(state.asks.findIndex(isLULDBoundaryRow)).toBe(1);
    expect(state.bids.slice(state.rowOffset, state.rowOffset + 2).some(isLULDBoundaryRow)).toBe(true);
    expect(state.bids.find((row) => isLULDBoundaryRow(row))).toMatchObject({ kind: "luld", frozen: true });
  });

  it("creates no visual rows for warming, unavailable, invalid, unknown, or unpriced frozen values", () => {
    const cases: EstimatedLULD[] = [
      luld({ state: "warming", lower: 0, upper: 0 }),
      luld({ state: "unavailable", lower: 0, upper: 0 }),
      luld({ state: "unknown", lower: 3.4, upper: 3.6 }),
      luld({ state: "estimated", lower: Number.NaN }),
      luld({ state: "frozen", lower: 0, upper: 3.6 }),
    ];
    for (const estimatedLuld of cases) {
      const sides = buildLadderSides(book({ estimatedLuld }), 10);
      expect(sides.bids.some(isLULDBoundaryRow)).toBe(false);
      expect(sides.asks.some(isLULDBoundaryRow)).toBe(false);
      expect(sides.bidFallback).toBeNull();
      expect(sides.askFallback).toBeNull();
    }
  });

  it("keeps LULD state and metadata in accessible text", () => {
    expect(luldAccessibleText("US.AAPL", luld({ state: "frozen", reason: "provider_status" })))
      .toContain("state frozen; values 3.475–3.515; tier T1; registry as of 2026-07-01; reason PROVIDER STATUS");
  });
  it("uses the selected precision for sub-dollar LULD accessibility values", () => {
    expect(luldAccessibleText("US.PENNY", luld({ lower: 0.3475, upper: 0.3515 }), false, 4))
      .toContain("values 0.3475–0.3515");
  });

  it("uses one selected precision for rows, spread, and LULD fallbacks", () => {
    const testBook = book({
      bids: [{ price: 0.349, size: 10 }],
      asks: [{ price: 0.351, size: 20 }],
      estimatedLuld: luld({ lower: 0.3, upper: 0.4 }),
    });
    const texts: string[] = [];
    const ctx = {
      clearRect() {}, fillRect() {}, fillText(text: string) { texts.push(text); },
      beginPath() {}, moveTo() {}, lineTo() {}, stroke() {}, setLineDash() {},
    } as unknown as CanvasRenderingContext2D;
    paintLadder(ctx, buildLadderState({
      ...base, book: testBook, last: { price: 0.5, direction: "BUY" }, height: 80, levels: 1,
    }));
    expect(texts).toContain("0.3490 × 0.3510 · spread 0.0020");
    expect(texts).toContain("0.3000");
    expect(texts).toContain("0.4000");
  });

  it("keeps the BBO strip and omits dashed markers", () => {
    const estimatedLuld = luld({ lower: 99.5, upper: 100.5 });
    const testBook = book({
      bids: [{ price: 99, size: 10 }],
      asks: [{ price: 101, size: 20 }],
      estimatedLuld,
    });
    const texts: string[] = [];
    let dashed = 0;
    const ctx = {
      clearRect() {}, fillRect() {}, fillText(text: string) { texts.push(text); },
      beginPath() {}, moveTo() {}, lineTo() {}, stroke() {}, setLineDash() { dashed++; },
    } as unknown as CanvasRenderingContext2D;
    paintLadder(ctx, buildLadderState({
      symbol: "US.AAPL", book: testBook, orders: [], flash: null, last: null, nowMs: 0,
      width: 300, height: 80, palette: getPalette("light"), levels: 10,
    }));
    expect(texts).toContain("99.000 × 101.000 · spread 2.000");
    expect(texts).toContain("LULD");
    expect(dashed).toBe(0);
  });
});

describe("Average-Entry Row", () => {
  const palette = getPalette("light");
  const base = { symbol: "US.AAPL", book: book(), orders: [], flash: null, last: null, nowMs: 0, width: 300, height: 480, palette };

  it("matches a valid exact real row and bolds its price and size", () => {
    const state = buildLadderState({ ...base, averageEntryPrice: 3.49 });
    expect(state.averageEntryPrice).toBe(3.49);
    expect(state.averageEntryRowVisible).toBe(true);

    const { ctx, draws, rectStrokes } = recordingContext();
    paintLadder(ctx, state);
    expect(draws.filter(({ font }) => font.startsWith("700 11px")).map(({ text }) => text)).toEqual(["300", "3.490"]);
    expect(rectStrokes).toEqual([{
      x: 0.5, y: 36.5, width: 149, height: 21, strokeStyle: palette.text, lineWidth: 1,
    }]);
  });

  it("frames an exact ordinary ask row inside the ask half", () => {
    const state = buildLadderState({ ...base, averageEntryPrice: 3.51 });
    const { ctx, draws, rectStrokes } = recordingContext();
    paintLadder(ctx, state);
    expect(draws.filter(({ font }) => font.startsWith("700 11px")).map(({ text }) => text)).toEqual(["3.510", "400"]);
    expect(rectStrokes).toEqual([{
      x: 151.5, y: 36.5, width: 148, height: 21, strokeStyle: palette.text, lineWidth: 1,
    }]);
  });

  it("bolds both real sides when a transient crossed book has the same exact price", () => {
    const state = buildLadderState({
      ...base,
      book: book({
        bids: [{ price: 3.49, size: 300 }, { price: 3.48, size: 200 }],
        asks: [{ price: 3.49, size: 400 }, { price: 3.52, size: 100 }],
      }),
      averageEntryPrice: 3.49,
    });
    expect(state.averageEntryRowVisible).toBe(true);

    const { ctx, draws, rectStrokes } = recordingContext();
    paintLadder(ctx, state);
    expect(draws.filter(({ font }) => font.startsWith("700 11px")).map(({ text }) => text))
      .toEqual(["300", "3.490", "3.490", "400"]);
    expect(rectStrokes).toHaveLength(2);
    expect(rectStrokes[0].x + rectStrokes[0].width + 0.5).toBeLessThanOrEqual(150);
    expect(rectStrokes[1].x - 0.5).toBeGreaterThanOrEqual(151);
    for (const border of rectStrokes) {
      expect(border.y).toBe(36.5);
      expect(border.height).toBe(21);
      expect(border.strokeStyle).toBe(palette.text);
      expect(border.lineWidth).toBe(1);
    }
  });

  it("ignores invalid bases and virtual LULD boundary rows", () => {
    for (const averageEntryPrice of [undefined, null, 0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      const state = buildLadderState(averageEntryPrice === undefined ? base : { ...base, averageEntryPrice });
      expect(state.averageEntryPrice).toBeNull();
      expect(state.averageEntryRowVisible).toBe(false);
      const { ctx, rectStrokes } = recordingContext();
      paintLadder(ctx, state);
      expect(rectStrokes).toHaveLength(0);
    }

    const state = buildLadderState({
      ...base,
      book: book({
        bids: [{ price: 3.49, size: 300 }, { price: 3.47, size: 200 }],
        estimatedLuld: luld({ lower: 3.48, upper: 3.515 }),
      }),
      averageEntryPrice: 3.48,
    });
    expect(state.averageEntryPrice).toBe(3.48);
    expect(state.averageEntryRowVisible).toBe(false);
    const { ctx, draws, rectStrokes } = recordingContext();
    paintLadder(ctx, state);
    expect(draws.some(({ text, font }) => text === "3.480" && font.startsWith("700 "))).toBe(false);
    expect(rectStrokes).toHaveLength(0);
  });

  it("reports the cue only while the exact real row is visible", () => {
    const deep = book({
      bids: Array.from({ length: 5 }, (_, i) => ({ price: 100 - i, size: 10 })),
      asks: [],
    });
    const offscreen = buildLadderState({ ...base, book: deep, levels: 5, height: 80, averageEntryPrice: 96 });
    expect(offscreen.averageEntryRowVisible).toBe(false);
    expect(luldAccessibleText(offscreen.symbol, offscreen.luld, offscreen.averageEntryRowVisible))
      .not.toContain("Average-Entry Row");
    const offscreenRecording = recordingContext();
    paintLadder(offscreenRecording.ctx, offscreen);
    expect(offscreenRecording.rectStrokes).toHaveLength(0);

    const visible = buildLadderState({ ...base, book: deep, levels: 5, height: 80, rowOffset: 3, averageEntryPrice: 96 });
    expect(visible.averageEntryRowVisible).toBe(true);
    expect(luldAccessibleText(visible.symbol, visible.luld, visible.averageEntryRowVisible))
      .toContain("Average-Entry Row visible");
    const visibleRecording = recordingContext();
    paintLadder(visibleRecording.ctx, visible);
    expect(visibleRecording.rectStrokes).toHaveLength(1);
  });
});
