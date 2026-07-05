import { describe, it, expect } from "vitest";
import { applyCanvasSize } from "./canvas";

function fakes() {
  const canvas = { width: 0, height: 0 } as HTMLCanvasElement;
  const transforms: number[][] = [];
  const ctx = {
    setTransform: (...args: number[]) => {
      transforms.push(args);
    },
  } as unknown as CanvasRenderingContext2D;
  return { canvas, ctx, transforms };
}

describe("applyCanvasSize", () => {
  it("sizes the backing store by dpr and normalizes the transform to CSS pixels", () => {
    const { canvas, ctx, transforms } = fakes();
    expect(applyCanvasSize(canvas, ctx, 300, 200, 2)).toBe(true);
    expect(canvas.width).toBe(600);
    expect(canvas.height).toBe(400);
    expect(transforms.at(-1)).toEqual([2, 0, 0, 2, 0, 0]);
  });

  it("leaves the backing store alone when the size is unchanged (no canvas clear)", () => {
    const { canvas, ctx } = fakes();
    applyCanvasSize(canvas, ctx, 300, 200, 2);
    const w = canvas.width;
    applyCanvasSize(canvas, ctx, 300, 200, 2);
    expect(canvas.width).toBe(w);
  });

  it("declines zero/negative sizes (panel still measuring)", () => {
    const { canvas, ctx } = fakes();
    expect(applyCanvasSize(canvas, ctx, 0, 200, 2)).toBe(false);
    expect(canvas.width).toBe(0);
  });
});
