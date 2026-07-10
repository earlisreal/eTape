import { PaintStore } from "./store";
import type { Fill, SnapshotMsg, DeltaMsg } from "../wire/contract";

// A fill marker as consumed by the chart. Declared structurally here (not imported
// from render/chart/diamondMarker) so data/ never imports render/ — the shape is
// identical to render's FillMarker, so ChartController.setFills accepts it.
export interface FillPoint { timeMs: number; price: number; side: "buy" | "sell" | "short" }

// The richer per-fill shape the chart needs to bucket-and-aggregate fills by
// order (render/chart/fillAggregate.ts's FillSlice) — same reason as FillPoint
// above: declared structurally so data/ never imports render/.
export interface FillSlice { venue: string; orderId: string; timeMs: number; price: number; qty: number; side: "buy" | "sell" | "short" }

// Fills are append-only events (not a replaceable snapshot): a reconnect
// re-snapshot MERGES + dedupes rather than wiping backfilled history. Bucketed by
// symbol; forSymbol() maps to the chart's fill-marker shape.
const key = (f: Fill) => `${f.venue}|${f.orderId}|${f.tsMs}|${f.price}|${f.qty}`;

export class FillStore extends PaintStore {
  private readonly bySymbol = new Map<string, Fill[]>();
  private readonly seen = new Set<string>();
  private readonly fillListeners = new Set<(fill: Fill) => void>();

  /** Fires once per newly-ingested fill (snapshot or delta), after dedup. */
  onNewFill(cb: (fill: Fill) => void): () => void {
    this.fillListeners.add(cb);
    return () => { this.fillListeners.delete(cb); };
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    this.ingest(m.kind === "snapshot" ? (m.payload as Fill[]) : [m.payload as Fill]);
  }

  ingest(fills: Fill[]): void {
    let changed = false;
    for (const f of fills) {
      const k = key(f);
      if (this.seen.has(k)) continue;
      this.seen.add(k);
      const arr = this.bySymbol.get(f.symbol) ?? [];
      arr.push(f);
      arr.sort((a, b) => a.tsMs - b.tsMs);
      this.bySymbol.set(f.symbol, arr);
      changed = true;
      for (const cb of this.fillListeners) {
        try { cb(f); } catch { /* a listener must never break fill ingestion */ }
      }
    }
    if (changed) this.markDirty();
  }

  // Superseded by forSymbolFills (which carries venue/orderId/qty for chart-side
  // bucketing + per-order aggregation); kept only as a thin FillPoint projection
  // in case something needs the plain point shape.
  forSymbol(symbol: string): FillPoint[] {
    return this.forSymbolFills(symbol).map(({ timeMs, price, side }) => ({ timeMs, price, side }));
  }

  forSymbolFills(symbol: string): FillSlice[] {
    return (this.bySymbol.get(symbol) ?? []).map((f) => ({
      venue: f.venue, orderId: f.orderId, timeMs: f.tsMs, price: f.price, qty: f.qty,
      side: f.side === "SHORT" ? "short" : f.side === "SELL" ? "sell" : "buy",
    }));
  }
}
