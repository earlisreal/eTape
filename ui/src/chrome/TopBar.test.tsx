// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TopBar } from "./TopBar";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";

const base = {
  workspaceName: "main", health: new HealthStore(), armed: false,
  onArmToggle: vi.fn(), onAddPanel: vi.fn(), onNewWindow: vi.fn(),
  onOpenSettings: vi.fn(), onOpenConnection: vi.fn(), onOpenReplay: vi.fn(),
};

describe("TopBar", () => {
  it("renders wordmark, workspace name, and the shell buttons", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.getByText("eTape")).toBeTruthy();
    expect(screen.getByText("· main")).toBeTruthy();
    expect(screen.getByRole("button", { name: /add panel/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /new window/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /practice/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /settings/i })).toBeTruthy();
  });
  it("arm chip reflects state and toggles", () => {
    render(<ThemeProvider><TopBar {...base} armed /></ThemeProvider>);
    const chip = screen.getByTestId("arm-chip");
    expect(chip.textContent).toContain("ARMED");
    fireEvent.click(chip);
    expect(base.onArmToggle).toHaveBeenCalled();
  });
  it("has no link-group symbol boxes", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.queryByLabelText(/focus green/i)).toBeNull();
  });
  it("renders the ET session clock in the center", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.getByTestId("session-clock")).toBeTruthy();
  });
});
