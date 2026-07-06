// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider, useToasts } from "./Toast";

function Raiser({ onApi }: { onApi: (api: ReturnType<typeof useToasts>) => void }) {
  const api = useToasts();
  onApi(api);
  return null;
}

function setup() {
  let api!: ReturnType<typeof useToasts>;
  render(
    <ThemeProvider>
      <ToastProvider autoDismissMs={4000}>
        <Raiser onApi={(a) => (api = a)} />
      </ToastProvider>
    </ThemeProvider>,
  );
  return () => api;
}

describe("Toast", () => {
  it("renders a pushed toast with its verbatim text", () => {
    const api = setup();
    act(() => api().push({ level: "danger", text: "Blocked: venue disarmed" }));
    expect(screen.getByText("Blocked: venue disarmed")).toBeTruthy();
  });
  it("auto-dismisses a non-sticky toast after the interval; sticky stays", () => {
    vi.useFakeTimers();
    const api = setup();
    act(() => { api().push({ level: "info", text: "flash-order" }); api().push({ level: "danger", text: "stay", sticky: true }); });
    act(() => vi.advanceTimersByTime(4001));
    expect(screen.queryByText("flash-order")).toBeNull();
    expect(screen.getByText("stay")).toBeTruthy();
    vi.useRealTimers();
  });
});
