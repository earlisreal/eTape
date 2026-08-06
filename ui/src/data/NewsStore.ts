import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, NewsItem } from "../wire/contract";

interface NewsState { items: NewsItem[] }

// Article deltas are ID upserts. Snapshots replace state; malformed legacy
// payloads get a defensive URL/headline key until every reconnect is current.
export class NewsStore extends ReactStore<NewsState> {
  constructor(private readonly cap = 500) { super({ items: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const incoming = this.asArray(m.payload);
    if (m.kind === "snapshot") {
      this.set({ items: this.upsert([], incoming) });
      return;
    }
    const items = this.upsert(this.getSnapshot().items, incoming);
    if (items !== this.getSnapshot().items) this.set({ items });
  }

  itemsFor(symbol: string): NewsItem[] {
    return this.getSnapshot().items
      .filter((it) => it.symbols?.includes(symbol))
      .slice()
      .sort((a, b) => this.compareNewest(a, b));
  }

  private asArray(p: unknown): NewsItem[] {
    const raw = Array.isArray(p) ? p : p == null ? [] : [p];
    return raw.filter((it): it is NewsItem => it != null && typeof it === "object");
  }

  private keyOf(it: NewsItem): string { return it.id || it.url || `${it.headline}|${it.seen_at}`; }

  private upsert(existing: NewsItem[], incoming: NewsItem[]): NewsItem[] {
    if (incoming.length === 0) return existing;
    const items = existing.slice();
    const index = new Map(items.map((it, i) => [this.keyOf(it), i]));
    for (const item of incoming) {
      const key = this.keyOf(item); const at = index.get(key);
      if (at == null) { index.set(key, items.length); items.push(item); } else items[at] = item;
    }
    return items.slice(-this.cap);
  }

  private compareNewest(a: NewsItem, b: NewsItem): number {
    const aKnown = a.published_precision !== "unknown" && !!a.published_at;
    const bKnown = b.published_precision !== "unknown" && !!b.published_at;
    if (aKnown && bKnown) return b.published_at.localeCompare(a.published_at);
    if (aKnown !== bKnown) return aKnown ? -1 : 1;
    return b.seen_at.localeCompare(a.seen_at);
  }
}
