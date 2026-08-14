// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TopBar, targetCueFor } from "./TopBar";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";

const base = {
  workspaceName: "main", health: new HealthStore(), armed: false,
  onArmToggle: vi.fn(), onAddPanel: vi.fn(), onNewWindow: vi.fn(),
  onOpenSettings: vi.fn(), onOpenConnection: vi.fn(), onOpenPractice: vi.fn(),
};

describe("TopBar", () => {
  it("renders wordmark, workspace name, and the shell buttons", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.getByText("eTape")).toBeTruthy();
    expect(screen.queryByText("· main")).toBeNull();
    expect(screen.getByRole("button", { name: /add panel/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /new window/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /practice/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /settings/i })).toBeTruthy();
  });
  it("arm chip reflects state and toggles", () => {
    render(<ThemeProvider><TopBar {...base} armed /></ThemeProvider>);
    const chip = screen.getByTestId("arm-chip");
    expect(chip.textContent).toContain("LOCK TRADING");
    fireEvent.click(chip);
    expect(base.onArmToggle).toHaveBeenCalled();
  });
  it("has no link-group symbol boxes", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    expect(screen.queryByLabelText(/focus green/i)).toBeNull();
  });
  it("renders the ET clock after latency and the hotkey cue in the center", () => {
    render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
    const bar = screen.getByTestId("top-bar");
    const clock = screen.getByTestId("session-clock");
    const cue = screen.getByTestId("hotkey-target-cue");
    const left = bar.querySelector(".top-bar-left")!;
    const children = Array.from(left.children);
    expect(left.contains(clock)).toBe(true);
    expect(children.indexOf(left.querySelector(".top-bar-latency")!)).toBeLessThan(children.indexOf(clock));
    expect(bar.querySelector(".top-bar-target")?.contains(cue)).toBe(true);
    expect(bar.querySelector(".top-bar-actions")?.contains(cue)).toBe(false);
  });
  it("renders every target cue state without making the cue interactive", () => {
    const cases = [
      [targetCueFor(null), "no-target"],
      [targetCueFor({ ownerWindow: "a", panel: "p", group: null, symbol: "US.AAPL", venue: "sim-paper", revision: 1 }), "ungrouped"],
      [targetCueFor({ ownerWindow: "a", panel: "p", group: "blue", venue: "sim-paper", revision: 1 }), "missing-symbol"],
      [targetCueFor({ ownerWindow: "a", panel: "p", group: "blue", symbol: "US.AAPL", revision: 1 }), "missing-venue"],
      [targetCueFor({ ownerWindow: "a", panel: "p", group: "blue", symbol: "US.AAPL", venue: "sim-paper", revision: 1 }), "ready"],
    ] as const;
    for (const [cue, state] of cases) {
      const view = render(<ThemeProvider><TopBar {...base} targetCue={cue} /></ThemeProvider>);
      const element = screen.getByTestId("hotkey-target-cue");
      expect(element.getAttribute("data-state")).toBe(state);
      expect(element.tagName).toBe("SPAN");
      view.unmount();
    }
  });
});
