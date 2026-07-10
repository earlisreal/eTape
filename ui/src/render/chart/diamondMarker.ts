// Ported from ~/Projects/earlisreal-lightweight-charts @ 069fa855
// (drawDiamond / hitTestDiamond, Manhattan hit test, 0.8 size factor). Kept pure so
// the primitive (diamondPrimitive.ts) stays a thin LWC adapter.
import type { Palette } from "../palette";

export interface FillMarker { timeMs: number; price: number; side: "buy" | "sell" | "short" }

// Minimal 2D-context surface the path routine needs (so it is testable with a fake).
export interface PathCtx {
  beginPath(): void;
  moveTo(x: number, y: number): void;
  lineTo(x: number, y: number): void;
  closePath(): void;
}

// shapeSize('diamond', size) === size * 0.8, rounded to an odd integer (LWC keeps
// marker shapes odd-sized so they center on a pixel). halfSize = (shapeSize - 1)/2.
function shapeSize(size: number): number {
  const r = Math.round(size * 0.8);
  return r % 2 === 0 ? r + 1 : r;
}
export function diamondHalfSize(size: number): number {
  return (shapeSize(size) - 1) / 2;
}

export function drawDiamondPath(ctx: PathCtx, x: number, y: number, halfSize: number): void {
  ctx.beginPath();
  ctx.moveTo(x, y - halfSize);
  ctx.lineTo(x - halfSize, y);
  ctx.lineTo(x, y + halfSize);
  ctx.lineTo(x + halfSize, y);
  ctx.closePath();
}

export function hitTestDiamond(cx: number, cy: number, size: number, x: number, y: number): boolean {
  const halfSize = diamondHalfSize(size);
  return Math.abs(cx - x) + Math.abs(cy - y) <= halfSize; // Manhattan ball = rotated square
}

export function fillColor(side: "buy" | "sell" | "short", p: Palette): string {
  return side === "short" ? p.shortFill : side === "buy" ? p.buyFill : p.sellFill;
}
