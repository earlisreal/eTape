import { describe, it, expect } from "vitest";
import { PANELS, CATALOG, isDevPanel } from "./registry";

describe("panel registry — monitoring surfaces", () => {
  it("registers scanner and movers with the scanner topics", () => {
    for (const id of ["scanner", "movers"]) {
      expect(PANELS[id]).toBeDefined();
      expect(PANELS[id].topics).toEqual(["scanner.rank", "scanner.hit"]);
    }
  });
});

describe("catalog metadata", () => {
  it("every non-dev panel has title/glyph/description", () => {
    for (const [id, def] of Object.entries(PANELS)) {
      if (isDevPanel(id)) continue;
      expect(def.title, id).toBeTruthy();
      expect(def.glyph, id).toBeTruthy();
      expect(def.description, id).toBeTruthy();
    }
  });
  it("CATALOG omits the dev smoke panel and lists chart first", () => {
    expect(CATALOG.map((c) => c.panelId)).not.toContain("smoke-painter");
    expect(CATALOG[0].panelId).toBe("chart");
  });
  it("marks symbol-bearing panels", () => {
    expect(PANELS["chart"].symbolBearing).toBe(true);
    expect(PANELS["scanner"].symbolBearing).toBe(false);
  });
});
