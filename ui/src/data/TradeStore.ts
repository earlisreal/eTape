import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg, ClosedTradeRow } from "../wire/contract";

// Closed round trips are append-only events (not a replaceable snapshot): a
// reconnect re-snapshot MERGES + dedupes rather than wiping backfilled trades.
// seq is a single monotonically-increasing counter across the whole
// engine-side aggregator (RoundTripAggregator), so a plain Set<number> of seen
// seq values is sufficient to dedup — no composite key needed (unlike
// FillStore, whose Fill payloads have no such id).
interface TradeState {
  trades: ClosedTradeRow[]; // kept sorted by closeMs ascending
}

export class TradeStore extends ReactStore<TradeState> {
  private readonly seen = new Set<number>();

  constructor() { super({ trades: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    this.ingest(m.kind === "snapshot" ? (m.payload as ClosedTradeRow[]) : [m.payload as ClosedTradeRow]);
  }

  ingest(rows: ClosedTradeRow[]): void {
    let changed = false;
    const trades = [...this.getSnapshot().trades];
    for (const row of rows) {
      if (this.seen.has(row.seq)) continue;
      this.seen.add(row.seq);
      trades.push(row);
      changed = true;
    }
    if (!changed) return;
    trades.sort((a, b) => a.closeMs - b.closeMs);
    this.set({ trades });
  }

  trades(): ClosedTradeRow[] { return this.getSnapshot().trades; }

  // Sum of realized P&L across all currently-held trades. The engine only
  // ever emits/seeds today's round trips (Tasks 2-3), so no date filtering
  // is needed here — the engine is the source of truth for scope.
  dayRealized(): number {
    return this.getSnapshot().trades.reduce((sum, t) => sum + t.realized, 0);
  }
}
