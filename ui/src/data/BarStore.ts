import { PaintStore } from "./store";
import type { Bar, SnapshotMsg, DeltaMsg } from "../wire/contract";

// One ordered series per (symbol, timeframe). The last bar may be in-progress
// (updates in place on every push); it finalizes only when a bar with the same
// bucketStart arrives with inProgress:false, or a later bucket appears. Quiet
// symbols may hold a partial past its wall-clock end — never a "closed" bar.
export class BarStore extends PaintStore {
  private readonly series_ = new Map<string, Bar[]>();
  // Bumped per (symbol, timeframe) on every apply() for that key — backs the
  // key-scoped getRev(symbol, timeframe) overload below. The base PaintStore's
  // own rev counter (bumped via markDirty()) still backs the no-arg getRev()
  // global fallback, unchanged.
  private readonly revs = new Map<string, number>();

  private key(symbol: string, timeframe: string): string { return `${symbol}:${timeframe}`; }

  private bumpRev(k: string): void { this.revs.set(k, (this.revs.get(k) ?? 0) + 1); }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.kind === "snapshot") {
      const bars = (m.payload as Bar[]).slice().sort((a, b) => a.bucketStart.localeCompare(b.bucketStart));
      if (bars.length > 0) {
        const k = this.key(bars[0].symbol, bars[0].timeframe);
        this.series_.set(k, bars);
        this.bumpRev(k);
      }
      this.markDirty();
      return;
    }
    const b = m.payload as Bar;
    const k = this.key(b.symbol, b.timeframe);
    const arr = this.series_.get(k) ?? [];
    const last = arr[arr.length - 1];
    if (!last || b.bucketStart > last.bucketStart) {
      arr.push(b); // common case: new bucket at the tail (or first bar ever)
    } else if (b.bucketStart === last.bucketStart) {
      arr[arr.length - 1] = b; // upsert in place (in-progress update or finalize)
    } else {
      // Out-of-order delta (reconnect replay, a late tick belonging to an earlier
      // bucket): keep the series sorted so a downstream consumer (e.g. the chart
      // controller feeding Lightweight Charts, which throws on a non-monotonic
      // time) never sees bucketStart go backwards.
      const idx = arr.findIndex((x) => x.bucketStart === b.bucketStart);
      if (idx >= 0) {
        arr[idx] = b; // late revision to an already-recorded earlier bucket
      } else {
        let i = arr.length - 1;
        while (i >= 0 && arr[i].bucketStart > b.bucketStart) i--;
        arr.splice(i + 1, 0, b);
      }
    }
    this.series_.set(k, arr);
    this.bumpRev(k);
    this.markDirty();
  }

  series(symbol: string, timeframe: string): Bar[] {
    return this.series_.get(this.key(symbol, timeframe)) ?? [];
  }

  inProgressBar(symbol: string, timeframe: string): Bar | undefined {
    const arr = this.series_.get(this.key(symbol, timeframe));
    const last = arr?.[arr.length - 1];
    return last?.inProgress ? last : undefined;
  }

  /**
   * Per-(symbol, timeframe) revision when both are given (starts at 0,
   * increments by 1 on each apply() for that key). Omitting either falls
   * back to the existing global PaintStore revision — back-compat for
   * callers not yet migrated to key-scoped reads.
   */
  getRev(symbol?: string, timeframe?: string): number {
    if (symbol === undefined || timeframe === undefined) return super.getRev();
    return this.revs.get(this.key(symbol, timeframe)) ?? 0;
  }
}
