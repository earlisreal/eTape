import { describe, it, expect } from "vitest";
import { TapeRing } from "./TapeRing";
import type { SnapshotMsg, DeltaMsg, Tick } from "../wire/contract";

const tick = (price: number): Tick => ({ symbol: "US.AAPL", price, size: 100, direction: "BUY", ts: `t${price}` });
const snap = (ticks: Tick[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.tape", key: "US.AAPL", payload: ticks });
const delta = (ticks: Tick[]): DeltaMsg => ({ kind: "delta", topic: "md.tape", key: "US.AAPL", payload: ticks });

describe("TapeRing", () => {
  it("appends batches and preserves order", () => {
    const r = new TapeRing(4);
    r.apply(snap([tick(1), tick(2)]));
    r.apply(delta([tick(3)]));
    expect(r.size()).toBe(3);
    expect(r.latest(3).map((t) => t.price)).toEqual([1, 2, 3]);
    expect(r.isDirty()).toBe(true);
  });

  it("overwrites oldest when capacity is exceeded (burst-proof)", () => {
    const r = new TapeRing(3);
    r.apply(snap([tick(1), tick(2), tick(3)]));
    r.apply(delta([tick(4), tick(5)]));  // exceeds capacity by 2
    expect(r.size()).toBe(3);
    expect(r.latest(3).map((t) => t.price)).toEqual([3, 4, 5]);
    expect(r.at(0).price).toBe(3); // index 0 = oldest retained
  });

  it("snapshot rebuilds the ring from scratch", () => {
    const r = new TapeRing(4);
    r.apply(delta([tick(1), tick(2)]));
    r.apply(snap([tick(9)]));  // reconnect re-snapshot
    expect(r.size()).toBe(1);
    expect(r.latest(1)[0].price).toBe(9);
  });
});
