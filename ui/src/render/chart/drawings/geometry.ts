import type { DrawingKind } from "./model";
import type { Timeframe } from "../barBucket";

export type Px = { x: number; y: number };
export type Hit = { type: "handle"; index: number } | { type: "body" } | null;

// Nominal per-bar time step, used only to EXTRAPOLATE beyond loaded data
// (interpolation between two loaded bars never needs it). D/W/M are coarse
// approximations — acceptable because the trading workspace is 1m + 10s.
export function timeframeToMs(tf: Timeframe): number {
  switch (tf) {
    case "10s": return 10_000;
    case "1m": return 60_000;
    case "5m": return 300_000;
    case "15m": return 900_000;
    case "30m": return 1_800_000;
    case "60m": return 3_600_000;
    case "D": return 86_400_000;
    case "W": return 604_800_000;
    case "M": return 2_592_000_000;
  }
}

// Map an anchor's epoch-ms to a fractional LWC logical index on THIS chart's
// bar array. bar[i] sits at logical i; between bars we interpolate; beyond the
// ends we extrapolate by timeframeMs (rays keep pointing into the future; an
// anchor before loaded history keeps the line's true slope). barsMs must be
// ascending. Returns 0 for an empty array (primitive skips drawing with no bars).
export function timeToLogical(timeMs: number, barsMs: readonly number[], timeframeMs: number): number {
  const n = barsMs.length;
  if (n === 0) return 0;
  const first = barsMs[0];
  const last = barsMs[n - 1];
  if (timeMs <= first) return (timeMs - first) / timeframeMs;
  if (timeMs >= last) return (n - 1) + (timeMs - last) / timeframeMs;
  // largest i with barsMs[i] <= timeMs
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (barsMs[mid] <= timeMs) lo = mid;
    else hi = mid - 1;
  }
  const span = barsMs[lo + 1] - barsMs[lo];
  const frac = span > 0 ? (timeMs - barsMs[lo]) / span : 0;
  return lo + frac;
}

export function distToSegment(px: number, py: number, ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax;
  const dy = by - ay;
  const len2 = dx * dx + dy * dy;
  if (len2 === 0) return Math.hypot(px - ax, py - ay);
  let t = ((px - ax) * dx + (py - ay) * dy) / len2;
  t = Math.max(0, Math.min(1, t));
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
}

// Extend the ray p0→p1 to the viewport edge in its own x-direction. Vertical
// rays extend far along y. Used by both hit-testing and the renderer.
export function extendToEdge(p0: Px, p1: Px, width: number): Px {
  const dx = p1.x - p0.x;
  const dy = p1.y - p0.y;
  if (dx === 0) return { x: p1.x, y: p1.y + (dy >= 0 ? 1 : -1) * 1e6 };
  const targetX = dx > 0 ? width : 0;
  const t = (targetX - p0.x) / dx;
  return { x: targetX, y: p0.y + t * dy };
}

// Magnet: level within tolPx of the cursor with max distance, else null.
// Tie-break by highest price.
export function snapToLevels(cursorY: number, levels: readonly { price: number; y: number }[], tolPx: number): number | null {
  let bestPrice: number | null = null;
  let bestDist = -Infinity;
  for (const l of levels) {
    const d = Math.abs(cursorY - l.y);
    if (d <= tolPx) {
      if (d > bestDist || (d === bestDist && (bestPrice === null || l.price > bestPrice))) {
        bestDist = d;
        bestPrice = l.price;
      }
    }
  }
  return bestPrice;
}

// Pixel-space hit test. `pts` are the projected pixel positions of the drawing's
// anchors (null = off-screen). Handles win over the body. `width` is the pane
// width for horizontal/ray extension.
export function hitTest(kind: DrawingKind, pts: readonly (Px | null)[], cursor: Px, width: number, seg = 5, handle = 6): Hit {
  for (let i = 0; i < pts.length; i++) {
    const p = pts[i];
    if (p && Math.hypot(cursor.x - p.x, cursor.y - p.y) <= handle) return { type: "handle", index: i };
  }
  const p0 = pts[0];
  if (!p0) return null;
  switch (kind) {
    case "hline":
      return Math.abs(cursor.y - p0.y) <= seg ? { type: "body" } : null;
    case "hray":
      return Math.abs(cursor.y - p0.y) <= seg && cursor.x >= p0.x - seg ? { type: "body" } : null;
    case "trendline": {
      const p1 = pts[1];
      return p1 && distToSegment(cursor.x, cursor.y, p0.x, p0.y, p1.x, p1.y) <= seg ? { type: "body" } : null;
    }
    case "ray": {
      const p1 = pts[1];
      if (!p1) return null;
      const far = extendToEdge(p0, p1, width);
      return distToSegment(cursor.x, cursor.y, p0.x, p0.y, far.x, far.y) <= seg ? { type: "body" } : null;
    }
    case "rect": {
      const p1 = pts[1];
      if (!p1) return null;
      const edges: [number, number, number, number][] = [
        [p0.x, p0.y, p1.x, p0.y],
        [p1.x, p0.y, p1.x, p1.y],
        [p1.x, p1.y, p0.x, p1.y],
        [p0.x, p1.y, p0.x, p0.y],
      ];
      for (const [ax, ay, bx, by] of edges) {
        if (distToSegment(cursor.x, cursor.y, ax, ay, bx, by) <= seg) return { type: "body" };
      }
      return null;
    }
  }
}
