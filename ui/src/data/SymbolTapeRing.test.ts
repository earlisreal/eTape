// Extracted from TapeRing.test.ts (pre-Task-1 shape): these cases cover the
// single-symbol ring's own behavior in isolation, now that TapeRing (the
// collection, see TapeRing.test.ts) is a Map<string, SymbolTapeRing> keyed
// by symbol rather than one global ring.
import { describe, it, expect } from "vitest";
import { SymbolTapeRing } from "./TapeRing";
import type { SnapshotMsg, DeltaMsg, Tick } from "../wire/contract";

const tick = (price: number): Tick => ({ symbol: "US.AAPL", price, size: 100, direction: "BUY", ts: `t${price}` });
const snap = (ticks: Tick[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.tape", key: "US.AAPL", payload: ticks });
const delta = (ticks: Tick[]): DeltaMsg => ({ kind: "delta", topic: "md.tape", key: "US.AAPL", payload: ticks });

describe("SymbolTapeRing", () => {
  it("appends batches and preserves order", () => {
    const r = new SymbolTapeRing(4);
    r.apply(snap([tick(1), tick(2)]));
    r.apply(delta([tick(3)]));
    expect(r.size()).toBe(3);
    expect(r.latest(3).map((t) => t.price)).toEqual([1, 2, 3]);
  });

  it("bumps its own rev on apply", () => {
    const r = new SymbolTapeRing(4);
    expect(r.getRev()).toBe(0);
    r.apply(delta([tick(1)]));
    expect(r.getRev()).toBe(1);
    r.apply(delta([tick(2)]));
    expect(r.getRev()).toBe(2);
  });

  it("overwrites oldest when capacity is exceeded (burst-proof)", () => {
    const r = new SymbolTapeRing(3);
    r.apply(snap([tick(1), tick(2), tick(3)]));
    r.apply(delta([tick(4), tick(5)]));  // exceeds capacity by 2
    expect(r.size()).toBe(3);
    expect(r.latest(3).map((t) => t.price)).toEqual([3, 4, 5]);
    expect(r.at(0).price).toBe(3); // index 0 = oldest retained
  });

  it("snapshot rebuilds the ring from scratch", () => {
    const r = new SymbolTapeRing(4);
    r.apply(delta([tick(1), tick(2)]));
    r.apply(snap([tick(9)]));  // reconnect re-snapshot
    expect(r.size()).toBe(1);
    expect(r.latest(1)[0].price).toBe(9);
  });

  describe("sequence tracking (Plan 3 pause anchoring)", () => {
    // Named to avoid shadowing the file's existing top-level snapshot helper.
    const seqTick = (n: number): Tick =>
      ({ symbol: "US.AAPL", price: 3.5, size: n, direction: "BUY", ts: `2026-07-06T13:30:0${n % 10}Z` });
    const seqSnap = (ticks: Tick[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.tape", payload: ticks });
    const seqDel = (ticks: Tick[]): DeltaMsg => ({ kind: "delta", topic: "md.tape", payload: ticks });

    it("numbers ticks monotonically and exposes the retained seq window", () => {
      const ring = new SymbolTapeRing(3);
      ring.apply(seqSnap([seqTick(1), seqTick(2)]));            // seqs 1, 2
      ring.apply(seqDel([seqTick(3), seqTick(4), seqTick(5)])); // seqs 3, 4, 5 — capacity 3 retains 3..5
      expect(ring.lastSeq()).toBe(5);
      expect(ring.oldestSeq()).toBe(3);
      expect(ring.tickBySeq(4)).toEqual(seqTick(4));
      expect(ring.tickBySeq(2)).toBeUndefined(); // overwritten
      expect(ring.tickBySeq(6)).toBeUndefined(); // not yet appended
    });

    it("bumps the generation and restarts seq on snapshot rebuild (reconnect)", () => {
      const ring = new SymbolTapeRing(8);
      ring.apply(seqSnap([seqTick(1), seqTick(2)]));
      const g1 = ring.generation();
      ring.apply(seqSnap([seqTick(3)]));
      expect(ring.generation()).toBe(g1 + 1);
      expect(ring.lastSeq()).toBe(1);
      expect(ring.tickBySeq(1)).toEqual(seqTick(3));
    });

    it("reports an empty seq window before any ticks", () => {
      const ring = new SymbolTapeRing(8);
      expect(ring.lastSeq()).toBe(0);
      expect(ring.oldestSeq()).toBe(1); // empty range: oldest > last
      expect(ring.generation()).toBe(0);
      expect(ring.size()).toBe(0);
      expect(ring.tickBySeq(1)).toBeUndefined();
    });
  });
});
