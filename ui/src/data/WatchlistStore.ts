import { ReactStore } from "./store";
import type { DeltaMsg, SnapshotMsg, WatchlistRow, WatchlistRowsPayload } from "../wire/contract";

export interface WatchlistState {
  symbols: string[];
  received: boolean;
  rows: Map<string, WatchlistRow>;
  refreshedAt: string | null;
}

const EMPTY: WatchlistState = { symbols: [], received: false, rows: new Map(), refreshedAt: null };

// WatchlistStore holds the single global watchlist snapshot. Deliberately none
// of ScannerStore's flash/mute/seen machinery — a user-curated stable list has
// no "new hit" churn event.
export class WatchlistStore extends ReactStore<WatchlistState> {
  private membership = new Set<string>();

  constructor() {
    super(EMPTY);
  }

  disconnect(): void {
    const snapshot = this.getSnapshot();
    if (snapshot.received) this.set({ ...snapshot, received: false });
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const p = m.payload as WatchlistRowsPayload;
    const previous = this.getSnapshot().symbols;
    const incoming = p.symbols ?? [];
    const symbols = incoming.length === previous.length && incoming.every((symbol, index) => symbol === previous[index])
      ? previous : incoming;
    const rows = new Map<string, WatchlistRow>();
    for (const r of p.rows ?? []) rows.set(r.symbol, r);
    this.membership = new Set(symbols);
    this.set({ symbols, received: Array.isArray(p.symbols), rows, refreshedAt: p.refreshedAt ?? null });
  }

  has(symbol: string): boolean {
    return this.membership.has(symbol);
  }
}
