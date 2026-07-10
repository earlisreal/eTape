import { describe, it, expect } from "vitest";
import { LIGHT } from "../palette";
import { TV_LIGHT, TV_DARK, getTvPalette, getTvChrome, TV_FONT, TV_GEOM } from "./tvTheme";

describe("tvTheme palettes", () => {
  it("TV palettes have exactly the Palette interface keys (no drift from the app palette)", () => {
    const appKeys = Object.keys(LIGHT).sort();
    expect(Object.keys(TV_LIGHT).sort()).toEqual(appKeys);
    expect(Object.keys(TV_DARK).sort()).toEqual(appKeys);
  });

  it("uses TV's current candle colors in both themes", () => {
    expect(TV_LIGHT.up).toBe("#089981");
    expect(TV_LIGHT.down).toBe("#F23645");
    expect(TV_DARK.up).toBe("#089981");
    expect(TV_DARK.down).toBe("#F23645");
  });

  it("uses TV chart backgrounds and accent", () => {
    expect(TV_LIGHT.bg).toBe("#FFFFFF");
    expect(TV_DARK.bg).toBe("#131722");
    expect(TV_LIGHT.accent).toBe("#2962FF");
    expect(TV_DARK.accent).toBe("#2962FF");
  });

  it("uses TV study colors for indicator defaults", () => {
    expect(TV_LIGHT.indEma).toBe("#2962FF");
    expect(TV_LIGHT.indSma).toBe("#FF6D00");
    expect(TV_LIGHT.indVwap).toBe("#7E57C2");
    expect(TV_LIGHT.indMacdLine).toBe("#2962FF");
    expect(TV_LIGHT.indMacdSignal).toBe("#FF6D00");
  });

  it("getTvPalette selects by mode", () => {
    expect(getTvPalette("light")).toBe(TV_LIGHT);
    expect(getTvPalette("dark")).toBe(TV_DARK);
  });

  it("uses distinct pastel fill-diamond colors, not the low-alpha candle hue", () => {
    expect(TV_LIGHT.buyFill).toBe("rgba(165,214,167,.8)");
    expect(TV_LIGHT.sellFill).toBe("rgba(244,143,177,.8)");
    expect(TV_DARK.buyFill).toBe("rgba(165,214,167,.8)");
    expect(TV_DARK.sellFill).toBe("rgba(244,143,177,.8)");
  });
});

describe("tvTheme chrome tokens", () => {
  it("exposes TV hover fills per theme", () => {
    expect(getTvChrome("light").hover).toBe("#F0F3FA");
    expect(getTvChrome("dark").hover).toBe("#2A2E39");
  });
  it("exposes TV toolbar/dialog surfaces per theme", () => {
    expect(getTvChrome("light").surface).toBe("#FFFFFF");
    expect(getTvChrome("dark").surface).toBe("#1E222D");
  });
});

describe("tvTheme font + geometry", () => {
  it("uses TV's font stack without IBM Plex", () => {
    expect(TV_FONT).toContain("Trebuchet MS");
    expect(TV_FONT).not.toContain("Plex");
  });
  it("uses TV geometry", () => {
    expect(TV_GEOM.iconBtn).toBe(28);
    expect(TV_GEOM.radius).toBe(6);
  });
});
