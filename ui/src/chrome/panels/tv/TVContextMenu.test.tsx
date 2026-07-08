// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVContextMenu.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVContextMenu } from "./TVContextMenu";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("TVContextMenu", () => {
  it("renders items and separators; item click fires onClick then onClose", () => {
    const onClose = vi.fn(); const reset = vi.fn();
    render(<TVContextMenu chrome={chrome} x={10} y={20} onClose={onClose}
      items={[{ label: "Reset chart view", onClick: reset }, "separator", { label: "Settings…", onClick: () => {} }]} />);
    fireEvent.click(screen.getByRole("button", { name: "Reset chart view" }));
    expect(reset).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it("closes on Escape", () => {
    const onClose = vi.fn();
    render(<TVContextMenu chrome={chrome} x={0} y={0} onClose={onClose} items={[{ label: "A", onClick: () => {} }]} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("closes on outside mousedown", () => {
    const onClose = vi.fn();
    render(<TVContextMenu chrome={chrome} x={0} y={0} onClose={onClose} items={[{ label: "A", onClick: () => {} }]} />);
    fireEvent.mouseDown(document.body);
    expect(onClose).toHaveBeenCalled();
  });
});
