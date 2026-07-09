// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVDrawingRail.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVDrawingRail } from "./TVDrawingRail";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");
const base = {
  chrome, activeTool: "select" as const, hideAll: false, symbol: "US.AAPL",
  onSelectTool: vi.fn(), onToggleHideAll: vi.fn(),
  hasSelection: () => false, onDeleteSelection: vi.fn(), onClearAll: vi.fn(),
};

describe("TVDrawingRail", () => {
  it("has the data-drawing-ui opt-out marker", () => {
    const { container } = render(<TVDrawingRail {...base} />);
    expect(container.querySelector("[data-drawing-ui]")).toBeTruthy();
  });

  it("has no cursor button; rect and measure arm their tools", () => {
    render(<TVDrawingRail {...base} />);
    expect(screen.queryByLabelText("cursor")).toBeNull();
    fireEvent.click(screen.getByLabelText("rectangle"));
    fireEvent.click(screen.getByLabelText("measure"));
    expect(base.onSelectTool).toHaveBeenCalledWith("rect");
    expect(base.onSelectTool).toHaveBeenCalledWith("measure");
  });

  it("re-clicking the armed tool toggles back to select", () => {
    render(<TVDrawingRail {...base} activeTool="rect" />);
    fireEvent.click(screen.getByLabelText("rectangle"));
    expect(base.onSelectTool).toHaveBeenCalledWith("select");
  });

  it("lays out horizontally and drags via the grip, reporting one position on release", () => {
    const onPosChange = vi.fn();
    const { container } = render(<TVDrawingRail {...base} onPosChange={onPosChange} />);
    const rail = container.querySelector("[data-drawing-ui]") as HTMLDivElement;
    expect(rail.style.flexDirection).toBe("row");
    const grip = screen.getByLabelText("move toolbar");
    // jsdom has no layout (all rects are zeros), so the drag clamps to 0,0 —
    // the contract under test is down → move → single report on release.
    // MouseEvent-typed dispatches: jsdom has no PointerEvent constructor, and
    // fireEvent.pointerDown's fallback drops clientX/clientY entirely. The
    // move/up land on the window listeners the grip's pointerdown installs.
    fireEvent(grip, new MouseEvent("pointerdown", { bubbles: true, clientX: 100, clientY: 50 }));
    fireEvent(grip, new MouseEvent("pointermove", { bubbles: true, clientX: 140, clientY: 90 }));
    expect(onPosChange).not.toHaveBeenCalled();
    fireEvent(grip, new MouseEvent("pointerup", { bubbles: true }));
    expect(onPosChange).toHaveBeenCalledTimes(1);
    expect(onPosChange).toHaveBeenCalledWith({ x: 0, y: 0 });
    expect(rail.style.left).toBe("0px");
  });

  it("seeds its position from initialPos", () => {
    const { container } = render(<TVDrawingRail {...base} initialPos={{ x: 33, y: 44 }} />);
    const rail = container.querySelector("[data-drawing-ui]") as HTMLDivElement;
    expect(rail.style.left).toBe("33px");
    expect(rail.style.top).toBe("44px");
  });

  it("has no flyout — trend line, horizontal line, and extended line are flat buttons", () => {
    render(<TVDrawingRail {...base} />);
    expect(screen.queryByLabelText("line tools")).toBeNull();
    expect(screen.queryByLabelText(/^select /)).toBeNull();
    fireEvent.click(screen.getByLabelText("trend line"));
    expect(base.onSelectTool).toHaveBeenCalledWith("trendline");
    fireEvent.click(screen.getByLabelText("horizontal line"));
    expect(base.onSelectTool).toHaveBeenCalledWith("hline");
    fireEvent.click(screen.getByLabelText("extended line"));
    expect(base.onSelectTool).toHaveBeenCalledWith("extendedline");
  });

  it("ray and horizontal ray tools no longer exist on the rail", () => {
    render(<TVDrawingRail {...base} />);
    expect(screen.queryByLabelText("ray")).toBeNull();
    expect(screen.queryByLabelText("horizontal ray")).toBeNull();
  });

  it("re-clicking an armed line tool toggles back to select", () => {
    render(<TVDrawingRail {...base} activeTool="extendedline" />);
    fireEvent.click(screen.getByLabelText("extended line"));
    expect(base.onSelectTool).toHaveBeenCalledWith("select");
  });

  it("toggles hide-all", () => {
    render(<TVDrawingRail {...base} />);
    fireEvent.click(screen.getByLabelText("hide all drawings"));
    expect(base.onToggleHideAll).toHaveBeenCalled();
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
