import { PaintStore } from "./store";
import type { Tick, SnapshotMsg, DeltaMsg } from "../wire/contract";

// Fixed-size ring so a burst never grows unbounded; the painter reads the
// newest N each frame. Snapshot rebuilds (reconnect re-sync); delta appends.
export class TapeRing extends PaintStore {
  private readonly buf: Tick[];
  private head = 0;   // index of next write
  private count = 0;  // number of retained ticks (≤ capacity)
  private seq = 0;    // total ticks appended this generation — the newest tick's 1-based seq
  private gen = 0;    // bumped on snapshot rebuild; anchors into an old generation are invalid

  constructor(private readonly capacity = 65536) {
    super();
    this.buf = new Array<Tick>(capacity);
  }

  apply(m: SnapshotMsg | DeltaMsg): void {
    const ticks = m.payload as Tick[];
    if (m.kind === "snapshot") {
      this.head = 0;
      this.count = 0;
      this.seq = 0;
      this.gen++;
    }
    for (const t of ticks) {
      this.buf[this.head] = t;
      this.head = (this.head + 1) % this.capacity;
      if (this.count < this.capacity) this.count++;
      this.seq++;
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

  /** 1-based seq of the newest retained tick this generation; 0 when empty. */
  lastSeq(): number {
    return this.seq;
  }

  /** Seq of the oldest retained tick; lastSeq()+1 when empty (an empty range). */
  oldestSeq(): number {
    return this.seq - this.count + 1;
  }

  /** Bumped whenever a snapshot rebuilds the ring (reconnect re-sync). */
  generation(): number {
    return this.gen;
  }

  /** Tick by seq, or undefined once overwritten / never appended. */
  tickBySeq(s: number): Tick | undefined {
    if (s < this.oldestSeq() || s > this.seq) return undefined;
    return this.at(s - this.oldestSeq());
  }
}
