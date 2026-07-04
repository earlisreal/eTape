// vitest.config.ts sets no explicit `test.include`, so vitest's default glob
// (**/*.{test,spec}.?(c|m)[jt]s?(x)) applies and already covers fixtures/** —
// mirrors the existing precedent of mock-engine/server.test.ts living outside src/.
import { describe, it, expect } from "vitest";
import fixture from "./chart-session.json";
import { bucketStartMs } from "../src/render/chart/barBucket";

describe("chart-session fixture", () => {
  it("every md.bars bucketStart is consistent with the engine-mirror bucketing", () => {
    const bars = (fixture.snapshots.find((s) => s.topic === "md.bars")!.payload as Array<{ bucketStart: string; timeframe: string }>);
    for (const b of bars) {
      const ms = Date.parse(b.bucketStart);
      expect(bucketStartMs(ms, b.timeframe as "1m")).toBe(ms);
    }
  });
});
