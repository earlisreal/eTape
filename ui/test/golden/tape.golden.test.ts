// ui/test/golden/tape.golden.test.ts
import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette } from "../../src/render/palette";
import type { Tick } from "../../src/wire/contract";
import { buildTapeRows, liveView, type TapeSource } from "../../src/render/tape/tapeState";
import { paintTape } from "../../src/render/tape/paintTape";
import { TAPE_MIN_WIDTH, TAPE_TIME_VISIBLE_MIN_WIDTH } from "../../src/render/tape/tapeLayout";

const W = 260;
const H = 360; // 20 rows × 18

function mkTick(i: number, over: Partial<Tick> = {}): Tick {
  return {
    symbol: "US.AAPL",
    price: 3.5 + ((i % 5) - 2) * 0.01,
    size: 50 + ((i * 173) % 950),
    direction: (["BUY", "SELL", "NEUTRAL", "BUY", "SELL", "BUY", "BUY", "SELL", "NEUTRAL", "BUY"] as const)[i % 10],
    transactionType: "regular",
    significance: "none",
    ts: new Date(Date.UTC(2026, 6, 6, 13, 30, i * 2)).toISOString(),
    ...over,
  };
}

const ticks = Array.from({ length: 30 }, (_, i) => mkTick(i + 1));
function sourceFor(tapeTicks: Tick[]): TapeSource {
  return {
    lastSeq: () => tapeTicks.length,
    oldestSeq: () => 1,
    generation: () => 1,
    tickBySeq: (s) => (s >= 1 && s <= tapeTicks.length ? tapeTicks[s - 1] : undefined),
  };
}

const src = sourceFor(ticks);

const significantDirections = ["BUY", "SELL", "NEUTRAL"] as const;
const largeVariants = significantDirections.map((direction) => {
  const variantTicks = ticks.map((t, i) => i === ticks.length - 1
    ? { ...t, direction, size: 12_500, significance: "large" as const }
    : t);
  return { direction, ticks: variantTicks, src: sourceFor(variantTicks) };
});
const exceptionalVariants = significantDirections.map((direction) => {
  const variantTicks = ticks.map((t, i) => i === ticks.length - 1
    ? { ...t, direction, size: 25_000, significance: "exceptional" as const }
    : t);
  return { direction, ticks: variantTicks, src: sourceFor(variantTicks) };
});

describe("paintTape goldens (full-row color)", () => {
  for (const mode of ["light", "dark"] as const) {
    const palette = getPalette(mode);

    it(`live tape — buy/sell/neutral mix — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tapecolor-live-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    for (const variant of largeVariants) {
      const suffix = variant.direction === "BUY" ? "large" : `large-${variant.direction.toLowerCase()}`;
      it(`a Large ${variant.direction} print renders bold — ${mode}`, () => {
        const { rows, paused } = buildTapeRows(variant.src, liveView(variant.src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
        expectGolden(`tapecolor-${suffix}-${mode}`, renderScene(W, H, (ctx) =>
          paintTape(ctx, { rows, paused, width: W, height: H, palette })));
      });
    }

    for (const variant of exceptionalVariants) {
      const suffix = variant.direction === "BUY" ? "exceptional" : `exceptional-${variant.direction.toLowerCase()}`;
      it(`an Exceptional ${variant.direction} print renders strong tint and edge — ${mode}`, () => {
        const { rows, paused } = buildTapeRows(variant.src, liveView(variant.src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
        expectGolden(`tapecolor-${suffix}-${mode}`, renderScene(W, H, (ctx) =>
          paintTape(ctx, { rows, paused, width: W, height: H, palette })));
      });
    }

    it(`an Exceptional print at minimum width keeps the edge visible — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(exceptionalVariants[0].src, liveView(exceptionalVariants[0].src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tapecolor-exceptional-narrow-${mode}`, renderScene(TAPE_MIN_WIDTH, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: TAPE_MIN_WIDTH, height: H, palette })));
    });

    it(`min-size-filtered tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 500, maxRows: 20 });
      expectGolden(`tapecolor-filtered-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    it(`paused (scrolled back) tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, { anchorSeq: 24, generation: 1 }, { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tapecolor-paused-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    it(`paused tape with zero matching rows still shows the warn strip — ${mode}`, () => {
      // Regression: a paused view whose window has no rows (e.g. a minSize
      // filter excludes every tick at the anchor) must still render the
      // honesty-policy warn strip, not just "no prints yet".
      expectGolden(`tapecolor-paused-empty-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows: [], paused: true, width: W, height: H, palette })));
    });

    it(`minimum-width tape hides Time without overlapping Price and Size — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tapecolor-narrow-${mode}`, renderScene(TAPE_MIN_WIDTH, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: TAPE_MIN_WIDTH, height: H, palette })));
    });

    it(`compact three-column breakpoint keeps Time visible — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tapecolor-time-breakpoint-${mode}`, renderScene(TAPE_TIME_VISIBLE_MIN_WIDTH, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: TAPE_TIME_VISIBLE_MIN_WIDTH, height: H, palette })));
    });
  }
});
