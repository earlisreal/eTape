import { describe, it, expect } from "vitest";
import { resolvePlaceTemplate } from "./resolveTemplate";
import type { PlaceOrderTemplate } from "./actionTemplate";
import type { Quote } from "../../wire/contract";

const RTH = Date.parse("2026-07-06T14:00:00Z");
const PRE = Date.parse("2026-07-06T08:00:00Z");
const q: Quote = { symbol: "US.AAPL", bid: 3.49, ask: 3.50, last: 3.50, ts: "" };
const tmpl = (o: Partial<PlaceOrderTemplate> = {}): PlaceOrderTemplate => ({
  kind: "place", id: "p1", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY",
  priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, ...o,
});

describe("resolvePlaceTemplate", () => {
  it("resolves price+qty and builds a venue-tagged SubmitOrderArgs + flash string", () => {
    const r = resolvePlaceTemplate(tmpl(), { venue: "alpaca-paper", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: RTH, extHoursMarketBufferPct: 1 });
    expect(r.args.venue).toBe("alpaca-paper");
    expect(r.args.qty).toBe(1428);          // floor(5000/3.50)
    expect(r.args.limitPrice).toBeCloseTo(3.50);
    expect(r.preCheck.ok).toBe(true);
    expect(r.flash).toBe("BUY 1,428 AAPL @ 3.50 LMT");
  });
  it("PositionFraction=all resolves from the live position (flatten)", () => {
    const r = resolvePlaceTemplate(
      tmpl({ side: "SELL", sizing: { mode: "PositionFraction", pct: 100 } }),
      { venue: "alpaca-paper", symbol: "US.AAPL", quote: q, buyingPower: 0, positionQty: 300, nowMs: RTH, extHoursMarketBufferPct: 1 });
    expect(r.args.qty).toBe(300);
    expect(r.args.side).toBe("SELL");
  });
  it("surfaces pre-check failure without throwing (qty 0 → not ok)", () => {
    const r = resolvePlaceTemplate(
      tmpl({ sizing: { mode: "Dollar", dollar: 1 } }),
      { venue: "alpaca-paper", symbol: "US.AAPL", quote: { ...q, ask: 100 }, buyingPower: 0, positionQty: 0, nowMs: RTH, extHoursMarketBufferPct: 1 });
    expect(r.args.qty).toBe(0);
    expect(r.preCheck.ok).toBe(false);
  });
  it("MARKET keeps limitPrice 0 in args and flashes MKT", () => {
    const r = resolvePlaceTemplate(tmpl({ type: "MARKET" }), { venue: "v", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: RTH, extHoursMarketBufferPct: 1 });
    expect(r.args.type).toBe("MARKET");
    expect(r.args.limitPrice).toBe(0);
    expect(r.flash).toContain("MKT");
  });
  it("threads the template's session into the submitted args", () => {
    const r = resolvePlaceTemplate(tmpl({ session: "EXTENDED" }), { venue: "v", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: RTH, extHoursMarketBufferPct: 1 });
    expect(r.args.session).toBe("EXTENDED");
  });
  it("defaults a template with no session to AUTO", () => {
    const r = resolvePlaceTemplate(tmpl(), { venue: "v", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: RTH, extHoursMarketBufferPct: 1 });
    expect(r.args.session).toBe("AUTO");
  });
  it("converts a MARKET template outside RTH to a buffered LIMIT and flashes the limit price", () => {
    const r = resolvePlaceTemplate(
      tmpl({ type: "MARKET", priceSource: "Ask" }),
      { venue: "v", symbol: "US.AAPL", quote: q, buyingPower: 10_000, positionQty: 0, nowMs: PRE, extHoursMarketBufferPct: 1 });
    expect(r.args.type).toBe("LIMIT");
    expect(r.args.limitPrice).toBeCloseTo(3.54, 2); // ask 3.50 * 1.01 → tick-up
    expect(r.flash).toContain("3.54 LMT");
    expect(r.preCheck.notices.join(" ")).toMatch(/ask \+1%/);
  });
});
