import { describe, it, expect } from "vitest";
import { DEFAULT_SOUND_CONFIG, sanitizeSoundConfig } from "./SoundConfig";

describe("sanitizeSoundConfig", () => {
  it("returns defaults for absent / non-object input", () => {
    expect(sanitizeSoundConfig(undefined)).toEqual(DEFAULT_SOUND_CONFIG);
    expect(sanitizeSoundConfig(null)).toEqual(DEFAULT_SOUND_CONFIG);
    expect(sanitizeSoundConfig("nope")).toEqual(DEFAULT_SOUND_CONFIG);
  });

  it("keeps valid fields and falls back per-field on invalid ones", () => {
    const out = sanitizeSoundConfig({
      enabled: false,
      volume: 2,               // out of range -> clamp to default
      fillSound: "marimba",    // valid
      placeClick: "yes",       // wrong type -> default
      rejectSound: "bogus",    // invalid id -> default
      scannerSound: "off",     // valid ("off" allowed)
    });
    expect(out.enabled).toBe(false);
    expect(out.volume).toBe(DEFAULT_SOUND_CONFIG.volume);
    expect(out.fillSound).toBe("marimba");
    expect(out.placeClick).toBe(DEFAULT_SOUND_CONFIG.placeClick);
    expect(out.rejectSound).toBe(DEFAULT_SOUND_CONFIG.rejectSound);
    expect(out.scannerSound).toBe("off");
  });

  it("clamps volume to [0,1]", () => {
    expect(sanitizeSoundConfig({ volume: -1 }).volume).toBe(0);
    expect(sanitizeSoundConfig({ volume: 0.3 }).volume).toBe(0.3);
  });

  it("DEFAULT_SOUND_CONFIG matches the approved spec", () => {
    expect(DEFAULT_SOUND_CONFIG).toEqual({
      enabled: true, volume: 0.6, fillSound: "twoTone",
      placeClick: true, rejectSound: "alertBeeps", scannerSound: "arpeggio",
    });
  });
});
