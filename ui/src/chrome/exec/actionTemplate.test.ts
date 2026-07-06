import { describe, it, expect } from "vitest";
import { DEFAULT_TEMPLATES, DEFAULT_ORDER_CONFIG, ORDER_CONFIG_KEY, type ActionTemplate } from "./actionTemplate";

describe("action templates", () => {
  it("ships defaults with unique ids and a mix of place + manage kinds", () => {
    const ids = DEFAULT_TEMPLATES.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(DEFAULT_TEMPLATES.some((t) => t.kind === "place")).toBe(true);
    expect(DEFAULT_TEMPLATES.some((t) => t.kind === "manage")).toBe(true);
  });
  it("every place default carries a complete sizing + price recipe", () => {
    for (const t of DEFAULT_TEMPLATES.filter((t): t is Extract<ActionTemplate, { kind: "place" }> => t.kind === "place")) {
      expect(t.sizing.mode).toBeTruthy();
      expect(["Bid", "Ask", "Last", "Mid"]).toContain(t.priceSource);
    }
  });
  it("default order config wraps templates + an empty active venue", () => {
    expect(ORDER_CONFIG_KEY).toBe("orderConfig");
    expect(DEFAULT_ORDER_CONFIG.templates).toEqual(DEFAULT_TEMPLATES);
    expect(DEFAULT_ORDER_CONFIG.activeVenue).toBe("");
  });
});
