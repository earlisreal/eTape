import { describe, it, expect } from "vitest";
import { BookStore } from "./BookStore";
import type { SnapshotMsg } from "../wire/contract";

const book = (bids: number[][], asks: number[][]): SnapshotMsg => ({
  kind: "snapshot", topic: "md.book", key: "US.AAPL",
  payload: {
    symbol: "US.AAPL", ts: "t",
    bids: bids.map(([price, size]) => ({ price, size })),
    asks: asks.map(([price, size]) => ({ price, size })),
  },
});

describe("BookStore", () => {
  it("replaces the whole book on each apply", () => {
    const s = new BookStore();
    s.apply(book([[3.49, 100]], [[3.51, 200]]));
    expect(s.get("US.AAPL")?.bids).toHaveLength(1);
    s.apply({ ...book([[3.48, 50], [3.47, 75]], [[3.5, 10]]) });
    expect(s.get("US.AAPL")?.bids).toHaveLength(2);
    expect(s.get("US.AAPL")?.asks[0]).toEqual({ price: 3.5, size: 10 });
    expect(s.isDirty()).toBe(true);
  });
});
