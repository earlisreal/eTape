import { describe, expect, it } from "vitest";
import {
  computeTapeColumnLayout,
  TAPE_MIN_GAP,
  TAPE_MIN_WIDTH,
  TAPE_TIME_VISIBLE_MIN_WIDTH,
  TAPE_TIME_WIDTH,
} from "./tapeLayout";

describe("computeTapeColumnLayout", () => {
  it("keeps the two three-column gaps equal at wide widths", () => {
    const layout = computeTapeColumnLayout(400);
    expect(layout.showTime).toBe(true);
    expect(layout.sizeLeft - layout.priceRight).toBe(layout.timeLeft! - layout.sizeRight);
    expect(layout.timeRight! - layout.timeLeft!).toBe(TAPE_TIME_WIDTH);
  });

  it("keeps Time visible through the exact breakpoint", () => {
    const layout = computeTapeColumnLayout(TAPE_TIME_VISIBLE_MIN_WIDTH);
    expect(layout.showTime).toBe(true);
    expect(layout.gap).toBe(TAPE_MIN_GAP);
    expect(layout.sizeLeft - layout.priceRight).toBe(TAPE_MIN_GAP);
    expect(layout.timeLeft! - layout.sizeRight).toBe(TAPE_MIN_GAP);
  });

  it("hides Time immediately below the breakpoint", () => {
    expect(computeTapeColumnLayout(TAPE_TIME_VISIBLE_MIN_WIDTH - 1).showTime).toBe(false);
  });

  it("keeps Price and Size separated at and below the Dockview minimum", () => {
    for (const width of [TAPE_MIN_WIDTH, TAPE_MIN_WIDTH - 1, 0, -10, Number.NaN]) {
      const layout = computeTapeColumnLayout(width);
      expect(layout.showTime).toBe(false);
      expect(Number.isFinite(layout.gap)).toBe(true);
      expect(layout.sizeLeft).toBeGreaterThanOrEqual(layout.priceRight);
      expect(layout.sizeRight).toBeGreaterThanOrEqual(layout.sizeLeft);
      expect(layout.gap).toBeGreaterThanOrEqual(TAPE_MIN_GAP);
    }
  });
});
