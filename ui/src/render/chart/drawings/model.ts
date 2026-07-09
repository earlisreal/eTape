// Drawing data model + load-time validation. Pure — no LWC, no DOM.

import type { LineStyleName } from "../lineStyle";
import { LINE_STYLE_NAMES } from "../lineStyle";

export type DrawingKind = "hline" | "trendline" | "extendedline" | "rect";

export interface Anchor {
  timeMs: number; // epoch ms (a bar's time on the chart it was drawn)
  price: number;  // raw price
}

export interface Drawing {
  id: string;       // crypto.randomUUID()
  symbol: string;   // "US.AAPL"
  kind: DrawingKind;
  anchors: Anchor[]; // hline: 1, trendline/extendedline/rect: 2
  createdMs: number;
  updatedMs: number;
  // Optional per-drawing style overrides (TV floating toolbar). Absent = palette default.
  color?: string;
  width?: number;
  lineStyle?: LineStyleName;
}

export const DEFAULT_DRAWING_WIDTH = 1;
export const DEFAULT_LINE_STYLE: LineStyleName = "solid";

const KINDS: ReadonlySet<string> = new Set<DrawingKind>(["hline", "trendline", "extendedline", "rect"]);

export function anchorCount(kind: DrawingKind): 1 | 2 {
  return kind === "hline" ? 1 : 2;
}

function isFiniteNumber(x: unknown): x is number {
  return typeof x === "number" && Number.isFinite(x);
}

function isAnchor(x: unknown): x is Anchor {
  return typeof x === "object" && x !== null
    && isFiniteNumber((x as Anchor).timeMs) && isFiniteNumber((x as Anchor).price);
}

// Exported for reuse by DrawingToolStyleStore (toolStyles.ts), which validates
// the same three optional fields on a remembered per-tool style loaded from config.
export function isValidDrawingStyle(d: Record<string, unknown>): boolean {
  if (d.color !== undefined && typeof d.color !== "string") return false;
  if (d.width !== undefined && !isFiniteNumber(d.width)) return false;
  if (d.lineStyle !== undefined && !LINE_STYLE_NAMES.includes(d.lineStyle as LineStyleName)) return false;
  return true;
}

export function isValidDrawing(x: unknown): x is Drawing {
  if (typeof x !== "object" || x === null) return false;
  const d = x as Record<string, unknown>;
  if (typeof d.id !== "string" || typeof d.symbol !== "string") return false;
  if (typeof d.kind !== "string" || !KINDS.has(d.kind)) return false;
  if (!isFiniteNumber(d.createdMs) || !isFiniteNumber(d.updatedMs)) return false;
  if (!Array.isArray(d.anchors)) return false;
  if (d.anchors.length !== anchorCount(d.kind as DrawingKind)) return false;
  return d.anchors.every(isAnchor) && isValidDrawingStyle(d);
}

// Load-time gate: drops malformed entries so a corrupt config never crashes a chart.
export function validateDrawings(raw: unknown): Drawing[] {
  if (!Array.isArray(raw)) return [];
  const out = raw.filter(isValidDrawing);
  const dropped = raw.length - out.length;
  if (dropped > 0) console.warn(`[drawings] dropped ${dropped} malformed drawing(s) on load`);
  return out;
}
