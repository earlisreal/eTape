import { PaintStore } from "./store";
import type { Bar, SnapshotMsg, DeltaMsg } from "../wire/contract";

// One ordered series per (symbol, timeframe). The last bar may be in-progress
// (updates in place on every push); it finalizes only when a bar with the same
// bucketStart arrives with inProgress:false, or a later bucket appears. Quiet
// symbols may hold a partial past its wall-clock end — never a "closed" bar.
export class BarStore extends PaintStore {
  private readonly series_ = new Map<string, Bar[]>();

  private key(symbol: string, timeframe: string): string { return `${symbol}:${timeframe}`; }

  apply(m: SnapshotMsg | DeltaMsg): void {
    if (m.kind === "snapshot") {
      const bars = (m.payload as Bar[]).slice().sort((a, b) => a.bucketStart.localeCompare(b.bucketStart));
      if (bars.length > 0) this.series_.set(this.key(bars[0].symbol, bars[0].timeframe), bars);
      this.markDirty();
      return;
    }
    const b = m.payload as Bar;
    const k = this.key(b.symbol, b.timeframe);
    const arr = this.series_.get(k) ?? [];
    const last = arr[arr.length - 1];
    if (last && last.bucketStart === b.bucketStart) {
      arr[arr.length - 1] = b; // upsert in place (in-progress update or finalize)
    } else {
      arr.push(b);             // new bucket
    }
    this.series_.set(k, arr);
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
}
