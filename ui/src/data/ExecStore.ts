import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

interface ExecState {
  account: Record<string, unknown> | null;
  positions: unknown[];
  orders: unknown[];
}

// Minimal in Plan 1 — Plan 5 replaces the payload shapes with typed Order/Position/
// Account and adds keyed upserts + the 9-state order lifecycle.
export class ExecStore extends ReactStore<ExecState> {
  constructor() { super({ account: null, positions: [], orders: [] }); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    switch (m.topic) {
      case "exec.account":
        this.set({ ...cur, account: m.payload as Record<string, unknown> });
        return;
      case "exec.positions":
        this.set({ ...cur, positions: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.positions, m.payload] });
        return;
      case "exec.orders":
        this.set({ ...cur, orders: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.orders, m.payload] });
        return;
      default:
        return;
    }
  }
}
