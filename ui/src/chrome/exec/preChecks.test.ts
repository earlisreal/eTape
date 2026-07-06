import { describe, it, expect } from "vitest";
import { preCheck, type DraftOrder } from "./preChecks";

// ET: 2026-07-06 is a Monday. 14:00 UTC = 10:00 ET (RTH). 08:00 UTC = 04:00 ET (pre).
const RTH = Date.parse("2026-07-06T14:00:00Z");
const PRE = Date.parse("2026-07-06T08:00:00Z");
const draft = (o: Partial<DraftOrder>): DraftOrder =>
  ({ symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, ...o });

describe("preCheck", () => {
  it("blocks non-positive quantity", () => {
    const r = preCheck(draft({ qty: 0 }), 3.5, RTH);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/greater than 0/);
  });
  it("passes a clean RTH limit", () => {
    expect(preCheck(draft({}), 3.5, RTH).ok).toBe(true);
  });
  it("coerces Market outside RTH to Limit-at-last with a notice", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), 3.44, PRE);
    expect(r.ok).toBe(true);
    expect(r.order.type).toBe("LIMIT");
    expect(r.order.limitPrice).toBeCloseTo(3.44);
    expect(r.notices.join(" ")).toMatch(/coerced to Limit/);
  });
  it("leaves a Market during RTH alone", () => {
    const r = preCheck(draft({ type: "MARKET", limitPrice: 0 }), 3.44, RTH);
    expect(r.order.type).toBe("MARKET");
    expect(r.notices).toHaveLength(0);
  });
  it("rejects an inverted buy stop-limit (limit below stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "BUY", stopPrice: 3.6, limitPrice: 3.5 }), 3.5, RTH);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted buy stop-limit/);
  });
  it("rejects an inverted sell stop-limit (limit above stop)", () => {
    const r = preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.4, limitPrice: 3.5 }), 3.5, RTH);
    expect(r.ok).toBe(false);
    expect(r.errors.join(" ")).toMatch(/Inverted sell stop-limit/);
  });
  it("accepts a coherent sell stop-limit", () => {
    expect(preCheck(draft({ type: "STOP_LIMIT", side: "SELL", stopPrice: 3.5, limitPrice: 3.4 }), 3.5, RTH).ok).toBe(true);
  });
});
