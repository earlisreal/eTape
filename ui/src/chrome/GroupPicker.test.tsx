// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { GroupPicker } from "./GroupPicker";
import { ThemeProvider } from "./ThemeProvider";

describe("GroupPicker", () => {
  it("lists the four groups + pinned and reports the pick", () => {
    const onPick = vi.fn();
    render(<ThemeProvider><GroupPicker group="blue" onPick={onPick} onClose={() => {}} /></ThemeProvider>);
    // Codebase convention (see Catalog.test.tsx / TopBar.test.tsx): plain vitest/chai
    // matchers — @testing-library/jest-dom (toBeInTheDocument) isn't installed here.
    expect(screen.getByText(/red group/i)).toBeTruthy();
    expect(screen.getByText(/pinned/i)).toBeTruthy();
    fireEvent.click(screen.getByText(/green group/i));
    expect(onPick).toHaveBeenCalledWith("green");
    fireEvent.click(screen.getByText(/pinned/i));
    expect(onPick).toHaveBeenCalledWith(null);
  });

  it("hovering an unselected group row shows the hover tint, cleared on mouse-leave", () => {
    render(<ThemeProvider><GroupPicker group="blue" onPick={() => {}} onClose={() => {}} /></ThemeProvider>);
    const row = screen.getByText(/red group/i).closest('[role="button"]') as HTMLElement;
    expect(row.style.background).toBe("transparent");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe("rgba(154, 106, 27, 0.06)");
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe("transparent");
  });

  it("hovering the selected row leaves its selected background unchanged", () => {
    render(<ThemeProvider><GroupPicker group="blue" onPick={() => {}} onClose={() => {}} /></ThemeProvider>);
    const row = screen.getByText(/blue group/i).closest('[role="button"]') as HTMLElement;
    const selectedBackground = row.style.background;
    expect(selectedBackground).not.toBe("transparent");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe(selectedBackground);
  });

  it("hovering the pinned row uses the same hover tint as a group row, independent of the null sentinel", () => {
    render(<ThemeProvider><GroupPicker group="blue" onPick={() => {}} onClose={() => {}} /></ThemeProvider>);
    const pinnedRow = screen.getByText(/pinned/i).closest('[role="button"]') as HTMLElement;
    expect(pinnedRow.style.background).toBe("transparent");
    fireEvent.mouseEnter(pinnedRow);
    expect(pinnedRow.style.background).toBe("rgba(154, 106, 27, 0.06)");
    fireEvent.mouseLeave(pinnedRow);
    expect(pinnedRow.style.background).toBe("transparent");
  });
});
