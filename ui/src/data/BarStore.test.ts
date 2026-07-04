import { describe, it, expect } from "vitest";
import { BarStore } from "./BarStore";
import type { Bar, SnapshotMsg, DeltaMsg } from "../wire/contract";

const bar = (bucketStart: string, c: number, inProgress: boolean): Bar => ({
  symbol: "US.AAPL", timeframe: "1m", bucketStart,
  o: 3.5, h: 3.6, l: 3.4, c, v: 1000, inProgress,
});
const snap = (bars: Bar[]): SnapshotMsg => ({ kind: "snapshot", topic: "md.bars", key: "US.AAPL:1m", payload: bars });
const delta = (b: Bar): DeltaMsg => ({ kind: "delta", topic: "md.bars", key: "US.AAPL:1m", payload: b });

describe("BarStore", () => {
  it("seeds from a burst snapshot in bucket order", () => {
    const s = new BarStore();
    s.apply(snap([bar("09:31", 3.5, false), bar("09:30", 3.4, false)]));
    expect(s.series("US.AAPL", "1m").map((b) => b.bucketStart)).toEqual(["09:30", "09:31"]);
    expect(s.isDirty()).toBe(true);
  });

  it("upserts the in-progress bar in place, then finalizes it", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, true)));
    s.apply(delta(bar("09:30", 3.55, true)));  // same bucket updates in place
    expect(s.series("US.AAPL", "1m")).toHaveLength(1);
    expect(s.inProgressBar("US.AAPL", "1m")?.c).toBe(3.55);
    s.apply(delta(bar("09:30", 3.58, false))); // finalize
    expect(s.inProgressBar("US.AAPL", "1m")).toBeUndefined();
    expect(s.series("US.AAPL", "1m")[0].c).toBe(3.58);
  });

  it("keeps series for different timeframes separate", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, false)));
    s.apply({ kind: "delta", topic: "md.bars", key: "US.AAPL:10s",
      payload: { ...bar("09:30:10", 3.5, false), timeframe: "10s" } });
    expect(s.series("US.AAPL", "1m")).toHaveLength(1);
    expect(s.series("US.AAPL", "10s")).toHaveLength(1);
  });
});
