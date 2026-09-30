import { describe, expect, it } from "vitest";
import { chartBindingForModifiers, chartStopLimitTemplate, chartStopLimitWillTrigger, resolveChartStopLimit } from "./resolveChartStopLimit";
import type { PlaceOrderTemplate } from "./actionTemplate";

const template: PlaceOrderTemplate = {
  kind: "place", id: "entry", label: "Entry", side: "BUY", type: "STOP_LIMIT", tif: "DAY", session: "EXTENDED",
  priceSource: "Last", priceOffset: 0, limitCushion: 0.5, limitCushionUnit: "%", chartBinding: "Shift",
  sizing: { mode: "Dollar", dollar: 1000 },
};

describe("chart stop-limit template resolution", () => {
  it("requires exactly the configured modifier combination", () => {
    expect(chartBindingForModifiers({ ctrlKey: false, altKey: false, shiftKey: true })).toBe("Shift");
    expect(chartBindingForModifiers({ ctrlKey: true, altKey: false, shiftKey: true })).toBe("Ctrl+Shift");
    expect(chartBindingForModifiers({ ctrlKey: true, altKey: false, shiftKey: false })).toBe("Ctrl");
    expect(chartBindingForModifiers({ ctrlKey: true, altKey: true, shiftKey: true })).toBeUndefined();
    expect(chartStopLimitTemplate([template], "Ctrl")).toBeUndefined();
    expect(chartStopLimitTemplate([template], "Shift")).toBe(template);
  });

  it("uses the clicked price as the trigger and the cushion-adjusted limit for sizing", () => {
    const resolved = resolveChartStopLimit(template, {
      venue: "sim", symbol: "US.AAPL", buyingPower: 5000, availableCash: 1000, positionQty: 0, nowMs: Date.now(), extHoursMarketBufferPct: 1,
    }, 100);
    expect(resolved.errors).toEqual([]);
    expect(resolved.args).toMatchObject({ type: "STOP_LIMIT", stopPrice: 100, limitPrice: 100.5, qty: 9, session: "EXTENDED" });
  });

  it("keeps sell-side cushion outward and accepts exact sub-dollar click prices", () => {
    const sell = { ...template, side: "SHORT" as const, limitCushion: 0.25015, limitCushionUnit: "$" as const, sizing: { mode: "Shares" as const, shares: 3 } };
    const resolved = resolveChartStopLimit(sell, {
      venue: "sim", symbol: "US.XYZ", buyingPower: 0, availableCash: 0, positionQty: 0, nowMs: Date.now(), extHoursMarketBufferPct: 1,
    }, 0.7534);
    expect(resolved.errors).toEqual([]);
    expect(resolved.args).toMatchObject({ stopPrice: 0.7534, limitPrice: 0.5032, qty: 3 });
  });

  it("predicts a trigger only from a trusted eligible print", () => {
    const route = { route:"ENGINE_HELD", effectiveSession:"EXTENDED", phase:"PRE", deadlineMs:10,
      hasTrustedEligiblePrint:true, lastEligiblePrice:101 } as const;
    expect(chartStopLimitWillTrigger("BUY", 101, route)).toBe(true);
    expect(chartStopLimitWillTrigger("SELL", 100, route)).toBe(false);
    expect(chartStopLimitWillTrigger("BUY", 102, route)).toBe(false);
    expect(chartStopLimitWillTrigger("BUY", 101, { ...route, hasTrustedEligiblePrint:false })).toBe(false);
  });
});
