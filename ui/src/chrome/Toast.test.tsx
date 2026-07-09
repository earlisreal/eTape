// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider, useToasts } from "./Toast";

function Raiser({ onApi }: { onApi: (api: ReturnType<typeof useToasts>) => void }) {
  const api = useToasts();
  onApi(api);
  return null;
}

function setup() {
  let api!: ReturnType<typeof useToasts>;
  const result = render(
    <ThemeProvider>
      <ToastProvider autoDismissMs={4000}>
        <Raiser onApi={(a) => (api = a)} />
      </ToastProvider>
    </ThemeProvider>,
  );
  return { api: () => api, unmount: result.unmount };
}

describe("Toast", () => {
  it("renders a pushed toast with its verbatim text", () => {
    vi.useFakeTimers();
    const { api } = setup();
    act(() => api().push({ level: "danger", text: "Blocked: venue disarmed" }));
    expect(screen.getByText("Blocked: venue disarmed")).toBeTruthy();
    vi.useRealTimers();
  });
  it("auto-dismisses a non-sticky toast after the interval; sticky stays", () => {
    vi.useFakeTimers();
    const { api } = setup();
    act(() => { api().push({ level: "info", text: "flash-order" }); api().push({ level: "danger", text: "stay", sticky: true }); });
    act(() => vi.advanceTimersByTime(4001));
    expect(screen.queryByText("flash-order")).toBeNull();
    expect(screen.getByText("stay")).toBeTruthy();
    vi.useRealTimers();
  });
  it("clears a toast's auto-dismiss timer when manually dismissed, so it doesn't fire later", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(globalThis, "clearTimeout");
    const { api } = setup();
    act(() => api().push({ level: "info", text: "manual-dismiss" }));
    const el = screen.getByText("manual-dismiss");
    act(() => el.click());
    expect(screen.queryByText("manual-dismiss")).toBeNull();
    expect(clearSpy).toHaveBeenCalled();
    // Advancing past the original auto-dismiss deadline must not throw or warn.
    expect(() => act(() => vi.advanceTimersByTime(5000))).not.toThrow();
    clearSpy.mockRestore();
    vi.useRealTimers();
  });
  it("clears all pending timers on unmount, leaving nothing scheduled", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(globalThis, "clearTimeout");
    const { api, unmount } = setup();
    act(() => api().push({ level: "info", text: "pending-on-unmount" }));
    unmount();
    expect(clearSpy).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    clearSpy.mockRestore();
    vi.useRealTimers();
  });
  // Regression/coverage: the alert row's hover feedback is a direct DOM
  // mutation (ev.currentTarget.style.background) in ToastHost, not React
  // state — untested until now. Uses `sticky` so the row can't auto-dismiss
  // out from under the assertions.
  it("mutates the alert row's background directly on hover, then restores it on mouseleave", () => {
    vi.useFakeTimers();
    const { api } = setup();
    act(() => api().push({ level: "danger", text: "hover-target", sticky: true }));
    const row = screen.getByRole("alert");
    const restBackground = row.style.background;

    fireEvent.mouseEnter(row);
    expect(row.style.background).not.toBe(restBackground);

    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe(restBackground);
    vi.useRealTimers();
  });
});
