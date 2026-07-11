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

  it("inserts an out-of-order delta in sorted position instead of appending it out of order", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, false)));
    s.apply(delta(bar("09:32", 3.7, false)));
    s.apply(delta(bar("09:31", 3.6, false))); // arrives late, belongs between the two above
    expect(s.series("US.AAPL", "1m").map((b) => b.bucketStart)).toEqual(["09:30", "09:31", "09:32"]);
  });

  it("revises an already-recorded earlier bucket in place without duplicating it", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, false)));
    s.apply(delta(bar("09:31", 3.6, false)));
    s.apply(delta(bar("09:30", 3.55, false))); // late revision to the now-earlier bucket
    const series = s.series("US.AAPL", "1m");
    expect(series).toHaveLength(2);
    expect(series[0]).toMatchObject({ bucketStart: "09:30", c: 3.55 });
  });

  it("keeps series for different timeframes separate", () => {
    const s = new BarStore();
    s.apply(delta(bar("09:30", 3.5, false)));
    s.apply({ kind: "delta", topic: "md.bars", key: "US.AAPL:10s",
      payload: { ...bar("09:30:10", 3.5, false), timeframe: "10s" } });
    expect(s.series("US.AAPL", "1m")).toHaveLength(1);
    expect(s.series("US.AAPL", "10s")).toHaveLength(1);
  });

  it("bumps only the applied (symbol, timeframe) key's own revision", () => {
    const s = new BarStore();
    expect(s.getRev("US.AAPL", "1m")).toBe(0);
    expect(s.getRev("US.NVDA", "1m")).toBe(0);

    s.apply(delta(bar("09:30", 3.5, false)));
    expect(s.getRev("US.AAPL", "1m")).toBe(1);
    expect(s.getRev("US.NVDA", "1m")).toBe(0);

    s.apply(delta(bar("09:31", 3.6, false)));
    expect(s.getRev("US.AAPL", "1m")).toBe(2);
    expect(s.getRev("US.NVDA", "1m")).toBe(0);
  });

  it("bumps the per-key revision from a non-empty snapshot but not from an empty one", () => {
    const s = new BarStore();
    s.apply(snap([]));
    expect(s.getRev("US.AAPL", "1m")).toBe(0);

    s.apply(snap([bar("09:30", 3.5, false)]));
    expect(s.getRev("US.AAPL", "1m")).toBe(1);
  });

  it("falls back to the global PaintStore revision when symbol/timeframe are omitted", () => {
    const s = new BarStore();
    expect(s.getRev()).toBe(0);
    s.apply(delta(bar("09:30", 3.5, false)));
    expect(s.getRev()).toBe(1);
  });
});

describe("BarStore batch prepend", () => {
  const batchBar = (bucketStart: string): Record<string, unknown> => ({
    symbol: "US.AAPL", timeframe: "1m", bucketStart, o: 1, h: 1, l: 1, c: 1, v: 1, inProgress: false,
  });

  it("unshifts a strictly-older batch and bumps rev", () => {
    const s = new BarStore();
    s.apply({ kind: "snapshot", topic: "md.bars", payload: [batchBar("2024-01-02T10:00:00Z"), batchBar("2024-01-02T10:01:00Z")] } as never);
    const rev0 = s.getRev("US.AAPL", "1m");

    s.apply({ kind: "delta", topic: "md.bars", payload: [batchBar("2024-01-01T10:00:00Z"), batchBar("2024-01-01T10:01:00Z")] } as never);

    const series = s.series("US.AAPL", "1m");
    expect(series.map((b) => b.bucketStart)).toEqual([
      "2024-01-01T10:00:00Z", "2024-01-01T10:01:00Z",
      "2024-01-02T10:00:00Z", "2024-01-02T10:01:00Z",
    ]);
    expect(s.getRev("US.AAPL", "1m")).toBeGreaterThan(rev0);
  });

  it("ignores non-older bars in a batch (no duplicates)", () => {
    const s = new BarStore();
    s.apply({ kind: "snapshot", topic: "md.bars", payload: [batchBar("2024-01-02T10:00:00Z")] } as never);
    s.apply({ kind: "delta", topic: "md.bars", payload: [batchBar("2024-01-02T10:00:00Z"), batchBar("2024-01-01T10:00:00Z")] } as never);
    expect(s.series("US.AAPL", "1m").map((b) => b.bucketStart)).toEqual([
      "2024-01-01T10:00:00Z", "2024-01-02T10:00:00Z",
    ]);
  });
});
