import { expect, test } from "vitest";
import { LIGHT } from "../palette";
import { VolumeProfilePrimitive } from "./volumeProfilePrimitive";

test("draws inward at twenty percent of plot width on either edge and combines flat levels", () => {
  const primitive = new VolumeProfilePrimitive(LIGHT);
  primitive.attached({ series: { priceToCoordinate: () => 20 }, requestUpdate: () => {} } as never);
  primitive.setProjection({ status: "ready", detail: "", result: {
    selection: { symbol: "US.TEST", timeframe: "1m", fromMs: 1000, toMs: 2000, rows: 100, valueArea: 70 },
    status: "ready", source: "captured", partial: true, reasons: ["coverage_unproven"],
    rows: [{ lower: 10, upper: 10, volume: 20 }], capturedVolume: 20, poc: 10, vah: 10, val: 10, asOfMs: 1000,
  }});
  for (const placement of ["left", "right"] as const) {
    primitive.setInstance({ instanceId: "vp", type: "VOLUME_PROFILE", params: {}, placement });
    const rectangles: number[][] = [], texts: string[] = [];
    const ctx = { save() {}, restore() {}, fillRect: (...args: number[]) => rectangles.push(args),
      fillText: (text: string) => texts.push(text), measureText: (text: string) => ({ width: text.length*6 }),
      beginPath() {}, moveTo() {}, lineTo() {}, stroke() {}, setLineDash() {},
    } as unknown as CanvasRenderingContext2D;
    const target = { useBitmapCoordinateSpace: (draw: (scope: unknown) => void) => draw({ context: ctx, bitmapSize: { width: 200, height: 80 }, horizontalPixelRatio: 2, verticalPixelRatio: 2 }) };
    for (const view of primitive.paneViews()) view.renderer()!.draw(target as never);
    expect(rectangles[0][0]).toBe(placement === "left" ? 0 : 160);
    expect(rectangles[0][2]).toBe(40);
    expect(texts).toEqual(["POC/VAH/VAL 10.0000"]);
  }
});
