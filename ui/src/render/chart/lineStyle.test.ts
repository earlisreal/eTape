import { describe, it, expect } from "vitest";
import { LINE_DASH, LWC_LINE_STYLE, LINE_STYLE_NAMES } from "./lineStyle";

describe("lineStyle", () => {
  it("solid is an empty dash array; dashed/dotted are not", () => {
    expect(LINE_DASH.solid).toEqual([]);
    expect(LINE_DASH.dashed.length).toBeGreaterThan(0);
    expect(LINE_DASH.dotted.length).toBeGreaterThan(0);
  });

  it("maps names to LWC LineStyle enum values", () => {
    expect(LWC_LINE_STYLE.solid).toBe(0);
    expect(LWC_LINE_STYLE.dotted).toBe(1);
    expect(LWC_LINE_STYLE.dashed).toBe(2);
  });

  it("enumerates all three names", () => {
    expect([...LINE_STYLE_NAMES]).toEqual(["solid", "dashed", "dotted"]);
  });
});
