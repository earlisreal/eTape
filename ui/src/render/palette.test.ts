import { describe, it, expect } from "vitest";
import { LIGHT, DARK, getPalette, FONTS, type Palette } from "./palette";

const KEYS: (keyof Palette)[] = [
  "bg", "surface", "border", "text", "textMuted", "grid", "crosshair",
  "up", "down", "volUp", "volDown",
  "buyFill", "sellFill", "shortFill", "fillOutline",
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

describe("Daylight Ledger palette", () => {
  it("light is warm paper with bronze accent and a borderStrong rule", () => {
    const p = getPalette("light");
    expect(p.bg).toBe("#FBFAF7");
    expect(p.surface).toBe("#F2F0EA");
    expect(p.border).toBe("#DDD9CF");
    expect(p.borderStrong).toBe("#C9C4B8");
    expect(p.text).toBe("#171A1E");
    expect(p.textMuted).toBe("#6A7280");
    expect(p.up).toBe("#177A58");
    expect(p.down).toBe("#C2334D");
    expect(p.accent).toBe("#9A6A1B");
    expect(p.danger).toBe("#A81E30");
  });
  it("keeps every dark token role populated (dark stays first-class)", () => {
    const d = getPalette("dark");
    for (const k of Object.keys(getPalette("light")) as (keyof typeof d)[]) {
      expect(d[k], `dark.${k}`).toBeTruthy();
    }
  });
  it("reserves ok/warn for link-status colours (green/bronze), not a 2nd accent", () => {
    const p = getPalette("light");
    expect(p.ok).toBe(p.up);       // link ok == green
    expect(p.warn).toBe(p.accent); // link degraded == bronze
  });
});
