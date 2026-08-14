// @vitest-environment jsdom
import { describe, it, expect, afterEach } from "vitest";
import { createDockview, type DockviewApi } from "dockview-core";
import { PRESETS, TRADING_LAYOUT } from "./presets";

// dockview's DockviewComponent constructor watches its container via a real
// ResizeObserver on mount, which jsdom doesn't implement (same stub as AppShell.test.tsx).
class FakeResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = FakeResizeObserver;

describe("TRADING_LAYOUT dockview round-trip", () => {
  let api: DockviewApi | undefined;
  let container: HTMLElement | undefined;

  afterEach(() => {
    api?.dispose();
    container?.remove();
  });

  it("fromJSON accepts the layout without throwing and toJSON preserves exactly the expected panel ids", () => {
    container = document.createElement("div");
    document.body.appendChild(container);
    api = createDockview(container, {
      createComponent: () => ({ element: document.createElement("div"), init: () => {} }),
    });

    expect(() => api!.fromJSON(TRADING_LAYOUT)).not.toThrow();

    const roundTripped = api!.toJSON();
    expect(Object.keys(roundTripped.panels).sort()).toEqual(
      [
        "chart-977336c7", "t-chart-1m", "watchlist-75d05981", "scanner-51fd77fe",
        "news-eb65ba23", "t-chart-10s", "t-dom", "t-tape", "t-ticket", "t-account",
      ].sort(),
    );
  });
});

for (const preset of PRESETS.filter((p) => p.id !== "trading")) {
  describe(`${preset.id} dockview round-trip`, () => {
    let api: DockviewApi | undefined;
    let container: HTMLElement | undefined;

    afterEach(() => {
      api?.dispose();
      container?.remove();
    });

    it("restores every declared panel without legacy hidden headers", () => {
      const built = preset.build();
      container = document.createElement("div");
      document.body.appendChild(container);
      api = createDockview(container, {
        createComponent: () => ({ element: document.createElement("div"), init: () => {} }),
      });

      expect(JSON.stringify(built.layout)).not.toContain("hideHeader");
      expect(() => api!.fromJSON(built.layout)).not.toThrow();
      expect(Object.keys(api!.toJSON().panels).sort()).toEqual(built.panels.map((p) => p.id).sort());
    });
  });
}
