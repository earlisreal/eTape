import { describe, it, expect } from "vitest";
import { scrollAccumulate } from "./scroll";

describe("scrollAccumulate (wickplot accumulatePan port)", () => {
  it("slow scroll accumulates sub-row movement across events until a whole row is crossed", () => {
    // Row = 8px; four slow wheel events of 2px each = one full row, not four discarded rounds.
    let acc = scrollAccumulate(0, 2, 8);
    expect(acc.rows).toBe(0);
    acc = scrollAccumulate(acc.remainder, 2, 8);
    expect(acc.rows).toBe(0);
    acc = scrollAccumulate(acc.remainder, 2, 8);
    expect(acc.rows).toBe(0);
    acc = scrollAccumulate(acc.remainder, 2, 8);
    expect(acc.rows).toBe(1);
    expect(acc.remainder).toBeCloseTo(0, 6);
  });

  it("fast scroll emits multiple rows and carries the sub-row residue", () => {
    const acc = scrollAccumulate(0, -20, 8);
    expect(acc.rows).toBe(-2); // truncation toward zero, like the Kotlin original
    expect(acc.remainder).toBeCloseTo(-0.5, 6);
  });

  it("is safe when the row height is not positive", () => {
    const acc = scrollAccumulate(0.4, 10, 0);
    expect(acc.rows).toBe(0);
    expect(acc.remainder).toBeCloseTo(0.4, 6);
  });
});
