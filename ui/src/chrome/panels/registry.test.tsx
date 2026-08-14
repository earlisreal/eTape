import { describe, it, expect } from "vitest";
import { PANELS, CATALOG, isDevPanel } from "./registry";
import { TAPE_MIN_WIDTH } from "../../render/tape/tapeLayout";

describe("panel registry — monitoring surfaces", () => {
  it("registers scanner only with the scanner topics", () => {
    expect(PANELS.scanner.topics).toEqual(["scanner.rank", "scanner.hit"]);
    expect(PANELS.scanner.headerControls).toBe(true);
    expect(PANELS.movers).toBeUndefined();
  });
});

describe("Task 19: merged account panel + back-compat aliases", () => {
  it("registers the merged account panel with all six exec/quote topics", () => {
    expect(PANELS["account"]).toBeDefined();
    expect(PANELS["account"].topics).toEqual([
      "exec.account", "exec.positions", "exec.orders", "exec.fills", "exec.trades", "exec.status", "md.quote",
    ]);
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

describe("Task 8: open-orders folds into the merged account panel", () => {
  it("aliases open-orders to the same merged component for saved-doc back-compat", () => {
    expect(PANELS["open-orders"].component).toBe(PANELS["account"].component);
  });
  it("omits open-orders from the Add Panel catalog", () => {
    const ids = CATALOG.map((c) => c.panelId);
    expect(ids).not.toContain("open-orders");
  });
  it("gives all four account aliases the identical topics array, including exec.orders and exec.trades", () => {
    const expected = [
      "exec.account", "exec.positions", "exec.orders", "exec.fills", "exec.trades", "exec.status", "md.quote",
    ];
    for (const id of ["account", "account-bar", "positions", "open-orders"]) {
      expect(PANELS[id].topics, id).toEqual(expected);
    }
  });
});

describe("chart panel subscribes to fills", () => {
  it("includes exec.fills so live buy/sell fill markers reach the chart", () => {
    expect(PANELS["chart"].topics).toEqual(["md.bars", "md.indicator", "exec.fills"]);
  });
});

describe("catalog metadata", () => {
  it("keeps the canonical tape title and its responsive short title", () => {
    expect(PANELS.tape.title).toBe("Time & Sales");
    expect(PANELS.tape.shortTitle).toBe("T&S");
    expect(CATALOG.find((panel) => panel.panelId === "tape")?.title).toBe("Time & Sales");
  });

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
  it("maps chart to chart, tape to watch, ladder to focused, news to interest", () => {
    expect(PANELS.chart.demand).toBe("chart");
    expect(PANELS.tape.demand).toBe("watch");
    expect(PANELS.ladder.demand).toBe("focused");
    expect(PANELS.news.demand).toBe("interest");
  });
  it("leaves non-symbol panels without a demand profile", () => {
    expect(PANELS.scanner?.demand).toBeUndefined();
  });
});

describe("locates panel", () => {
  it("is a symbol-bearing execution panel without market-data demand", () => {
    expect(PANELS.locates.topics).toEqual(["exec.status"]);
    expect(PANELS.locates.symbolBearing).toBe(true);
    expect(PANELS.locates.demand).toBeUndefined();
    expect(CATALOG.map((c) => c.panelId)).toContain("locates");
  });
});

describe("ladder header actions", () => {
  it("provides a header slot for the DOM settings gear", () => {
    expect(PANELS.ladder.headerActions).toBe(true);
  });
});

describe("panel minimum widths", () => {
  it("registers the tape minimum from the shared layout constants", () => {
    expect(PANELS.tape.minimumWidth).toBe(TAPE_MIN_WIDTH);
  });
});
