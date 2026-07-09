import { describe, it, expect } from "vitest";
import { TradeStore } from "./TradeStore";
import { makeStores, routeToStore } from "./registry";
import type { ClosedTradeRow } from "../wire/contract";

const row = (o: Partial<ClosedTradeRow>): ClosedTradeRow => ({
  venue: "alpaca-paper", symbol: "US.AAPL", isLong: true, qty: 10,
  entryPrice: 100, exitPrice: 105, realized: 50, openMs: 1000, closeMs: 2000, seq: 1,
  ...o,
});
const snap = (payload: ClosedTradeRow[]) => ({ kind: "snapshot" as const, topic: "exec.trades" as never, payload });
const delta = (payload: ClosedTradeRow) => ({ kind: "delta" as const, topic: "exec.trades" as never, payload });

describe("TradeStore", () => {
  it("starts empty", () => {
    const s = new TradeStore();
    expect(s.trades()).toEqual([]);
    expect(s.dayRealized()).toBe(0);
  });

  it("snapshot followed by a delta appends", () => {
    const s = new TradeStore();
    s.apply(snap([row({ seq: 1, closeMs: 1000 }), row({ seq: 2, closeMs: 2000 })]));
    s.apply(delta(row({ seq: 3, closeMs: 3000 })));
    expect(s.trades().map((t) => t.seq)).toEqual([1, 2, 3]);
  });

  it("dedups by seq: a re-snapshot with an already-seen seq does not double the row", () => {
    const s = new TradeStore();
    s.apply(snap([row({ seq: 1, closeMs: 1000, realized: 50 })]));
    // Reconnect re-snapshot includes the same seq=1 row again plus a genuinely new one.
    s.apply(snap([row({ seq: 1, closeMs: 1000, realized: 50 }), row({ seq: 2, closeMs: 2000, realized: 75 })]));
    expect(s.trades()).toHaveLength(2);
    expect(s.trades().map((t) => t.seq)).toEqual([1, 2]);
    // Prove it's actual dedup, not coincidence: a delta repeating seq=1 is also dropped.
    s.apply(delta(row({ seq: 1, closeMs: 1000, realized: 50 })));
    expect(s.trades()).toHaveLength(2);
  });

  it("keeps trades sorted by closeMs regardless of arrival order", () => {
    const s = new TradeStore();
    s.apply(delta(row({ seq: 2, closeMs: 5000 })));
    s.apply(delta(row({ seq: 1, closeMs: 1000 })));
    expect(s.trades().map((t) => t.seq)).toEqual([1, 2]);
  });

  it("dayRealized sums realized across all held trades", () => {
    const s = new TradeStore();
    s.apply(snap([
      row({ seq: 1, closeMs: 1000, realized: 50 }),
      row({ seq: 2, closeMs: 2000, realized: -20 }),
    ]));
    s.apply(delta(row({ seq: 3, closeMs: 3000, realized: 12.5 })));
    expect(s.dayRealized()).toBeCloseTo(42.5);
  });

  it("dayRealized is 0 when no trades are held", () => {
    expect(new TradeStore().dayRealized()).toBe(0);
  });
});

describe("routeToStore exec.trades wiring (anti-silent-failure)", () => {
  it("routes an exec.trades message to stores.trades, not silently dropping it", () => {
    const stores = makeStores();
    routeToStore(stores, { kind: "snapshot", topic: "exec.trades", payload: [row({ seq: 7, closeMs: 4000, realized: 33 })] });
    expect(stores.trades.trades().map((t) => t.seq)).toEqual([7]);
    expect(stores.trades.dayRealized()).toBe(33);
  });
});
