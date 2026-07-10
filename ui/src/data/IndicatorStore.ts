import { PaintStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

export interface IndicatorPoint { timeMs: number; value: number }

// One series per indicator instanceId (delivered as the message `key`). Snapshot
// replaces the whole series (backfill); delta appends a new point, or upserts the
// last point in place when timeMs matches (the current in-progress bar's value).
export class IndicatorStore extends PaintStore {
  private readonly byInstance = new Map<string, IndicatorPoint[]>();

  apply(m: SnapshotMsg | DeltaMsg): void {
    const id = m.key ?? "";
    if (m.kind === "snapshot") {
      const pts = (m.payload as IndicatorPoint[]).slice().sort((a, b) => a.timeMs - b.timeMs);
      this.byInstance.set(id, pts);
      this.markDirty();
      return;
    }
    const pt = m.payload as IndicatorPoint;
    const arr = this.byInstance.get(id) ?? [];
    const last = arr[arr.length - 1];
    if (last && last.timeMs === pt.timeMs) arr[arr.length - 1] = pt;
    else arr.push(pt);
    this.byInstance.set(id, arr);
    this.markDirty();
  }

  series(instanceId: string): IndicatorPoint[] {
    return this.byInstance.get(instanceId) ?? [];
  }

  // Drop a series' points — called on symbol/timeframe switch so the previous
  // symbol's data doesn't linger under the new one until its snapshot arrives.
  reset(instanceId: string): void {
    this.byInstance.delete(instanceId);
    this.markDirty();
  }
}
