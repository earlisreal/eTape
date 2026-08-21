// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVFloatingToolbar.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVFloatingToolbar } from "./TVFloatingToolbar";
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
  chrome, kind: "trendline" as const, rect: { x: 100, y: 80, w: 60, h: 20 }, color: "#2962FF", width: 1, lineStyle: "solid" as const,
  fill: false, fillColor: "#2962FF", fillOpacity: 20,
  onColor: vi.fn(), onWidth: vi.fn(), onLineStyle: vi.fn(), onFill: vi.fn(), onFillColor: vi.fn(), onFillOpacity: vi.fn(), onClone: vi.fn(), onDelete: vi.fn(),
};

describe("TVFloatingToolbar", () => {
  it("starts above the selected drawing and drags via its grip", () => {
    const { container } = render(<TVFloatingToolbar {...base} />);
    const toolbar = container.querySelector("[data-drawing-ui]") as HTMLDivElement;
    expect(toolbar.style.left).toBe("130px");
    expect(toolbar.style.top).toBe("40px");

    const grip = screen.getByLabelText("move toolbar");
    // jsdom has no layout, so dragging clamps to the host's 0,0 bounds.
    fireEvent(grip, new MouseEvent("pointerdown", { bubbles: true, clientX: 100, clientY: 50 }));
    fireEvent(grip, new MouseEvent("pointermove", { bubbles: true, clientX: 140, clientY: 90 }));
    fireEvent(grip, new MouseEvent("pointerup", { bubbles: true }));
    expect(toolbar.style.left).toBe("0px");
    expect(toolbar.style.top).toBe("0px");
  });

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

  it("shows and edits fill settings for rectangles only", () => {
    const rectBase = { ...base, kind: "rect" as const, onFill: vi.fn(), onFillColor: vi.fn(), onFillOpacity: vi.fn() };
    render(<TVFloatingToolbar {...rectBase} />);
    expect(screen.getByLabelText("fill").getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(screen.getByLabelText("fill"));
    fireEvent.click(screen.getByLabelText("fill color"));
    fireEvent.click(screen.getByLabelText("fill color #F23645"));
    fireEvent.change(screen.getByLabelText("fill opacity"), { target: { value: "45" } });
    expect(rectBase.onFill).toHaveBeenCalledWith(true);
    expect(rectBase.onFillColor).toHaveBeenCalledWith("#F23645");
    expect(rectBase.onFillOpacity).toHaveBeenCalledWith(45);

    cleanup();
    render(<TVFloatingToolbar {...base} />);
    expect(screen.queryByLabelText("fill")).toBeNull();
  });

  it("hovering the color swatch shows a ring, not a background swap", () => {
    render(<TVFloatingToolbar {...base} />);
    const swatch = screen.getByLabelText("color") as HTMLButtonElement;
    const bgBefore = swatch.style.background;
    expect(bgBefore).toBe(cssColor(base.color));

    fireEvent.mouseEnter(swatch);
    expect(swatch.style.background).toBe(bgBefore);
    expect(swatch.style.boxShadow).toBe(`inset 0 0 0 2px ${chrome.hover}`);

    fireEvent.mouseLeave(swatch);
    expect(swatch.style.background).toBe(bgBefore);
    expect(swatch.style.boxShadow).toBe("");
  });

  it("hovering a preset swatch shows a ring, not a background swap", () => {
    render(<TVFloatingToolbar {...base} />);
    fireEvent.click(screen.getByLabelText("color"));
    const preset = screen.getByLabelText("color #F23645") as HTMLButtonElement;
    const bgBefore = preset.style.background;
    expect(bgBefore).toBe(cssColor("#F23645"));

    fireEvent.mouseEnter(preset);
    expect(preset.style.background).toBe(bgBefore);
    expect(preset.style.boxShadow).toBe(`inset 0 0 0 2px ${chrome.hover}`);
  });

  it("clone/delete icon buttons show the standard hover overlay", () => {
    render(<TVFloatingToolbar {...base} />);
    const cloneBtn = screen.getByLabelText("clone") as HTMLButtonElement;
    const deleteBtn = screen.getByLabelText("delete drawing") as HTMLButtonElement;

    for (const btn of [cloneBtn, deleteBtn]) {
      fireEvent.mouseEnter(btn);
      expect(btn.style.background).toBe(cssColor(chrome.hover));
      expect(btn.style.color).toBe(cssColor(chrome.text));
      fireEvent.mouseLeave(btn);
      expect(btn.style.background).toBe("transparent");
    }
  });

  it("selected width button keeps its bold weight under hover; accent color intentionally flattens like other buttons", () => {
    render(<TVFloatingToolbar {...base} width={2} />);
    const widthBtn = screen.getByLabelText("width 2") as HTMLButtonElement;
    expect(widthBtn.style.fontWeight).toBe("700");
    expect(widthBtn.style.color).toBe(cssColor(chrome.accent));

    fireEvent.mouseEnter(widthBtn);
    expect(widthBtn.style.background).toBe(cssColor(chrome.hover));
    expect(widthBtn.style.fontWeight).toBe("700");
  });
});
