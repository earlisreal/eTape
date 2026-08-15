import { describe, it, expect } from "vitest";
import { buildMonitoringWorkspace, PRESETS } from "./presets";
import { PANELS, isDevPanel } from "./panels/registry";

describe("presets", () => {
  it("exposes Trading as the replaceable preset", () => {
    expect(PRESETS.map((p) => p.id).sort()).toEqual(["trading"]);
  });
  it("builds an unassigned Monitoring workspace", () => {
    const workspace = buildMonitoringWorkspace();
    expect(workspace.name).toBe("monitoring");
    expect(workspace.panels.filter((p) => p.panelId === "chart")).toHaveLength(4);
    expect(workspace.panels.every((p) => p.group === null)).toBe(true);
    expect(workspace.panels.every((p) => !("symbol" in p.settings))).toBe(true);
  });
  for (const preset of PRESETS) {
    it(`${preset.id}: every panel id is a real, non-dev registered panel`, () => {
      const { panels, layout } = preset.build();
      expect(panels.length).toBeGreaterThan(0);
      for (const p of panels) {
        expect(PANELS[p.panelId], p.panelId).toBeTruthy();
        expect(isDevPanel(p.panelId), p.panelId).toBe(false);
      }
      // layout JSON references exactly the panel ids we declared
      expect(layout && typeof layout).toBe("object");
      expect(JSON.stringify(layout)).not.toContain("hideHeader");
    });
    it(`${preset.id}: layout panel ids match the config list`, () => {
      const { panels, layout } = preset.build();
      const layoutIds = Object.keys((layout as { panels: Record<string, unknown> }).panels).sort();
      expect(layoutIds).toEqual(panels.map((p) => p.id).sort());
    });
  }
});
