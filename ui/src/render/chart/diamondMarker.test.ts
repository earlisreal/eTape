import { describe, it, expect } from "vitest";
import { diamondHalfSize, drawDiamondPath, hitTestDiamond, fillColor } from "./diamondMarker";
import { LIGHT } from "../palette";

describe("diamond geometry (ported from earlisreal-lightweight-charts 069fa855)", () => {
  it("applies the 0.8 size factor", () => {
    // shapeSize('diamond', s) = round(s * 0.8) made odd; halfSize = (that - 1)/2.
    expect(diamondHalfSize(20)).toBeCloseTo((oddish(20 * 0.8) - 1) / 2, 5);
  });

  it("hit test is Manhattan-distance (rotated square)", () => {
    // size 20 → halfSize ≈ 7; a point 3 right + 3 up (sum 6 ≤ 7) hits; 5+5=10 misses.
    expect(hitTestDiamond(100, 100, 20, 103, 97)).toBe(true);
    expect(hitTestDiamond(100, 100, 20, 105, 95)).toBe(false);
  });

  it("draws a 4-point closed diamond path centered at (x,y)", () => {
    const ops: string[] = [];
    const ctx = {
      beginPath: () => ops.push("begin"),
      moveTo: (x: number, y: number) => ops.push(`move ${x} ${y}`),
      lineTo: (x: number, y: number) => ops.push(`line ${x} ${y}`),
      closePath: () => ops.push("close"),
    };
    drawDiamondPath(ctx, 50, 50, 10);
    expect(ops).toEqual([
      "begin", "move 50 40", "line 40 50", "line 50 60", "line 60 50", "close",
    ]);
  });

  it("maps side to the palette fill color", () => {
    expect(fillColor("buy", LIGHT)).toBe(LIGHT.buyFill);
    expect(fillColor("sell", LIGHT)).toBe(LIGHT.sellFill);
  });
});

function oddish(n: number): number { const r = Math.round(n); return r % 2 === 0 ? r + 1 : r; }
