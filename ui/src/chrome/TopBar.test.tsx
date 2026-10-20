// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { TopBar, targetCueFor } from "./TopBar";
import { HealthStore } from "../data/HealthStore";
import { ThemeProvider } from "./ThemeProvider";
import type { VenueStatus } from "../wire/contract";

const venueStatus: VenueStatus = {
  venue: "sim-paper", broker: "alpaca", env: "live", connected: false,
  reconcilePending: false, positionDataReady: false, flattenPending: false, note: "disconnected", lastReconcileMs: null,
  gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
};

const base = {
  workspaceName: "main", health: new HealthStore(), armed: false,
  onArmToggle: vi.fn(), onAddPanel: vi.fn(), onNewWindow: vi.fn(),
  onOpenSettings: vi.fn(), onOpenConnection: vi.fn(), onOpenPractice: vi.fn(),
};

describe("TopBar", () => {
  it.each<{ broker: VenueStatus["broker"]; env?: string; label: string | null }>([
    { broker: "alpaca", env: "live", label: "LIVE" },
    { broker: "moomoo", env: "LiVe", label: "LIVE" },
    { broker: "sim", env: "live", label: "LIVE" },
    { broker: "alpaca", env: "paper", label: "PAPER" },
    { broker: "sim", env: "PAPER", label: "SIM" },
    { broker: "alpaca", label: null },
    { broker: "sim", env: "", label: null },
    { broker: "moomoo", env: "unknown", label: null },
  ])("shows $label for $broker/$env using metadata rather than the venue ID", ({ broker, env, label }) => {
    const metadata = { ...venueStatus, broker };
    if (env === undefined) delete metadata.env;
    else metadata.env = env;
    render(<ThemeProvider><TopBar {...base} targetVenue={metadata} targetCue={targetCueFor({
      ownerWindow: "a", panel: "p", group: "blue", symbol: "US.NVDA", venue: "sim-paper", revision: 1,
    })} /></ThemeProvider>);
    const badge = screen.queryByTestId("venue-environment-badge");
    if (!label) {
      expect(badge).toBeNull();
      return;
    }
    expect(badge?.textContent).toBe(label);
    expect(badge?.classList.contains(label === "LIVE" ? "chip-live" : "chip-neutral")).toBe(true);
    expect(screen.getByTestId("hotkey-target-cue").getAttribute("aria-label")).toBe(`NVDA · sim-paper · ${label}`);
    expect(screen.getByTestId("hotkey-target-cue").getAttribute("title")).toContain(label);
    expect(badge?.getAttribute("aria-label")).toBe(badge?.getAttribute("title"));
    if (label === "LIVE") expect(badge?.getAttribute("aria-label")).toContain("real-money account");
  });

  it("drops stale metadata on target changes and keeps LIVE independent of the trading lock", () => {
    const cue = targetCueFor({ ownerWindow: "a", panel: "p", group: "blue", symbol: "US.NVDA", venue: "sim-paper", revision: 1 });
    const view = render(<ThemeProvider><TopBar {...base} armed targetCue={cue} targetVenue={venueStatus} /></ThemeProvider>);
    expect(screen.getByTestId("venue-environment-badge").textContent).toBe("LIVE");
    view.rerender(<ThemeProvider><TopBar {...base} targetCue={cue} targetVenue={venueStatus} /></ThemeProvider>);
    expect(screen.getByTestId("venue-environment-badge").textContent).toBe("LIVE");
    view.rerender(<ThemeProvider><TopBar {...base} targetCue={cue} targetVenue={{ ...venueStatus, venue: "other" }} /></ThemeProvider>);
    expect(screen.queryByTestId("venue-environment-badge")).toBeNull();
    view.rerender(<ThemeProvider><TopBar {...base} targetCue={cue} /></ThemeProvider>);
    expect(screen.queryByTestId("venue-environment-badge")).toBeNull();
  });

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
      const view = render(<ThemeProvider><TopBar {...base} targetCue={cue} targetVenue={venueStatus} /></ThemeProvider>);
      const element = screen.getByTestId("hotkey-target-cue");
      expect(element.getAttribute("data-state")).toBe(state);
      expect(element.tagName).toBe("SPAN");
      expect(screen.queryByTestId("venue-environment-badge") !== null).toBe(state === "ready" || state === "missing-symbol");
      view.unmount();
    }
  });
  it("queries the exact target venue and renders only supplied permissions in T-M-S order", async () => {
    const commands = {
      sendQuery: vi.fn(async () => ({
        supported: true, found: true, tradable: true, marginable: false, shortable: null, error: "",
      })),
    };
    render(<ThemeProvider><TopBar {...base} commands={commands} targetVenue={{ ...venueStatus, venue: "alpaca-paper" }} targetCue={targetCueFor({
      ownerWindow: "a", panel: "p", group: "green", symbol: "US.AAPL", venue: "alpaca-paper", revision: 1,
    })} /></ThemeProvider>);

    await waitFor(() => expect(commands.sendQuery).toHaveBeenCalledWith(
      "QueryVenueInstrumentEligibility", { venue: "alpaca-paper", symbol: "US.AAPL" },
    ));
    const group = await screen.findByTestId("venue-instrument-eligibility");
    expect(group.textContent).toBe("TM");
    expect(group.previousElementSibling).toBe(screen.getByTestId("venue-environment-badge"));
    expect(screen.getByLabelText("Tradable: Yes").getAttribute("title")).toBe("Tradable: Yes");
    expect(screen.getByLabelText("Marginable: No").getAttribute("title")).toBe("Marginable: No");
    expect(screen.queryByLabelText(/Shortable:/)).toBeNull();
  });
  it("ignores a response for an old target", async () => {
    const pending: Array<(value: unknown) => void> = [];
    const commands = {
      sendQuery: vi.fn(() => new Promise<unknown>((resolve) => pending.push(resolve))),
    };
    const target = (symbol: string) => targetCueFor({
      ownerWindow: "a", panel: "p", group: "green", symbol, venue: "alpaca-paper", revision: 1,
    });
    const view = render(<ThemeProvider><TopBar {...base} commands={commands} targetCue={target("US.AAPL")} /></ThemeProvider>);
    await waitFor(() => expect(pending).toHaveLength(1));
    view.rerender(<ThemeProvider><TopBar {...base} commands={commands} targetCue={target("US.TSLA")} /></ThemeProvider>);
    await waitFor(() => expect(pending).toHaveLength(2));

    await act(async () => pending[0]({ supported: true, found: true, tradable: true, marginable: null, shortable: null, error: "" }));
    expect(screen.queryByTestId("venue-instrument-eligibility")).toBeNull();
    await act(async () => pending[1]({ supported: true, found: true, tradable: false, marginable: null, shortable: null, error: "" }));
    expect(await screen.findByLabelText("Tradable: No")).toBeTruthy();
  });
});
