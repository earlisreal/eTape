import { describe, it, expect } from "vitest";
import { LIGHT, DARK, getPalette, type Palette } from "./palette";

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
});
