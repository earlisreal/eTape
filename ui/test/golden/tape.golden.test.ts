// ui/test/golden/tape.golden.test.ts
import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette } from "../../src/render/palette";
import type { Tick } from "../../src/wire/contract";
import { buildTapeRows, liveView, type TapeSource } from "../../src/render/tape/tapeState";
import { paintTape } from "../../src/render/tape/paintTape";

const W = 260;
const H = 360; // 20 rows × 18

function mkTick(i: number): Tick {
  return {
    symbol: "US.AAPL",
    price: 3.5 + ((i % 5) - 2) * 0.01,
    size: 50 + ((i * 173) % 950),
    direction: (["BUY", "SELL", "NEUTRAL", "BUY", "SELL", "BUY", "BUY", "SELL", "NEUTRAL", "BUY"] as const)[i % 10],
    ts: new Date(Date.UTC(2026, 6, 6, 13, 30, i * 2)).toISOString(),
  };
}

const ticks = Array.from({ length: 30 }, (_, i) => mkTick(i + 1));
const src: TapeSource = {
  lastSeq: () => ticks.length,
  oldestSeq: () => 1,
  generation: () => 1,
  tickBySeq: (s) => (s >= 1 && s <= ticks.length ? ticks[s - 1] : undefined),
};

describe("paintTape goldens", () => {
  for (const mode of ["light", "dark"] as const) {
    const palette = getPalette(mode);

    it(`live tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tape-live-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    it(`min-size-filtered tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 500, maxRows: 20 });
      expectGolden(`tape-filtered-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });

    it(`paused (scrolled back) tape — ${mode}`, () => {
      const { rows, paused } = buildTapeRows(src, { anchorSeq: 24, generation: 1 }, { symbol: "US.AAPL", minSize: 0, maxRows: 20 });
      expectGolden(`tape-paused-${mode}`, renderScene(W, H, (ctx) =>
        paintTape(ctx, { rows, paused, width: W, height: H, palette })));
    });
  }
});
