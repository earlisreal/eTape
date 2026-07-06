import { describe, it, expect } from "vitest";
import { WebAudioPatchPlayer, FILL_PATCHES, REJECT_PATCHES, SCANNER_PATCHES, resolvePatch } from "./patches";
import { FILL_SOUND_IDS, REJECT_SOUND_IDS, SCANNER_SOUND_IDS } from "./SoundConfig";

describe("WebAudioPatchPlayer (node env, no Web Audio)", () => {
  it("unlock / setMasterVolume / play are safe no-ops when AudioContext is undefined", () => {
    const p = new WebAudioPatchPlayer();
    expect(() => p.unlock()).not.toThrow();
    expect(() => p.setMasterVolume(0.5)).not.toThrow();
    expect(() =>
      p.play(() => {
        throw new Error("must not run");
      }, "buy")
    ).not.toThrow();
  });
});

describe("patch registries", () => {
  it("has a patch for every configured sound id", () => {
    for (const id of FILL_SOUND_IDS) expect(typeof FILL_PATCHES[id]).toBe("function");
    for (const id of REJECT_SOUND_IDS) expect(typeof REJECT_PATCHES[id]).toBe("function");
    for (const id of SCANNER_SOUND_IDS) expect(typeof SCANNER_PATCHES[id]).toBe("function");
    expect(typeof resolvePatch("place", "x")).toBe("function");
    expect(resolvePatch("fill", "bogus")).toBeUndefined();
  });
});
