import { describe, it, expect } from "vitest";
import { LIGHT, DARK } from "../palette";
import { chartOptions, candleOptions, volumeOptions, RIGHT_OFFSET_BARS, clampRightScroll } from "./chartTheme";

describe("chartTheme", () => {
  it("maps palette surfaces onto chart layout + grid", () => {
    const o = chartOptions(LIGHT);
    expect(o.layout?.background).toEqual({ type: "solid", color: LIGHT.bg });
    expect(o.layout?.textColor).toBe(LIGHT.text);
    expect(o.grid?.vertLines?.color).toBe(LIGHT.grid);
    expect(o.grid?.horzLines?.color).toBe(LIGHT.grid);
    expect(o.crosshair?.vertLine?.color).toBe(LIGHT.crosshair);
  });

  it("pads the right edge via rightOffset, locks the left edge, and keeps a stable price-scale width", () => {
    const o = chartOptions(LIGHT);
    // Regression guard: LWC's TimeScale hardcodes the max right offset to the
    // literal constant 0 whenever fixRightEdge is true, REGARDLESS of
    // rightOffset — so the two together always collapse to zero right-edge
    // padding (verified against lightweight-charts.development.mjs's
    // _private__maxRightOffset()). DeepChartOptions.timeScale has no
    // fixRightEdge field at all (see chartTheme.ts) so this can't be
    // reintroduced without a compile error.
    expect(o.timeScale?.fixLeftEdge).toBe(true);
    expect(o.timeScale?.rightOffset).toBe(RIGHT_OFFSET_BARS);
    expect(o.timeScale?.shiftVisibleRangeOnNewBar).toBe(true);
    expect(o.rightPriceScale?.minimumWidth).toBeGreaterThan(0);
  });

  it("formats axis tick marks and the crosshair time in US/Eastern, not UTC", () => {
    const o = chartOptions(LIGHT);
    // 2026-07-06T13:30:00Z is 09:30 ET (EDT, UTC-4) — the RTH open.
    const rthOpenUtcSecs = Date.parse("2026-07-06T13:30:00Z") / 1000;
    expect(o.timeScale?.tickMarkFormatter?.(rthOpenUtcSecs, 3, "en-US")).toBe("09:30");
    expect(o.timeScale?.tickMarkFormatter?.(rthOpenUtcSecs, 4, "en-US")).toBe("09:30:00");
    expect(o.localization?.timeFormatter?.(rthOpenUtcSecs)).toContain("09:30:00");
  });

  it("tickMarkFormatter degrades to the default (null) for a non-numeric time", () => {
    const o = chartOptions(LIGHT);
    expect(o.timeScale?.tickMarkFormatter?.("2026-07-06" as unknown as number, 2, "en-US")).toBeNull();
  });

  it("candles use up/down palette colors for body, wick and border", () => {
    const c = candleOptions(DARK);
    expect(c.upColor).toBe(DARK.up);
    expect(c.downColor).toBe(DARK.down);
    expect(c.wickUpColor).toBe(DARK.up);
    expect(c.wickDownColor).toBe(DARK.down);
    expect(c.borderUpColor).toBe(DARK.up);
    expect(c.borderDownColor).toBe(DARK.down);
  });

  it("volume histogram is priceScaleId '' (overlay) and colored per palette", () => {
    const v = volumeOptions(LIGHT);
    expect(v.priceScaleId).toBe("");
    expect(v.priceFormat?.type).toBe("volume");
  });

  it("volume histogram hides its last-value label and price line (no axis-width jitter)", () => {
    const v = volumeOptions(LIGHT);
    expect(v.lastValueVisible).toBe(false);
    expect(v.priceLineVisible).toBe(false);
  });
});

describe("clampRightScroll", () => {
  it("does not clamp the resting position (scrollPosition === rightOffset)", () => {
    expect(clampRightScroll(RIGHT_OFFSET_BARS)).toBeNull();
  });

  it("snaps back to RIGHT_OFFSET_BARS once panned past the cap", () => {
    expect(clampRightScroll(RIGHT_OFFSET_BARS + 0.1)).toBe(RIGHT_OFFSET_BARS);
    expect(clampRightScroll(RIGHT_OFFSET_BARS + 20)).toBe(RIGHT_OFFSET_BARS);
  });

  it("does not clamp when scrolled left of the cap (into history)", () => {
    expect(clampRightScroll(0)).toBeNull();
    expect(clampRightScroll(-5)).toBeNull();
  });
});
