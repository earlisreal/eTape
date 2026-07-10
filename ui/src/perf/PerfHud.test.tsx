// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, act } from "@testing-library/react";
import { PerfHud } from "./PerfHud";
import { PerfMonitor, perf } from "./PerfMonitor";

beforeEach(() => {
  vi.useFakeTimers();
  perf.disable();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  perf.disable();
});

describe("PerfHud", () => {
  it("shows a disabled placeholder when perf is off", () => {
    render(<PerfHud />);
    expect(screen.getByTestId("perf-hud").textContent).toMatch(/disabled/i);
  });

  it("renders the current snapshot's numbers once perf is enabled", () => {
    perf.enable();
    perf.recordPaint("tape:t1", 4.2);
    perf.countMessage("md.tape");
    render(<PerfHud />);
    const text = screen.getByTestId("perf-hud").textContent!;
    expect(text).toMatch(/ws \d+\/s/);
    expect(text).toContain("tape:t1");
  });

  it("polls snapshot() on a ~250ms interval, not every frame", () => {
    const spy = vi.spyOn(perf, "snapshot");
    perf.enable();
    render(<PerfHud />);
    const callsAtMount = spy.mock.calls.length;
    act(() => { vi.advanceTimersByTime(250); });
    expect(spy.mock.calls.length).toBe(callsAtMount + 1);
    act(() => { vi.advanceTimersByTime(250); });
    expect(spy.mock.calls.length).toBe(callsAtMount + 2);
    spy.mockRestore();
  });

  it("stops polling after unmount (no leaked interval)", () => {
    perf.enable();
    const spy = vi.spyOn(perf, "snapshot");
    const { unmount } = render(<PerfHud />);
    unmount();
    const callsAfterUnmount = spy.mock.calls.length;
    act(() => { vi.advanceTimersByTime(1000); });
    expect(spy.mock.calls.length).toBe(callsAfterUnmount);
    spy.mockRestore();
  });

  it("does not throw when a fresh PerfMonitor instance (unused by the singleton) has no stats yet", () => {
    const fresh = new PerfMonitor();
    fresh.enable();
    expect(fresh.snapshot().paint).toEqual({});
  });
});
