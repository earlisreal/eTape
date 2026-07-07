import { PaintStore } from "../../../data/store";
import type { Drawing } from "./model";

// Symbol-keyed store of chart drawings, shared across every panel in a window
// (one instance from makeStores()). Extends PaintStore so N chart panels each
// track their own last-seen revision via getRev() without starving each other.
//
// Primary index is byId (so remove(id) is O(1) and can find the owning symbol);
// bySymbol tracks id membership + insertion order per symbol.
export class DrawingStore extends PaintStore {
  private readonly byId = new Map<string, Drawing>();
  private readonly bySymbol = new Map<string, Set<string>>();

  forSymbol(symbol: string): Drawing[] {
    const ids = this.bySymbol.get(symbol);
    if (!ids) return [];
    const out: Drawing[] = [];
    for (const id of ids) {
      const d = this.byId.get(id);
      if (d) out.push(d);
    }
    return out;
  }

  upsert(d: Drawing): void {
    this.setLocal(d);
  }

  remove(id: string): void {
    if (this.deleteLocal(id)) this.markDirty();
  }

  clearSymbol(symbol: string): void {
    this.clearLocal(symbol);
    this.markDirty();
  }

  // --- internal mutation primitives (Task 4 reuses these for remote apply) ---

  protected setLocal(d: Drawing): void {
    this.byId.set(d.id, d);
    let ids = this.bySymbol.get(d.symbol);
    if (!ids) {
      ids = new Set<string>();
      this.bySymbol.set(d.symbol, ids);
    }
    ids.add(d.id);
    this.markDirty();
  }

  protected deleteLocal(id: string): boolean {
    const d = this.byId.get(id);
    if (!d) return false;
    this.byId.delete(id);
    this.bySymbol.get(d.symbol)?.delete(id);
    return true;
  }

  protected clearLocal(symbol: string): void {
    const ids = this.bySymbol.get(symbol);
    if (!ids) return;
    for (const id of ids) this.byId.delete(id);
    this.bySymbol.delete(symbol);
  }
}
