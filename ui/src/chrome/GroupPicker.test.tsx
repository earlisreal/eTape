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
});
