import { describe, it, expect } from "vitest";
import { aggregateFillMarkers, type FillSlice } from "./fillAggregate";
import { bucketStartMs } from "./barBucket";

const at = (iso: string) => Date.parse(iso);
const slice = (o: Partial<FillSlice>): FillSlice =>
  ({ venue: "sim", orderId: "ET1", timeMs: at("2026-07-06T13:31:47Z"), price: 10, qty: 1, side: "buy", ...o });

describe("aggregateFillMarkers", () => {
  it("buckets a mid-bar fill onto its bar's start time (the missing-marker regression)", () => {
    const ts = at("2026-07-06T13:31:47Z"); // not a 1m bar boundary
    const out = aggregateFillMarkers([slice({ timeMs: ts })], "1m");
    expect(out).toHaveLength(1);
    expect(out[0].timeMs).toBe(bucketStartMs(ts, "1m"));
    expect(out[0].timeMs).not.toBe(ts);
  });

  it("merges partial fills of the SAME order in the same bar to the qty-weighted average price, not the equal-weight mean", () => {
    const bar = at("2026-07-06T13:31:00Z");
    const out = aggregateFillMarkers(
      [
        slice({ orderId: "ET1", timeMs: bar + 5000, price: 10, qty: 100 }),
        slice({ orderId: "ET1", timeMs: bar + 40000, price: 20, qty: 1 }),
      ],
      "1m",
    );
    expect(out).toHaveLength(1);
    expect(out[0].price).toBeCloseTo((100 * 10 + 1 * 20) / 101, 6); // 10.099... not 15 (equal-weight mean)
    expect(out[0].timeMs).toBe(bar);
  });

  it("keeps a buy and a sell in the same bar as two distinct markers", () => {
    const bar = at("2026-07-06T13:31:00Z");
    const out = aggregateFillMarkers(
      [slice({ orderId: "ET1", side: "buy", timeMs: bar + 1000 }), slice({ orderId: "ET2", side: "sell", timeMs: bar + 2000 })],
      "1m",
    );
    expect(out).toHaveLength(2);
    expect(out.map((m) => m.side).sort()).toEqual(["buy", "sell"]);
  });

  it("keeps two different orders in the same bar/side as two distinct markers (no cross-order merge)", () => {
    const bar = at("2026-07-06T13:31:00Z");
    const out = aggregateFillMarkers(
      [slice({ orderId: "ET1", price: 10, timeMs: bar + 1000 }), slice({ orderId: "ET2", price: 20, timeMs: bar + 2000 })],
      "1m",
    );
    expect(out).toHaveLength(2);
    expect(out.map((m) => m.price).sort()).toEqual([10, 20]);
  });

  it("splits one order's fills across two bars into two markers, one per bar", () => {
    const bar1 = at("2026-07-06T13:31:00Z");
    const bar2 = at("2026-07-06T13:32:00Z");
    const out = aggregateFillMarkers(
      [slice({ orderId: "ET1", price: 10, timeMs: bar1 + 5000 }), slice({ orderId: "ET1", price: 12, timeMs: bar2 + 5000 })],
      "1m",
    );
    expect(out).toHaveLength(2);
    expect(out.map((m) => m.timeMs)).toEqual([bar1, bar2]);
  });

  it("returns markers sorted by time", () => {
    const bar1 = at("2026-07-06T13:31:00Z");
    const bar2 = at("2026-07-06T13:33:00Z");
    const out = aggregateFillMarkers(
      [slice({ orderId: "ET2", timeMs: bar2 + 1000 }), slice({ orderId: "ET1", timeMs: bar1 + 1000 })],
      "1m",
    );
    expect(out.map((m) => m.timeMs)).toEqual([bar1, bar2]);
  });

  it("returns [] for no fills", () => {
    expect(aggregateFillMarkers([], "1m")).toEqual([]);
  });
});
