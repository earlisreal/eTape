// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { DrawingRail } from "./DrawingRail";

beforeEach(() => cleanup());

function props(overrides?: Partial<Parameters<typeof DrawingRail>[0]>) {
  return {
    activeTool: "select" as const,
    magnet: true,
    symbol: "US.AAPL",
    onSelectTool: vi.fn(),
    onToggleMagnet: vi.fn(),
    hasSelection: vi.fn(() => false),
    onDeleteSelection: vi.fn(),
    onClearAll: vi.fn(),
    ...overrides,
  };
}

describe("DrawingRail", () => {
  it("renders one button per tool plus magnet and trash", () => {
    render(<DrawingRail {...props()} />);
    for (const label of ["select", "horizontal line", "horizontal ray", "trendline", "ray", "rectangle", "measure", "magnet", "delete"]) {
      expect(screen.getByLabelText(label)).toBeTruthy();
    }
  });

  it("selecting a tool calls onSelectTool", () => {
    const p = props();
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("trendline"));
    expect(p.onSelectTool).toHaveBeenCalledWith("trendline");
  });

  it("marks the active tool with aria-pressed", () => {
    render(<DrawingRail {...props({ activeTool: "rect" })} />);
    expect(screen.getByLabelText("rectangle").getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByLabelText("select").getAttribute("aria-pressed")).toBe("false");
  });

  it("reflects and toggles magnet", () => {
    const p = props({ magnet: true });
    render(<DrawingRail {...p} />);
    expect(screen.getByLabelText("magnet").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByLabelText("magnet"));
    expect(p.onToggleMagnet).toHaveBeenCalledOnce();
  });

  it("trash deletes the selection when one exists (no popover)", () => {
    const p = props({ hasSelection: vi.fn(() => true) });
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("delete"));
    expect(p.onDeleteSelection).toHaveBeenCalledOnce();
    expect(screen.queryByText(/Clear all drawings/i)).toBeNull();
    expect(p.onClearAll).not.toHaveBeenCalled();
  });

  it("trash with no selection opens a confirm popover naming the symbol; confirm clears all", () => {
    const p = props({ hasSelection: vi.fn(() => false) });
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("delete"));
    expect(screen.getByText(/Clear all drawings for US\.AAPL/i)).toBeTruthy();
    fireEvent.click(screen.getByText("Clear"));
    expect(p.onClearAll).toHaveBeenCalledOnce();
  });

  it("cancel dismisses the confirm popover without clearing", () => {
    const p = props({ hasSelection: vi.fn(() => false) });
    render(<DrawingRail {...p} />);
    fireEvent.click(screen.getByLabelText("delete"));
    fireEvent.click(screen.getByText("Cancel"));
    expect(p.onClearAll).not.toHaveBeenCalled();
    expect(screen.queryByText(/Clear all drawings/i)).toBeNull();
  });
});
