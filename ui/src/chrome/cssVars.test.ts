import { describe, it, expect } from "vitest";
import { paletteToVars } from "./cssVars";
import { getPalette } from "../render/palette";

describe("paletteToVars", () => {
  it("kebab-cases every palette key into a --var", () => {
    const vars = paletteToVars(getPalette("light"));
    expect(vars["--bg"]).toBe("#FBFAF7");
    expect(vars["--border-strong"]).toBe("#C9C4B8");
    expect(vars["--text-muted"]).toBe("#6A7280");
    expect(vars["--accent"]).toBe("#9A6A1B");
    expect(vars["--up"]).toBe("#177A58");
  });
  it("emits one var per palette field", () => {
    const p = getPalette("light");
    const vars = paletteToVars(p);
    expect(Object.keys(vars).length).toBe(Object.keys(p).length);
  });
});
