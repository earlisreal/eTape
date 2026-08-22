import { ReactStore } from "./store";
import type { DeltaMsg, SnapshotMsg, WatchlistRow, WatchlistRowsPayload } from "../wire/contract";

export interface WatchlistState {
  symbols: string[];
  rows: Map<string, WatchlistRow>;
  refreshedAt: string | null;
  revision: number;
}

const EMPTY: WatchlistState = { symbols: [], rows: new Map(), refreshedAt: null, revision: 0 };

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
    if ((p.revision ?? 0) < this.getSnapshot().revision) return;
    const symbols = p.symbols ?? [];
    const rows = new Map<string, WatchlistRow>();
    for (const r of p.rows ?? []) rows.set(r.symbol, r);
    this.membership = new Set(symbols);
    this.set({ symbols, rows, refreshedAt: p.refreshedAt ?? null, revision: p.revision ?? 0 });
  }

  applyMutation(result: { symbols?: string[]; revision: number }): void {
    if (result.revision < this.getSnapshot().revision || !result.symbols) return;
    const symbols = [...result.symbols];
    const previous = this.getSnapshot();
    const rows = new Map<string, WatchlistRow>();
    for (const symbol of symbols) {
      const row = previous.rows.get(symbol);
      if (row) rows.set(symbol, row);
    }
    this.membership = new Set(symbols);
    this.set({ ...previous, symbols, rows, revision: result.revision });
  }

  has(symbol: string): boolean {
    return this.membership.has(symbol);
  }
}
