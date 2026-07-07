import { describe, it, expect } from "vitest";
import { PRESETS } from "./presets";
import { PANELS, isDevPanel } from "./panels/registry";

describe("presets", () => {
  it("exposes Monitoring and Trading", () => {
    expect(PRESETS.map((p) => p.id).sort()).toEqual(["monitoring", "trading"]);
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
    });
    it(`${preset.id}: layout panel ids match the config list`, () => {
      const { panels, layout } = preset.build();
      const layoutIds = Object.keys((layout as { panels: Record<string, unknown> }).panels).sort();
      expect(layoutIds).toEqual(panels.map((p) => p.id).sort());
    });
  }
});
