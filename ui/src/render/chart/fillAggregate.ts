import { bucketStartMs, type Timeframe } from "./barBucket";
import type { FillMarker } from "./diamondMarker";

// Structural input (not imported from data/FillStore) so render/ never imports
// data/ — FillStore.ts documents the inverse rule (data/ never imports render/);
// this keeps the boundary one-directional from both sides.
export interface FillSlice {
  venue: string;
  orderId: string;
  timeMs: number;
  price: number;
  qty: number;
  side: "buy" | "sell";
}

// Root cause of "marker sometimes missing": LWC's timeToCoordinate returns null
// for any time that isn't an exact bar time (diamondPrimitive.ts culls those
// silently). Bucketing every fill to its bar's start — via the same
// session-anchored bucketing the engine uses for bars — guarantees the marker's
// time always lands on a real bar.
//
// Also aggregates: multiple partial fills of the SAME order landing in the same
// bar collapse into one marker, placed at the order's quantity-weighted average
// price (Σ price·qty / Σ qty — the order's true average fill price, same figure
// a broker reports; not the market VWAP indicator). A different order, or the
// same order's opposite side, never merges with this group.
export function aggregateFillMarkers(fills: FillSlice[], tf: Timeframe): FillMarker[] {
  const groups = new Map<string, { timeMs: number; side: "buy" | "sell"; notional: number; qty: number }>();
  for (const f of fills) {
    const bucket = bucketStartMs(f.timeMs, tf);
    const k = `${f.venue}|${f.orderId}|${bucket}|${f.side}`;
    const g = groups.get(k);
    if (g) { g.notional += f.price * f.qty; g.qty += f.qty; }
    else groups.set(k, { timeMs: bucket, side: f.side, notional: f.price * f.qty, qty: f.qty });
  }
  const out: FillMarker[] = [];
  for (const g of groups.values()) out.push({ timeMs: g.timeMs, price: g.qty > 0 ? g.notional / g.qty : 0, side: g.side });
  out.sort((a, b) => a.timeMs - b.timeMs);
  return out;
}
