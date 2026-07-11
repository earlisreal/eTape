import { describe, it, expect } from "vitest";
import { preCheck, type DraftOrder } from "./preChecks";

// ET: 2026-07-06 is a Monday. 14:00 UTC = 10:00 ET (RTH). 08:00 UTC = 04:00 ET (pre).
const RTH = Date.parse("2026-07-06T14:00:00Z");
const PRE = Date.parse("2026-07-06T08:00:00Z");
const draft = (o: Partial<DraftOrder>): DraftOrder =>
  ({ symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 10, limitPrice: 3.5, stopPrice: 0, ...o });
const q = (o: Partial<{ bid: number; ask: number; last: number }> = {}) =>
  ({ bid: 3.49, ask: 3.5, last: 3.5, ...o });

describe("preCheck", () => {
  it("blocks non-positive quantity", () => {
    const r = preCheck(draft({ qty: 0 }), q(), RTH, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/greater than 0/);
  });
  it("passes a clean RTH limit", () => {
    expect(preCheck(draft({}), q(), RTH, 1).ok).toBe(true);
  });
  it("converts a buy Market outside RTH to ask + buffer, rounded up to tick", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 3.5 }), PRE, 1);
    expect(r.ok).toBe(true);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.54, 2); // ceil(3.535 / 0.01) * 0.01
    expect(r.notices.join(" ")).toMatch(/Limit @ 3\.54 \(ask \+1%\)/);
  });
  it("converts a sell Market outside RTH to bid - buffer, rounded down to tick", () => {
    const r = preCheck(draft({ side: "SELL", type: "MARKET", limitPrice: 0 }), q({ bid: 3.49 }), PRE, 1);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.45, 2); // floor(3.4551 / 0.01) * 0.01
    expect(r.notices.join(" ")).toMatch(/Limit @ 3\.45 \(bid -1%\)/);
  });
  it("uses the $0.0001 tick below $1", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 0.5 }), PRE, 1);
    expect(r.order.limitPrice).toBeCloseTo(0.505, 4); // 0.5 * 1.01, already on a 0.0001 tick
  });
  it("falls back to last (with a notice) when the relevant book side is empty", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 0, last: 3.44 }), PRE, 1);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.48, 2); // ceil(3.4744 / 0.01) * 0.01
    expect(r.notices.join(" ")).toMatch(/no ask; last \+1%/);
  });
  it("blocks a Market outside RTH with no usable price (side and last both 0)", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 0, bid: 0, last: 0 }), PRE, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/no price to coerce/);
  });
  it("respects the buffer percentage (2% vs 1%)", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q({ ask: 3.5 }), PRE, 2);
    expect(r.order.limitPrice).toBeCloseTo(3.57, 2); // 3.5 * 1.02
  });
  it("leaves a Market during RTH unconverted", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), q(), RTH, 1);
    expect(r.order.type).toBe("MARKET");
    expect(r.notices).toHaveLength(0);
  });
  // Broker-safety net keyed on the ACTUAL clock: must apply regardless of the
  // trader's explicit session choice (a chosen session only affects a Limit
  // order's wire TIF/extended_hours flag downstream, never a Market's ability
  // to submit right now).
  it("still converts a Market outside RTH even when the trader explicitly chose RTH", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0, session: "RTH" }), q(), PRE, 1);
    expect(r.order.type).toBe("LIMIT");
    expect(r.notices.join(" ")).toMatch(/Limit @/);
  });
  it("leaves Market alone during actual RTH even when the trader chose EXTENDED/OVERNIGHT", () => {
    for (const session of ["EXTENDED", "OVERNIGHT"] as const) {
      const r = preCheck(draft({ type: "MARKET", limitPrice: 0, session }), q(), RTH, 1);
      expect(r.order.type).toBe("MARKET");
      expect(r.notices).toHaveLength(0);
    }
  });
  it("passes the chosen session through unchanged (never overwritten by the conversion)", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0, session: "OVERNIGHT" }), q(), PRE, 1);
    expect(r.order.session).toBe("OVERNIGHT");
  });
  it("rejects an inverted buy stop-limit (limit below stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "BUY", stopPrice: 3.6, limitPrice: 3.5 }), q(), RTH, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted buy stop-limit/);
  });
  it("rejects an inverted sell stop-limit (limit above stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.4, limitPrice: 3.5 }), q(), RTH, 1);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted sell stop-limit/);
  });
  it("accepts a coherent sell stop-limit", () => {
    expect(preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.5, limitPrice: 3.4 }), q(), RTH, 1).ok).toBe(true);
  });
});
