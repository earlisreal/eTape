import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, StockDetailPayload } from "../wire/contract";

interface StockDetailState { bySymbol: Record<string, StockDetailPayload> }

// stock.detail arrives one frame per symbol, both on snapshot (the initial
// per-symbol subscribe frame) and delta (periodic refresh) — never a
// batch/array. Snapshot and delta are handled identically here: merge this
// one symbol's payload into the map, leaving every other symbol's entry
// untouched. Unlike NewsStore (where a snapshot means "here is the complete
// list, replace everything"), a stock.detail snapshot is just "here is
// symbol X's current detail" — the mirror emits one snapshot frame per
// already-known symbol, not one frame replacing the whole map. Clearing the
// map on snapshot would wipe out every other symbol's already-known data
// every time a new symbol's snapshot frame arrives.
export class StockDetailStore extends ReactStore<StockDetailState> {
  constructor() { super({ bySymbol: {} }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const payload = this.asPayload(m.payload);
    if (!payload) return;
    this.set({ bySymbol: { ...this.getSnapshot().bySymbol, [payload.symbol]: payload } });
  }

  detailFor(symbol: string): StockDetailPayload | undefined {
    return this.getSnapshot().bySymbol[symbol];
  }

  // Guards against a malformed/null payload (defensive — the engine never
  // sends stock.detail as null or an array, but a store must not throw or
  // silently corrupt state on a message that doesn't match the expected shape).
  private asPayload(p: unknown): StockDetailPayload | null {
    if (p == null || typeof p !== "object" || Array.isArray(p)) return null;
    const candidate = p as Partial<StockDetailPayload>;
    if (typeof candidate.symbol !== "string" || candidate.symbol === "") return null;
    return candidate as StockDetailPayload;
  }
}
