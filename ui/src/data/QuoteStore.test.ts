import { describe, it, expect } from "vitest";
import { QuoteStore } from "./QuoteStore";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

const snap = (p: unknown): SnapshotMsg => ({ kind: "snapshot", topic: "md.quote", key: "US.AAPL", payload: p });
const delta = (p: unknown): DeltaMsg => ({ kind: "delta", topic: "md.quote", key: "US.AAPL", payload: p });

describe("QuoteStore", () => {
  it("hydrates from snapshot and merges deltas, marking dirty", () => {
    const s = new QuoteStore();
    s.apply(snap({ symbol: "US.AAPL", bid: 3.49, ask: 3.51, last: 3.5, ts: "t0" }));
    expect(s.isDirty()).toBe(true);
    s.consumeDirty();
    s.apply(delta({ symbol: "US.AAPL", last: 3.6, ts: "t1" }));
    expect(s.get("US.AAPL")).toEqual({ symbol: "US.AAPL", bid: 3.49, ask: 3.51, last: 3.6, ts: "t1" });
    expect(s.isDirty()).toBe(true);
  });
});
