// @vitest-environment jsdom
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { TopBar, targetCueFor } from "./TopBar";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";

const here = dirname(fileURLToPath(import.meta.url));

const wailsWindow = vi.hoisted(() => ({
  Minimise: vi.fn(async () => {}),
  ToggleMaximise: vi.fn(async () => {}),
  Close: vi.fn(async () => {}),
}));
vi.mock("@wailsio/runtime", () => ({
  Window: wailsWindow,
  Call: { ByName: vi.fn(async () => undefined) },
}));

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
  it("exposes accessible native caption controls without making them draggable", async () => {
    Object.defineProperty(window, "chrome", { configurable: true, value: { webview: {} } });
    try {
      render(<ThemeProvider><TopBar {...base} /></ThemeProvider>);
      expect(screen.getByRole("button", { name: "Minimise window" })).toBeTruthy();
      expect(screen.getByRole("button", { name: "Maximise or restore window" })).toBeTruthy();
      expect(screen.getByRole("button", { name: "Close window" })).toBeTruthy();
      expect(screen.getByTestId("native-window-controls").className).toContain("native-window-controls");

      fireEvent.click(screen.getByRole("button", { name: "Minimise window" }));
      fireEvent.click(screen.getByRole("button", { name: "Close window" }));
      expect(wailsWindow.Minimise).toHaveBeenCalled();
      expect(wailsWindow.Close).toHaveBeenCalled();
      fireEvent.click(screen.getByRole("button", { name: "Maximise or restore window" }));
      await waitFor(() => expect(wailsWindow.ToggleMaximise).toHaveBeenCalled());
    } finally {
      delete (window as typeof window & { chrome?: unknown }).chrome;
    }
  });
  it("keeps only unused Top Bar surfaces draggable", () => {
    const css = readFileSync(join(here, "..", "global.css"), "utf8");
    expect(css).toMatch(/\.top-bar \{[^}]*--wails-draggable:\s*drag/);
    expect(css).toMatch(/\.top-bar > \* \{[^}]*--wails-draggable:\s*no-drag/);
    expect(css).toMatch(/\.dockview-theme-light, \.dockview-theme-dark, \.ledger-header \{[^}]*--wails-draggable:\s*no-drag/);
  });
});
