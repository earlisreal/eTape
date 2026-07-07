import { describe, it, expect } from "vitest";
import { LIGHT, DARK, getPalette, FONTS, type Palette } from "./palette";

const KEYS: (keyof Palette)[] = [
  "bg", "surface", "border", "text", "textMuted", "grid", "crosshair",
  "up", "down", "volUp", "volDown",
  "buyFill", "sellFill", "fillOutline",
  "sessionPre", "sessionRth", "sessionPost", "sessionClosed",
  "indVwap", "indEma", "indSma", "indMacdLine", "indMacdSignal", "indMacdHist",
  "linkRed", "linkGreen", "linkBlue", "linkYellow",
  "accent", "ok", "warn", "danger",
];

describe("palette", () => {
  it("both variants define every key with a non-empty string", () => {
    for (const p of [LIGHT, DARK]) {
      for (const k of KEYS) {
        expect(typeof p[k], `${k}`).toBe("string");
        expect(p[k].length, `${k}`).toBeGreaterThan(0);
      }
    }
  });

  it("light and dark differ on the core surfaces", () => {
    expect(LIGHT.bg).not.toBe(DARK.bg);
    expect(LIGHT.text).not.toBe(DARK.text);
  });

  it("getPalette selects by mode", () => {
    expect(getPalette("light")).toBe(LIGHT);
    expect(getPalette("dark")).toBe(DARK);
  });

  it("defines the ladder/tape tokens (Plan 3) in both variants", () => {
    for (const p of [LIGHT, DARK]) {
      for (const k of ["neutral", "depthBid", "depthAsk", "flashBuy", "flashSell", "flashNeutral", "orderMark"] as const) {
        expect(p[k]).toBeTruthy();
      }
      // depth bars and flashes are translucent fills layered under text — must be rgba()
      for (const k of ["depthBid", "depthAsk", "flashBuy", "flashSell", "flashNeutral"] as const) {
        expect(p[k].startsWith("rgba(")).toBe(true);
      }
    }
  });
});

describe("FONTS", () => {
  it("declares serif, sans and mono families with fallbacks", () => {
    expect(FONTS.serif).toContain("IBM Plex Serif");
    expect(FONTS.serif).toMatch(/serif$/);
    expect(FONTS.sans).toContain("IBM Plex Sans");
    expect(FONTS.mono).toContain("IBM Plex Mono");
  });
});
