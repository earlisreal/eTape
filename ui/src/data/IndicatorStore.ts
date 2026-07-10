import { PaintStore } from "./store";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

export interface IndicatorPoint { timeMs: number; value: number }

// One series per indicator instanceId (delivered as the message `key`). Snapshot
// replaces the whole series (backfill); delta appends a new point, or upserts the
// last point in place when timeMs matches (the current in-progress bar's value).
export class IndicatorStore extends PaintStore {
  private readonly byInstance = new Map<string, IndicatorPoint[]>();
  // Bumped per instanceId on every apply()/reset() for that id — backs the
  // key-scoped getRev(instanceId) overload below. The base PaintStore's own
  // rev counter (bumped via markDirty()) still backs the no-arg getRev()
  // global fallback, unchanged.
  private readonly revs = new Map<string, number>();

  private bumpRev(id: string): void { this.revs.set(id, (this.revs.get(id) ?? 0) + 1); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const id = m.key ?? "";
    if (m.kind === "snapshot") {
      const pts = (m.payload as IndicatorPoint[]).slice().sort((a, b) => a.timeMs - b.timeMs);
      this.byInstance.set(id, pts);
      this.bumpRev(id);
      this.markDirty();
      return;
    }
    const pt = m.payload as IndicatorPoint;
    const arr = this.byInstance.get(id) ?? [];
    const last = arr[arr.length - 1];
    if (last && last.timeMs === pt.timeMs) arr[arr.length - 1] = pt;
    else arr.push(pt);
    this.byInstance.set(id, arr);
    this.bumpRev(id);
    this.markDirty();
  }

  series(instanceId: string): IndicatorPoint[] {
    return this.byInstance.get(instanceId) ?? [];
  }

  // Drop a series' points — called on symbol/timeframe switch so the previous
  // symbol's data doesn't linger under the new one until its snapshot arrives.
  // Also bumps the instance's rev: an indicator disappearing is a real visual
  // change a per-instance consumer must see, not just new/updated points.
  reset(instanceId: string): void {
    this.byInstance.delete(instanceId);
    this.bumpRev(instanceId);
    this.markDirty();
  }

  /**
   * Per-instanceId revision when given (starts at 0, increments by 1 on each
   * apply()/reset() for that id). Omitting it falls back to the existing
   * global PaintStore revision — back-compat for callers not yet migrated to
   * key-scoped reads.
   */
  getRev(instanceId?: string): number {
    if (instanceId === undefined) return super.getRev();
    return this.revs.get(instanceId) ?? 0;
  }
}
