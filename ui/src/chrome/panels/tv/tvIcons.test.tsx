// @vitest-environment jsdom
// ui/src/chrome/panels/tv/tvIcons.test.tsx
import { describe, it, expect, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/react";
import * as Icons from "./tvIcons";

afterEach(cleanup);

describe("tvIcons", () => {
  it("exports at least 20 icon components", () => {
    const comps = Object.entries(Icons).filter(([k]) => k.startsWith("Icon"));
    expect(comps.length).toBeGreaterThanOrEqual(20);
  });

  it("each icon renders an <svg> and honors the size prop", () => {
    for (const [name, Comp] of Object.entries(Icons)) {
      if (!name.startsWith("Icon")) continue;
      const { container } = render(<Comp size={18} />);
      const svg = container.querySelector("svg");
      expect(svg, name).toBeTruthy();
      expect(svg?.getAttribute("width")).toBe("18");
      cleanup();
    }
  });
});
