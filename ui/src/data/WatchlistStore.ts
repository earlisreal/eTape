import { ReactStore } from "./store";
import type { DeltaMsg, SnapshotMsg, WatchlistRow, WatchlistRowsPayload } from "../wire/contract";

export interface WatchlistState {
  symbols: string[];
  rows: Map<string, WatchlistRow>;
  refreshedAt: string | null;
}

const EMPTY: WatchlistState = { symbols: [], rows: new Map(), refreshedAt: null };

// WatchlistStore holds the single global watchlist snapshot. Deliberately none
// of ScannerStore's flash/mute/seen machinery — a user-curated stable list has
// no "new hit" churn event.
export class WatchlistStore extends ReactStore<WatchlistState> {
  private membership = new Set<string>();

  constructor() {
    super(EMPTY);
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const p = m.payload as WatchlistRowsPayload;
    const symbols = p.symbols ?? [];
    const rows = new Map<string, WatchlistRow>();
    for (const r of p.rows ?? []) rows.set(r.symbol, r);
    this.membership = new Set(symbols);
    this.set({ symbols, rows, refreshedAt: p.refreshedAt ?? null });
  }

  has(symbol: string): boolean {
    return this.membership.has(symbol);
  }
}
