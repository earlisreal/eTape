import { describe, it, expect } from "vitest";
import { PANELS } from "./registry";
import { SEED_WORKSPACES } from "../../seeds/workspaces";

describe("panel registry — monitoring surfaces", () => {
  it("registers scanner and movers with the scanner topics", () => {
    for (const id of ["scanner", "movers"]) {
      expect(PANELS[id]).toBeDefined();
      expect(PANELS[id].topics).toEqual(["scanner.rank", "scanner.hit"]);
    }
  });
});

describe("seed monitoring — scanner/movers publish target + thresholds", () => {
  const panels = Object.fromEntries(SEED_WORKSPACES.monitoring.panels.map((p) => [p.id, p]));
  it("scanner stays display-pinned but targets a group and carries thresholds", () => {
    expect(panels["m-scanner"].group).toBeNull();
    expect(panels["m-scanner"].settings.targetGroup).toBe("green");
    expect(panels["m-scanner"].settings.thresholds).toBeDefined();
  });
  it("movers stays display-pinned and targets a group", () => {
    expect(panels["m-movers"].group).toBeNull();
    expect(panels["m-movers"].settings.targetGroup).toBe("green");
  });
});
