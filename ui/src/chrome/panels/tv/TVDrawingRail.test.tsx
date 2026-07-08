// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVDrawingRail.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVDrawingRail } from "./TVDrawingRail";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");
const base = {
  chrome, activeTool: "select" as const, magnet: true, hideAll: false, symbol: "US.AAPL",
  onSelectTool: vi.fn(), onToggleMagnet: vi.fn(), onToggleHideAll: vi.fn(),
  hasSelection: () => false, onDeleteSelection: vi.fn(), onClearAll: vi.fn(),
};

describe("TVDrawingRail", () => {
  it("has the data-drawing-rail opt-out marker", () => {
    const { container } = render(<TVDrawingRail {...base} />);
    expect(container.querySelector("[data-drawing-rail]")).toBeTruthy();
  });

  it("selects cursor, rect, measure", () => {
    render(<TVDrawingRail {...base} />);
    fireEvent.click(screen.getByLabelText("cursor"));
    fireEvent.click(screen.getByLabelText("rectangle"));
    fireEvent.click(screen.getByLabelText("measure"));
    expect(base.onSelectTool).toHaveBeenCalledWith("select");
    expect(base.onSelectTool).toHaveBeenCalledWith("rect");
    expect(base.onSelectTool).toHaveBeenCalledWith("measure");
  });

  it("group button selects the last line tool; flyout picks another", () => {
    render(<TVDrawingRail {...base} />);
    fireEvent.click(screen.getByLabelText(/line tool /));       // default trendline
    expect(base.onSelectTool).toHaveBeenCalledWith("trendline");
    fireEvent.click(screen.getByLabelText("line tools"));        // open flyout
    fireEvent.click(screen.getByLabelText("select ray"));
    expect(base.onSelectTool).toHaveBeenCalledWith("ray");
  });

  it("toggles magnet and hide-all", () => {
    render(<TVDrawingRail {...base} />);
    fireEvent.click(screen.getByLabelText("magnet"));
    fireEvent.click(screen.getByLabelText("hide all drawings"));
    expect(base.onToggleMagnet).toHaveBeenCalled();
    expect(base.onToggleHideAll).toHaveBeenCalled();
    expect(screen.getByLabelText("magnet").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByLabelText("hide all drawings").getAttribute("aria-pressed")).toBe("false");
  });

  it("trash deletes selection directly when one exists", () => {
    render(<TVDrawingRail {...base} hasSelection={() => true} />);
    fireEvent.click(screen.getByLabelText("delete"));
    expect(base.onDeleteSelection).toHaveBeenCalled();
  });

  it("trash without selection confirms before clearing all", () => {
    render(<TVDrawingRail {...base} hasSelection={() => false} />);
    fireEvent.click(screen.getByLabelText("delete"));
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(base.onClearAll).toHaveBeenCalled();
  });
});
