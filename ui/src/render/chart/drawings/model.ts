// Drawing data model + load-time validation. Pure — no LWC, no DOM.

export type DrawingKind = "hline" | "hray" | "trendline" | "ray" | "rect";

export interface Anchor {
  timeMs: number; // epoch ms (a bar's time on the chart it was drawn)
  price: number;  // raw price
}

export interface Drawing {
  id: string;       // crypto.randomUUID()
  symbol: string;   // "US.AAPL"
  kind: DrawingKind;
  anchors: Anchor[]; // hline/hray: 1, trendline/ray/rect: 2
  createdMs: number;
  updatedMs: number;
}

const KINDS: ReadonlySet<string> = new Set<DrawingKind>(["hline", "hray", "trendline", "ray", "rect"]);

export function anchorCount(kind: DrawingKind): 1 | 2 {
  return kind === "hline" || kind === "hray" ? 1 : 2;
}

function isFiniteNumber(x: unknown): x is number {
  return typeof x === "number" && Number.isFinite(x);
}

function isAnchor(x: unknown): x is Anchor {
  return typeof x === "object" && x !== null
    && isFiniteNumber((x as Anchor).timeMs) && isFiniteNumber((x as Anchor).price);
}

export function isValidDrawing(x: unknown): x is Drawing {
  if (typeof x !== "object" || x === null) return false;
  const d = x as Record<string, unknown>;
  if (typeof d.id !== "string" || typeof d.symbol !== "string") return false;
  if (typeof d.kind !== "string" || !KINDS.has(d.kind)) return false;
  if (!isFiniteNumber(d.createdMs) || !isFiniteNumber(d.updatedMs)) return false;
  if (!Array.isArray(d.anchors)) return false;
  if (d.anchors.length !== anchorCount(d.kind as DrawingKind)) return false;
  return d.anchors.every(isAnchor);
}

// Load-time gate: drops malformed entries so a corrupt config never crashes a chart.
export function validateDrawings(raw: unknown): Drawing[] {
  if (!Array.isArray(raw)) return [];
  const out = raw.filter(isValidDrawing);
  const dropped = raw.length - out.length;
  if (dropped > 0) console.warn(`[drawings] dropped ${dropped} malformed drawing(s) on load`);
  return out;
}
