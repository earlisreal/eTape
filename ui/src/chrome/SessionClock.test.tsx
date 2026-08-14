// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { SessionClock } from "./SessionClock";
import { ThemeProvider } from "./ThemeProvider";

describe("SessionClock", () => {
  it("renders the ET wall-clock time and next phase during market hours", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-09T14:30:00Z")); // 10:30:00 EDT -> RTH
    render(<ThemeProvider><SessionClock /></ThemeProvider>);
    const text = screen.getByTestId("session-clock").textContent;
    expect(text).toContain("10:30:00");
    expect(text).toContain("POST in 05:30:00");
    vi.useRealTimers();
  });

  it("ticks forward each second", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-09T14:30:00Z"));
    render(<ThemeProvider><SessionClock /></ThemeProvider>);
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    const text = screen.getByTestId("session-clock").textContent;
    expect(text).toContain("10:30:01");
    expect(text).toContain("POST in 05:29:59");
    vi.useRealTimers();
  });

  it("counts to PRE before the trading day", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-09T06:00:00Z")); // 02:00 EDT -> before pre-market, closed
    render(<ThemeProvider><SessionClock /></ThemeProvider>);
    expect(screen.getByTestId("session-clock").textContent).toContain("PRE in 02:00:00");
    vi.useRealTimers();
  });

  it("uses day formatting for a weekend wait", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-11T01:00:00Z")); // Friday 21:00 EDT -> Monday PRE
    render(<ThemeProvider><SessionClock /></ThemeProvider>);
    expect(screen.getByTestId("session-clock").textContent).toContain("PRE in 2d 07:00:00");
    vi.useRealTimers();
  });
});
