import { describe, it, expect } from "vitest";
import { IndicatorStore } from "./IndicatorStore";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

const snap = (key: string, payload: unknown): SnapshotMsg => ({ kind: "snapshot", topic: "md.indicator", key, payload });
const delta = (key: string, payload: unknown): DeltaMsg => ({ kind: "delta", topic: "md.indicator", key, payload });

describe("IndicatorStore", () => {
  it("snapshot loads a series, delta appends a new point", () => {
    const s = new IndicatorStore();
    s.apply(snap("vwap-1", [{ timeMs: 1000, value: 10 }, { timeMs: 2000, value: 11 }]));
    s.apply(delta("vwap-1", { timeMs: 3000, value: 12 }));
    expect(s.series("vwap-1")).toEqual([
      { timeMs: 1000, value: 10 }, { timeMs: 2000, value: 11 }, { timeMs: 3000, value: 12 },
    ]);
  });

  it("delta with same timeMs upserts the last point in place (in-progress value)", () => {
    const s = new IndicatorStore();
    s.apply(snap("vwap-1", [{ timeMs: 1000, value: 10 }]));
    s.apply(delta("vwap-1", { timeMs: 1000, value: 10.5 }));
    expect(s.series("vwap-1")).toEqual([{ timeMs: 1000, value: 10.5 }]);
  });

  it("keeps instances independent and marks dirty on apply", () => {
    const s = new IndicatorStore();
    s.apply(snap("ema-9", [{ timeMs: 1000, value: 5 }]));
    s.apply(snap("sma-20", [{ timeMs: 1000, value: 6 }]));
    expect(s.series("ema-9")).toHaveLength(1);
    expect(s.series("sma-20")[0].value).toBe(6);
    expect(s.series("missing")).toEqual([]);
    expect(s.consumeDirty()).toBe(true);
    expect(s.consumeDirty()).toBe(false);
  });
});
