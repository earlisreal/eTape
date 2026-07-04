import { PaintStore } from "./store";
import type { Tick, SnapshotMsg, DeltaMsg } from "../wire/contract";

// Fixed-size ring so a burst never grows unbounded; the painter reads the
// newest N each frame. Snapshot rebuilds (reconnect re-sync); delta appends.
export class TapeRing extends PaintStore {
  private readonly buf: Tick[];
  private head = 0;   // index of next write
  private count = 0;  // number of retained ticks (≤ capacity)

  constructor(private readonly capacity = 65536) {
    super();
    this.buf = new Array<Tick>(capacity);
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const ticks = m.payload as Tick[];
    if (m.kind === "snapshot") { this.head = 0; this.count = 0; }
    for (const t of ticks) {
      this.buf[this.head] = t;
      this.head = (this.head + 1) % this.capacity;
      if (this.count < this.capacity) this.count++;
    }
    this.markDirty();
  }

  size(): number { return this.count; }

  at(i: number): Tick {
    if (i < 0 || i >= this.count) throw new RangeError(`tape index ${i} out of [0,${this.count})`);
    const start = (this.head - this.count + this.capacity) % this.capacity;
    return this.buf[(start + i) % this.capacity];
  }

  latest(n: number): Tick[] {
    const take = Math.min(n, this.count);
    const out: Tick[] = new Array(take);
    for (let k = 0; k < take; k++) out[k] = this.at(this.count - take + k);
    return out;
  }
}
