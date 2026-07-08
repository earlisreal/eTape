import { describe, it, expect } from "vitest";
import { LIGHT, DARK } from "../palette";
import { chartOptions, candleOptions, volumeOptions } from "./chartTheme";

describe("chartTheme", () => {
  it("maps palette surfaces onto chart layout + grid", () => {
    const o = chartOptions(LIGHT);
    expect(o.layout?.background).toEqual({ type: "solid", color: LIGHT.bg });
    expect(o.layout?.textColor).toBe(LIGHT.text);
    expect(o.grid?.vertLines?.color).toBe(LIGHT.grid);
    expect(o.grid?.horzLines?.color).toBe(LIGHT.grid);
    expect(o.crosshair?.vertLine?.color).toBe(LIGHT.crosshair);
  });

  it("locks forward pan to the latest bar + padding and keeps a stable price-scale width", () => {
    const o = chartOptions(LIGHT);
    expect(o.timeScale?.fixRightEdge).toBe(true);
    expect(o.timeScale?.shiftVisibleRangeOnNewBar).toBe(true);
    expect(o.rightPriceScale?.minimumWidth).toBeGreaterThan(0);
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
