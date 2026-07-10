// TapeRing is now a Map<string, SymbolTapeRing> collection keyed by symbol
// (Task 1) rather than one global ring — see SymbolTapeRing.test.ts for the
// per-symbol ring's own behavior (append/order, capacity wraparound, seq
// windows, generation-bump-on-snapshot). This file covers the collection's
// own job: routing apply() by payload[0].symbol, and per-symbol isolation.
import { describe, it, expect } from "vitest";
import { TapeRing } from "./TapeRing";
import type { SnapshotMsg, DeltaMsg, Tick } from "../wire/contract";

const tick = (symbol: string, price: number): Tick =>
  ({ symbol, price, size: 100, direction: "BUY", ts: `t${price}` });
const snap = (ticks: Tick[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.tape", payload: ticks });
const delta = (ticks: Tick[]): DeltaMsg => ({ kind: "delta", topic: "md.tape", payload: ticks });

describe("TapeRing (per-symbol collection)", () => {
  it("routes apply() by payload[0].symbol: only the addressed symbol's rev/data change", () => {
    const r = new TapeRing(16);
    r.apply(delta([tick("US.NVDA", 1), tick("US.NVDA", 2)]));
    expect(r.getRev("US.NVDA")).toBe(1);
    expect(r.getRev("US.AAPL")).toBe(0);
    expect(r.source("US.AAPL").lastSeq()).toBe(0);
    expect(r.source("US.NVDA").lastSeq()).toBe(2);
  });

  it("drops empty-payload frames (no symbol derivable, nothing to route or store)", () => {
    const r = new TapeRing(16);
    r.apply(delta([]));
    expect(r.getRev()).toBe(0); // global rev unaffected — apply() returned before markDirty()
    expect(r.isDirty()).toBe(false);
  });

  it("lazily creates a symbol's ring only on first apply for that symbol", () => {
    const r = new TapeRing(16);
    expect(r.generation("US.TSLA")).toBe(0);
    r.apply(snap([tick("US.TSLA", 5)]));
    expect(r.generation("US.TSLA")).toBe(1);
  });

  describe("per-symbol snapshot isolation (regression: reconnect snapshots are one frame PER SYMBOL)", () => {
    it("a snapshot for one symbol does not wipe another symbol's ring, and bumps only that symbol's generation", () => {
      const r = new TapeRing(16);
      // Seed both symbols with deltas (simulating live ticks before a reconnect).
      r.apply(delta([tick("US.AAPL", 1), tick("US.AAPL", 2)]));
      r.apply(delta([tick("US.NVDA", 10), tick("US.NVDA", 11), tick("US.NVDA", 12)]));
      const aaplGenBefore = r.generation("US.AAPL");
      const nvdaGenBefore = r.generation("US.NVDA");

      // Reconnect re-sync: engine sends one snapshot frame per retained symbol.
      r.apply(snap([tick("US.AAPL", 100)]));
      r.apply(snap([tick("US.NVDA", 200), tick("US.NVDA", 201)]));

      // Both symbols' post-snapshot data survive — neither wiped the other.
      expect(r.source("US.AAPL").lastSeq()).toBe(1);
      expect(r.source("US.AAPL").tickBySeq(1)?.price).toBe(100);
      expect(r.source("US.NVDA").lastSeq()).toBe(2);
      expect(r.source("US.NVDA").tickBySeq(1)?.price).toBe(200);
      expect(r.source("US.NVDA").tickBySeq(2)?.price).toBe(201);

      // Each symbol's generation incremented by exactly 1 (not the other's).
      expect(r.generation("US.AAPL")).toBe(aaplGenBefore + 1);
      expect(r.generation("US.NVDA")).toBe(nvdaGenBefore + 1);
    });
  });

  describe("source()/generation()/lastTick() for an unknown symbol", () => {
    it("source() returns the documented empty shape", () => {
      const r = new TapeRing(16);
      const src = r.source("US.GHOST");
      expect(src.lastSeq()).toBe(0);
      expect(src.oldestSeq()).toBe(1);
      expect(src.generation()).toBe(0);
      expect(src.tickBySeq(1)).toBeUndefined();
      expect(src.tickBySeq(0)).toBeUndefined();
    });

    it("generation() returns 0 for a never-seen symbol", () => {
      const r = new TapeRing(16);
      expect(r.generation("US.GHOST")).toBe(0);
    });

    it("lastTick() returns undefined for a never-seen symbol", () => {
      const r = new TapeRing(16);
      expect(r.lastTick("US.GHOST")).toBeUndefined();
    });
  });

  describe("lastTick()", () => {
    it("returns the most recently appended tick for that symbol", () => {
      const r = new TapeRing(16);
      r.apply(delta([tick("US.AAPL", 1), tick("US.AAPL", 2), tick("US.AAPL", 3)]));
      expect(r.lastTick("US.AAPL")?.price).toBe(3);
      r.apply(delta([tick("US.AAPL", 4)]));
      expect(r.lastTick("US.AAPL")?.price).toBe(4);
    });

    it("is unaffected by other symbols' appends", () => {
      const r = new TapeRing(16);
      r.apply(delta([tick("US.AAPL", 1)]));
      r.apply(delta([tick("US.NVDA", 99)]));
      expect(r.lastTick("US.AAPL")?.price).toBe(1);
    });
  });

  describe("capacity wraparound is per symbol", () => {
    it("one symbol overrunning its cap does not affect another symbol's retained window", () => {
      const r = new TapeRing(3);
      r.apply(delta(Array.from({ length: 5 }, (_, i) => tick("US.NVDA", i)))); // exceeds cap 3
      r.apply(delta([tick("US.AAPL", 1)]));
      expect(r.source("US.NVDA").oldestSeq()).toBe(3); // seqs 1,2 evicted; 3,4,5 retained
      expect(r.source("US.AAPL").oldestSeq()).toBe(1); // untouched by NVDA's churn
      expect(r.source("US.AAPL").lastSeq()).toBe(1);
    });
  });

  describe("global no-arg getRev() (back-compat)", () => {
    it("increments on ANY symbol's apply, via the base PaintStore rev/dirty flag", () => {
      const r = new TapeRing(16);
      expect(r.getRev()).toBe(0);
      expect(r.isDirty()).toBe(false);
      r.apply(delta([tick("US.AAPL", 1)]));
      expect(r.getRev()).toBe(1);
      expect(r.isDirty()).toBe(true);
      r.consumeDirty();
      r.apply(delta([tick("US.NVDA", 2)])); // a different symbol still bumps the global rev
      expect(r.getRev()).toBe(2);
      expect(r.isDirty()).toBe(true);
    });
  });
});
