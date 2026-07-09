// @vitest-environment jsdom
import { describe, it, expect, afterEach } from "vitest";
import { createDockview, type DockviewApi } from "dockview-core";
import { TRADING_LAYOUT } from "./presets";

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
      ["t-chart-1m", "t-chart-10s", "t-dom", "t-tape", "t-ticket", "t-account"].sort(),
    );
  });
});
