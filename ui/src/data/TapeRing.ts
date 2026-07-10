import { PaintStore } from "./store";
import type { Tick, SnapshotMsg, DeltaMsg } from "../wire/contract";
import type { TapeSource } from "../render/tape/tapeState";

/**
 * Fixed-size ring for ONE symbol's ticks so a burst never grows unbounded;
 * painters read the newest N each frame. Snapshot rebuilds (reconnect
 * re-sync); delta appends. Extracted out of the old single-ring TapeRing
 * (Task 1) — TapeRing is now a Map<string, SymbolTapeRing> collection (below)
 * so one symbol's reconnect snapshot can no longer wipe another symbol's data.
 */
export class SymbolTapeRing {
  private readonly buf: Tick[];
  private head = 0;   // index of next write
  private count = 0;  // number of retained ticks (≤ capacity)
  private seq = 0;    // total ticks appended this generation — the newest tick's 1-based seq
  private gen = 0;    // bumped on snapshot rebuild; anchors into an old generation are invalid
  private rev = 0;    // bumped on every apply (own revision — see TapeRing.getRev(symbol))

  constructor(private readonly capacity: number) {
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
    this.rev++;
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

  /** Bumped on every apply() — this symbol's own revision counter. */
  getRev(): number { return this.rev; }

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

// Shared by every unknown-symbol lookup — source() returns this same instance
// rather than allocating a fresh object per call.
const EMPTY_SOURCE: TapeSource = {
  lastSeq: () => 0,
  oldestSeq: () => 1,
  generation: () => 0,
  tickBySeq: () => undefined,
};

// Per-symbol cap: generous local scrollback (the engine only retains 200
// ticks/symbol server-side) without pre-allocating a 65536-slot ring per
// symbol in a workspace subscribed to dozens of them.
const DEFAULT_SYMBOL_CAPACITY = 4096;

/**
 * Collection of per-symbol tape rings. md.tape delivers ticks for every
 * subscribed symbol to one WS client; before this a single global ring
 * (capacity 65536) held all of them, so symbol-pinned panels had to scan the
 * whole ring every frame, and a reconnect's per-symbol snapshot frames each
 * wiped the *entire* ring — with N symbols retained, only the last symbol's
 * snapshot survived. Keying by symbol fixes both: each symbol gets its own
 * bounded ring, generation, and revision, so a snapshot for one symbol never
 * touches another's.
 *
 * Still extends PaintStore: markDirty()/isDirty()/consumeDirty() cover the
 * scheduler's single dirty flag for "something changed, repaint," and the
 * base class's own rev counter backs the no-arg getRev() global fallback
 * below (existing callers that haven't been migrated to symbol-scoped reads
 * yet — see getRev()'s doc comment).
 */
export class TapeRing extends PaintStore {
  private readonly rings = new Map<string, SymbolTapeRing>();

  constructor(private readonly capacity = DEFAULT_SYMBOL_CAPACITY) {
    super();
  }

  /**
   * Routes by payload[0].symbol — the engine batches md.tape frames per
   * symbol, so a frame's ticks are always homogeneous for one symbol. An
   * empty payload carries no symbol to route by, so it is dropped (nothing
   * to store). A snapshot only resets *this* symbol's ring/generation.
   */
  apply(m: SnapshotMsg | DeltaMsg): void {
    const ticks = m.payload as Tick[];
    if (ticks.length === 0) return;
    const symbol = ticks[0].symbol;
    let ring = this.rings.get(symbol);
    if (!ring) {
      ring = new SymbolTapeRing(this.capacity);
      this.rings.set(symbol, ring);
    }
    ring.apply(m);
    this.markDirty();
  }

  /**
   * Per-symbol revision when `symbol` is given. The no-arg form is a global
   * revision (via the base PaintStore's own rev counter, bumped by
   * markDirty() above on every apply regardless of symbol) — back-compat for
   * callers not yet migrated to symbol-scoped reads (TapePanel/LadderPanel,
   * as of this writing; see Tasks 3/4).
   */
  getRev(symbol?: string): number {
    if (symbol === undefined) return super.getRev();
    return this.rings.get(symbol)?.getRev() ?? 0;
  }

  /** Per-symbol generation; 0 if the symbol has never been seen. */
  generation(symbol: string): number {
    return this.rings.get(symbol)?.generation() ?? 0;
  }

  /** TapeSource scoped to one symbol; an unknown symbol gets an empty source. */
  source(symbol: string): TapeSource {
    return this.rings.get(symbol) ?? EMPTY_SOURCE;
  }

  /** O(1) most recent tick appended for `symbol`, or undefined if none. */
  lastTick(symbol: string): Tick | undefined {
    const ring = this.rings.get(symbol);
    if (!ring || ring.size() === 0) return undefined;
    return ring.at(ring.size() - 1);
  }
}
