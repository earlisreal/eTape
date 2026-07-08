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

describe("Task 19: merged account panel + back-compat aliases", () => {
  it("registers the merged account panel with all four exec/quote topics", () => {
    expect(PANELS["account"]).toBeDefined();
    expect(PANELS["account"].topics).toEqual(["exec.account", "exec.positions", "exec.status", "md.quote"]);
  });
  it("aliases the pre-merge ids to the same merged component for saved-doc back-compat", () => {
    expect(PANELS["account-bar"].component).toBe(PANELS["account"].component);
    expect(PANELS["positions"].component).toBe(PANELS["account"].component);
  });
  it("omits the retired ids from the Add Panel catalog but keeps only the merged one", () => {
    const ids = CATALOG.map((c) => c.panelId);
    expect(ids).toContain("account");
    expect(ids).not.toContain("account-bar");
    expect(ids).not.toContain("positions");
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

describe("panel demand profiles", () => {
  it("maps chart/tape to watch, ladder to focused, news to interest", () => {
    expect(PANELS.chart.demand).toBe("watch");
    expect(PANELS.tape.demand).toBe("watch");
    expect(PANELS.ladder.demand).toBe("focused");
    expect(PANELS.news.demand).toBe("interest");
  });
  it("leaves non-symbol panels without a demand profile", () => {
    expect(PANELS.scanner?.demand).toBeUndefined();
  });
});
