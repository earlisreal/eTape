import { ReactStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

// Plan 4 adds session parameterization, dedup + new-hit flash, threshold config.
export class ScannerStore extends ReactStore<{ rows: unknown[] }> {
  constructor() { super({ rows: [] }); }
  apply(m: SnapshotMsg | DeltaMsg): void {
    const cur = this.getSnapshot();
    this.set({ rows: m.kind === "snapshot" ? (m.payload as unknown[]) : [...cur.rows, m.payload] });
  }
}
