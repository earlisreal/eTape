import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

// Plan 4 adds NewsItem typing, seen-time labeling, per-symbol filtering, dedup.
export class NewsStore extends ReactStore<{ items: unknown[] }> {
  constructor() { super({ items: [] }); }
  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    this.set({ items: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.items, m.payload] });
  }
}
