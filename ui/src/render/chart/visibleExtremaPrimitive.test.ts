import { describe, expect, it, vi } from "vitest";
import { LIGHT } from "../palette";
import { VisibleExtremaPrimitive } from "./visibleExtremaPrimitive";

function recordingTarget() {
  const texts: Array<{ text: string; x: number; y: number }> = [];
  const lines: Array<{ x0: number; y0: number; x1: number; y1: number }> = [];
  const ctx = {
    measureText: (text: string) => ({ width: text.length * 6 }),
    fillText: (text: string, x: number, y: number) => { texts.push({ text, x, y }); },
    beginPath: () => {},
    moveTo: (x: number, y: number) => { (ctx as unknown as { point: { x: number; y: number } }).point = { x, y }; },
    lineTo: (x: number, y: number) => {
      const from = (ctx as unknown as { point: { x: number; y: number } }).point;
      lines.push({ x0: from.x, y0: from.y, x1: x, y1: y });
    },
    stroke: () => {},
    setLineDash: () => {},
    point: { x: 0, y: 0 },
    strokeStyle: "",
    lineWidth: 0,
    font: "",
    textBaseline: "",
  } as unknown as CanvasRenderingContext2D;
  const target = {
    useBitmapCoordinateSpace: (draw: (scope: {
      context: CanvasRenderingContext2D;
      bitmapSize: { width: number; height: number };
      horizontalPixelRatio: number;
      verticalPixelRatio: number;
    }) => void) => draw({ context: ctx, bitmapSize: { width: 100, height: 80 }, horizontalPixelRatio: 1, verticalPixelRatio: 1 }),
  };
  return { target, texts, lines };
}

function drawPrimitive(primitive: VisibleExtremaPrimitive, target: ReturnType<typeof recordingTarget>["target"]): void {
  const view = primitive.paneViews()[0];
  view.renderer()!.draw(target as never);
}

describe("VisibleExtremaPrimitive", () => {
  it("draws ordinary formatted labels with leaders and flips a right-edge label left", () => {
    const primitive = new VisibleExtremaPrimitive(LIGHT);
    const requestUpdate = vi.fn();
    primitive.attached({
      series: { priceToCoordinate: (price: number) => price === 100 ? 20 : 50 },
      chart: { timeScale: () => ({ logicalToCoordinate: (logical: number) => logical === 2 ? 96 : 20 }) },
      requestUpdate,
    } as never);
    primitive.setProjection({ high: { logical: 2, price: 100 }, low: { logical: 1, price: 99 } }, 2);
    const recording = recordingTarget();
    drawPrimitive(primitive, recording.target);

    expect(recording.texts.map((t) => t.text)).toEqual(["100.00", "99.00"]);
    expect(recording.texts[0].x).toBeLessThan(96);
    expect(recording.lines).toHaveLength(2);
    expect(recording.lines[0].x0).toBe(96);
    expect(requestUpdate).toHaveBeenCalled();
  });

  it("uses one combined H/L label for a flat visible range", () => {
    const primitive = new VisibleExtremaPrimitive(LIGHT);
    primitive.attached({
      series: { priceToCoordinate: () => 40 },
      chart: { timeScale: () => ({ logicalToCoordinate: (logical: number) => logical * 20 }) },
      requestUpdate: () => {},
    } as never);
    primitive.setProjection({ high: { logical: 1, price: 10 }, low: { logical: 2, price: 10 } }, 2);
    const recording = recordingTarget();
    drawPrimitive(primitive, recording.target);

    expect(recording.texts.map((t) => t.text)).toEqual(["H/L 10.00"]);
    expect(recording.lines).toHaveLength(1);
  });

  it("clears projection output when disabled", () => {
    const primitive = new VisibleExtremaPrimitive(LIGHT);
    primitive.attached({
      series: { priceToCoordinate: () => 40 },
      chart: { timeScale: () => ({ logicalToCoordinate: () => 20 }) },
      requestUpdate: () => {},
    } as never);
    primitive.setProjection({ high: { logical: 1, price: 10 }, low: { logical: 2, price: 9 } }, 2);
    primitive.setVisible(false);
    const recording = recordingTarget();
    drawPrimitive(primitive, recording.target);

    expect(recording.texts).toEqual([]);
    expect(recording.lines).toEqual([]);
  });

  it("separates close labels while keeping both inside the pane", () => {
    const primitive = new VisibleExtremaPrimitive(LIGHT);
    primitive.attached({
      series: { priceToCoordinate: () => 4 },
      chart: { timeScale: () => ({ logicalToCoordinate: (logical: number) => logical * 20 }) },
      requestUpdate: () => {},
    } as never);
    primitive.setProjection({ high: { logical: 1, price: 10 }, low: { logical: 2, price: 9 } }, 2);
    const recording = recordingTarget();
    drawPrimitive(primitive, recording.target);

    expect(recording.texts.map((t) => t.text)).toEqual(["10.00", "9.00"]);
    expect(recording.texts[0].y).toBeGreaterThanOrEqual(3);
    expect(recording.texts[1].y).toBeGreaterThan(recording.texts[0].y + 11);
    expect(recording.texts[1].y + 11).toBeLessThanOrEqual(77);
  });
});
