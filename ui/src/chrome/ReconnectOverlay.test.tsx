// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
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
  it("renders children at full opacity with no overlay while open", () => {
    render(<Wrapped state="open" />);
    expect(screen.queryByText(/connecting…|reconnecting…/)).toBeNull();
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("1");
  });

  it("does not show the dim/overlay UI for a reconnect that recovers within the 600ms grace period", () => {
    vi.useFakeTimers();
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

    vi.useRealTimers();
  });

  it("shows the dimmed/overlay UI once a disconnect persists past the 600ms grace period", () => {
    vi.useFakeTimers();
    const { rerender } = render(<Wrapped state="open" />);

    act(() => rerender(<Wrapped state="reconnecting" />));
    expect(screen.queryByText("reconnecting…")).toBeNull(); // not immediate

    act(() => vi.advanceTimersByTime(599));
    expect(screen.queryByText("reconnecting…")).toBeNull(); // not yet

    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByText("reconnecting…")).toBeTruthy(); // exactly after grace period
    expect(screen.getByText("content").parentElement?.style.opacity).toBe("0.4");

    vi.useRealTimers();
  });

  it("shows connecting… text (not reconnecting…) for a persisted connecting state", () => {
    vi.useFakeTimers();
    render(<Wrapped state="connecting" />);
    act(() => vi.advanceTimersByTime(600));
    expect(screen.getByText("connecting…")).toBeTruthy();
    vi.useRealTimers();
  });

  it("clears the pending grace-period timer on unmount, leaving nothing scheduled", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(globalThis, "clearTimeout");
    const { unmount } = render(<Wrapped state="reconnecting" />);
    unmount();
    expect(clearSpy).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    clearSpy.mockRestore();
    vi.useRealTimers();
  });
});
