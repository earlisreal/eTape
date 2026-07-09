// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVDrawingRail.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVDrawingRail } from "./TVDrawingRail";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

// jsdom/cssstyle normalizes hex assignments to rgb() on readback; run every
// expected color through the same normalization so we're not hardcoding it.
const cssColor = (hex: string): string => {
  const div = document.createElement("div");
  div.style.color = hex;
  return div.style.color;
};

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

  it("active tool keeps its accent ring regardless of hover; inactive tool shows the plain hover overlay", () => {
    render(<TVDrawingRail {...base} activeTool="rect" />);
    const rectBtn = screen.getByLabelText("rectangle") as HTMLButtonElement;
    const measureBtn = screen.getByLabelText("measure") as HTMLButtonElement;

    // active, unhovered: accent ring + accent text on the hover-grey bg
    expect(rectBtn.style.boxShadow).toBe(`inset 0 0 0 1px ${chrome.accent}`);
    expect(rectBtn.style.background).toBe(cssColor(chrome.hover));
    expect(rectBtn.style.color).toBe(cssColor(chrome.accent));

    // hovering the armed tool is a no-visual-op relative to its own active look
    fireEvent.mouseEnter(rectBtn);
    expect(rectBtn.style.boxShadow).toBe(`inset 0 0 0 1px ${chrome.accent}`);
    expect(rectBtn.style.background).toBe(cssColor(chrome.hover));
    expect(rectBtn.style.color).toBe(cssColor(chrome.accent));
    fireEvent.mouseLeave(rectBtn);

    // inactive tool: no ring, transparent until hovered, then the plain grey/text overlay
    expect(measureBtn.style.boxShadow).toBe("none");
    expect(measureBtn.style.background).toBe("transparent");
    fireEvent.mouseEnter(measureBtn);
    expect(measureBtn.style.boxShadow).toBe("none");
    expect(measureBtn.style.background).toBe(cssColor(chrome.hover));
    expect(measureBtn.style.color).toBe(cssColor(chrome.text));
    fireEvent.mouseLeave(measureBtn);
    expect(measureBtn.style.background).toBe("transparent");
  });

  it("Cancel confirm button shows the plain hover overlay (genuinely neutral)", () => {
    render(<TVDrawingRail {...base} hasSelection={() => false} />);
    fireEvent.click(screen.getByLabelText("delete"));
    const cancelBtn = screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement;

    fireEvent.mouseEnter(cancelBtn);
    expect(cancelBtn.style.background).toBe(cssColor(chrome.hover));
    expect(cancelBtn.style.color).toBe(cssColor(chrome.text));
  });

  it("Clear confirm button keeps its danger color under hover (ring, not a flat wash)", () => {
    render(<TVDrawingRail {...base} hasSelection={() => false} />);
    fireEvent.click(screen.getByLabelText("delete"));
    const clearBtn = screen.getByRole("button", { name: "Clear" }) as HTMLButtonElement;
    const bgBefore = clearBtn.style.background;
    expect(bgBefore).toBe(cssColor(chrome.down));
    expect(clearBtn.style.color).toBe(cssColor("#fff"));

    fireEvent.mouseEnter(clearBtn);
    expect(clearBtn.style.background).toBe(bgBefore);
    expect(clearBtn.style.color).toBe(cssColor("#fff"));
    expect(clearBtn.style.boxShadow).toBe("inset 0 0 0 1px rgba(255,255,255,.5)");

    fireEvent.mouseLeave(clearBtn);
    expect(clearBtn.style.background).toBe(bgBefore);
    expect(clearBtn.style.boxShadow).toBe("");
  });
});
