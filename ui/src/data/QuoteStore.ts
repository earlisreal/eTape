import { PaintStore } from "./store";
import type { Quote, SnapshotMsg, DeltaMsg } from "../wire/contract";

export class QuoteStore extends PaintStore {
  private readonly quotes = new Map<string, Quote>();

  apply(m: SnapshotMsg | DeltaMsg): void {
    const p = m.payload as Partial<Quote> & { symbol: string };
    const prev = this.quotes.get(p.symbol);
    // snapshot replaces; delta merges onto the prior quote for that symbol.
    const next: Quote = m.kind === "snapshot"
      ? (p as Quote)
      : { ...(prev ?? { symbol: p.symbol, bid: 0, ask: 0, last: 0, ts: "" }), ...p };
    this.quotes.set(p.symbol, next);
    this.markDirty();
  }

  get(symbol: string): Quote | undefined { return this.quotes.get(symbol); }
}
