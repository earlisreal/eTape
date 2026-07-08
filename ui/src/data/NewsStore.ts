import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, NewsItem } from "../wire/contract";

interface NewsState { items: NewsItem[] }

// Broker-agnostic news feed. Snapshot replaces (and rebuilds the dedup set);
// delta appends one item or an array. Dedup by url (fallback: symbol|headline|
// seen_at). itemsFor(symbol) returns that symbol's items newest-first by seen_at.
export class NewsStore extends ReactStore<NewsState> {
  private readonly seenKeys = new Set<string>();
  constructor(private readonly cap = 500) { super({ items: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.kind === "snapshot") {
      this.seenKeys.clear();
      this.set({ items: this.dedupe(this.asArray(m.payload)).slice(-this.cap) });
      return;
    }
    const fresh = this.dedupe(this.asArray(m.payload));
    if (fresh.length === 0) return;
    this.set({ items: [...this.getSnapshot().items, ...fresh].slice(-this.cap) });
  }

  itemsFor(symbol: string): NewsItem[] {
    return this.getSnapshot().items
      .filter((it) => it.symbol === symbol)
      .sort((a, b) => b.seen_at.localeCompare(a.seen_at)); // ISO strings sort chronologically
  }

  // Coerce a payload to an item array, dropping null/non-object entries. An empty
  // news snapshot arrives as `payload: null` (a nil Go slice marshals to JSON null),
  // and a malformed message could carry null entries — neither must reach keyOf().
  private asArray(p: unknown): NewsItem[] {
    const raw = Array.isArray(p) ? p : p == null ? [] : [p];
    return raw.filter((it): it is NewsItem => it != null && typeof it === "object");
  }
  private keyOf(it: NewsItem): string { return it.url || `${it.symbol}|${it.headline}|${it.seen_at}`; }
  private dedupe(items: NewsItem[]): NewsItem[] {
    const out: NewsItem[] = [];
    for (const it of items) {
      const k = this.keyOf(it);
      if (this.seenKeys.has(k)) continue;
      this.seenKeys.add(k);
      out.push(it);
    }
    return out;
  }
}
