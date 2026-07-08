// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVFloatingToolbar.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVFloatingToolbar } from "./TVFloatingToolbar";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");
const base = {
  chrome, rect: { x: 100, y: 80, w: 60, h: 20 }, color: "#2962FF", width: 1, lineStyle: "solid" as const,
  onColor: vi.fn(), onWidth: vi.fn(), onLineStyle: vi.fn(), onClone: vi.fn(), onDelete: vi.fn(),
};

describe("TVFloatingToolbar", () => {
  it("picks a color from the swatch popover", () => {
    render(<TVFloatingToolbar {...base} />);
    fireEvent.click(screen.getByLabelText("color"));
    fireEvent.click(screen.getByLabelText("color #F23645"));
    expect(base.onColor).toHaveBeenCalledWith("#F23645");
  });

  it("sets width and line style, clones and deletes", () => {
    render(<TVFloatingToolbar {...base} />);
    fireEvent.click(screen.getByLabelText("width 3"));
    fireEvent.change(screen.getByLabelText("line style"), { target: { value: "dashed" } });
    fireEvent.click(screen.getByLabelText("clone"));
    fireEvent.click(screen.getByLabelText("delete drawing"));
    expect(base.onWidth).toHaveBeenCalledWith(3);
    expect(base.onLineStyle).toHaveBeenCalledWith("dashed");
    expect(base.onClone).toHaveBeenCalled();
    expect(base.onDelete).toHaveBeenCalled();
  });
});
