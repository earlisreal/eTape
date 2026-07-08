// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { ReconnectOverlay } from "./ReconnectOverlay";
import { ThemeProvider } from "./ThemeProvider";
import type { ConnState } from "../wire/WsClient";

function Wrapped({ state }: { state: ConnState }) {
  return (
    <ThemeProvider>
      <ReconnectOverlay state={state}>
        <div>content</div>
      </ReconnectOverlay>
    </ThemeProvider>
  );
}

describe("ReconnectOverlay", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders children at full opacity with no overlay while open", () => {
    render(<Wrapped state="open" />);
    expect(screen.queryByText(/connecting…|reconnecting…/)).toBeNull();
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("1");
  });

  it("does not show the dim/overlay UI for a reconnect that recovers within the 600ms grace period", () => {
    const { rerender } = render(<Wrapped state="open" />);

    act(() => rerender(<Wrapped state="reconnecting" />));
    // Still well inside the grace window.
    act(() => vi.advanceTimersByTime(300));
    expect(screen.queryByText("reconnecting…")).toBeNull();
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("1");

    // Recovers before the 600ms grace period elapses.
    act(() => rerender(<Wrapped state="open" />));
    // Even waiting past the original deadline must never show the overlay.
    act(() => vi.advanceTimersByTime(600));
    expect(screen.queryByText("reconnecting…")).toBeNull();
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("1");
  });

  it("shows the dimmed/overlay UI once a disconnect persists past the 600ms grace period", () => {
    const { rerender } = render(<Wrapped state="open" />);

    act(() => rerender(<Wrapped state="reconnecting" />));
    expect(screen.queryByText("reconnecting…")).toBeNull(); // not immediate

    act(() => vi.advanceTimersByTime(599));
    expect(screen.queryByText("reconnecting…")).toBeNull(); // not yet

    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByText("reconnecting…")).toBeTruthy(); // exactly after grace period
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("0.4");
  });

  it("shows connecting… text (not reconnecting…) for a persisted connecting state", () => {
    render(<Wrapped state="connecting" />);
    act(() => vi.advanceTimersByTime(600));
    expect(screen.getByText("connecting…")).toBeTruthy();
  });

  it("clears the pending grace-period timer on unmount, leaving nothing scheduled", () => {
    const clearSpy = vi.spyOn(globalThis, "clearTimeout");
    const { unmount } = render(<Wrapped state="reconnecting" />);
    unmount();
    expect(clearSpy).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    clearSpy.mockRestore();
  });

  it("still shows the overlay past 600ms of total outage even when an intermediate non-open state change occurs (reconnecting -> connecting churn must not reset the grace timer)", () => {
    const { rerender } = render(<Wrapped state="open" />);

    // t=0: leaves "open" -> starts the 600ms grace timer.
    act(() => rerender(<Wrapped state="reconnecting" />));

    // t=550: still inside the old timer's window, but the retry has moved on
    // to "connecting". Under the bug this re-keyed the effect on the raw
    // state string and restarted the 600ms countdown from here.
    act(() => vi.advanceTimersByTime(550));
    act(() => rerender(<Wrapped state="connecting" />));
    expect(screen.queryByText(/connecting…|reconnecting…/)).toBeNull();

    // t=600 total elapsed since leaving "open": the overlay must show,
    // proving the timer was never reset by the reconnecting->connecting churn.
    act(() => vi.advanceTimersByTime(50));
    expect(screen.getByText("connecting…")).toBeTruthy();
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("0.4");
  });

  it("never shows the overlay for a fast reconnect that churns through non-open states and recovers within 600ms total", () => {
    const { rerender } = render(<Wrapped state="open" />);

    act(() => rerender(<Wrapped state="reconnecting" />));
    act(() => vi.advanceTimersByTime(200));
    act(() => rerender(<Wrapped state="connecting" />));
    act(() => vi.advanceTimersByTime(200));

    // Recovers at t=400, well within the 600ms grace window.
    act(() => rerender(<Wrapped state="open" />));

    // Waiting well past the original deadline must never show the overlay.
    act(() => vi.advanceTimersByTime(600));
    expect(screen.queryByText(/connecting…|reconnecting…/)).toBeNull();
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("1");
  });
});
