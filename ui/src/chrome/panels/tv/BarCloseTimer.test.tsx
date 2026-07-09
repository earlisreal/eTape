// @vitest-environment jsdom
// ui/src/chrome/panels/tv/BarCloseTimer.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, act } from "@testing-library/react";
import { BarCloseTimer } from "./BarCloseTimer";
import { getTvChrome } from "../../../render/chart/tvTheme";
import { remainingToBarCloseMs, formatCountdown } from "../../../render/chart/barClose";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("BarCloseTimer", () => {
  it("renders the live price above the mm:ss countdown to the current 1m bar's close", () => {
    vi.useFakeTimers();
    // 13:30:45Z is 45s into the 1m bucket starting at 13:30:00Z -> 15s remain
    vi.setSystemTime(new Date("2026-07-06T13:30:45Z"));
    render(
      <BarCloseTimer chrome={chrome} timeframe="1m" price="205.60" lastPriceY={100} rightAxisWidth={60} paneBottom={400} up={true} />,
    );
    const expected = formatCountdown(remainingToBarCloseMs("1m", Date.now()));
    expect(expected).toBe("0:15");
    expect(screen.getByTestId("bar-close-timer-price").textContent).toBe("205.60");
    expect(screen.getByTestId("bar-close-timer-countdown").textContent).toBe(expected);
    vi.useRealTimers();
  });

  it("decrements once per second as fake timers advance", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-06T13:30:45Z"));
    render(
      <BarCloseTimer chrome={chrome} timeframe="1m" price="205.60" lastPriceY={100} rightAxisWidth={60} paneBottom={400} up={true} />,
    );
    expect(screen.getByTestId("bar-close-timer-countdown").textContent).toBe("0:15");

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByTestId("bar-close-timer-countdown").textContent).toBe("0:14");

    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(screen.getByTestId("bar-close-timer-countdown").textContent).toBe("0:09");
    vi.useRealTimers();
  });

  it("formats an hour-scale remainder as h:mm:ss (delegates to formatCountdown, not re-implemented)", () => {
    vi.useFakeTimers();
    // D bucket starts at 04:00Z; at 13:30:00Z on the same day there are hours left.
    vi.setSystemTime(new Date("2026-07-06T13:30:00Z"));
    render(
      <BarCloseTimer chrome={chrome} timeframe="D" price="205.60" lastPriceY={100} rightAxisWidth={60} paneBottom={400} up={false} />,
    );
    const expected = formatCountdown(remainingToBarCloseMs("D", Date.now()));
    expect(expected).toMatch(/^\d+:\d{2}:\d{2}$/);
    expect(screen.getByTestId("bar-close-timer-countdown").textContent).toBe(expected);
    vi.useRealTimers();
  });

  it("tints the badge with the up color when the current bar is green", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-06T13:30:45Z"));
    render(
      <BarCloseTimer chrome={chrome} timeframe="1m" price="205.60" lastPriceY={100} rightAxisWidth={60} paneBottom={400} up={true} />,
    );
    const badge = screen.getByTestId("bar-close-timer");
    const div = document.createElement("div");
    div.style.background = chrome.up;
    expect(badge.style.background).toBe(div.style.background);
    vi.useRealTimers();
  });

  it("tints the badge with the down color when the current bar is red", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-06T13:30:45Z"));
    render(
      <BarCloseTimer chrome={chrome} timeframe="1m" price="205.60" lastPriceY={100} rightAxisWidth={60} paneBottom={400} up={false} />,
    );
    const badge = screen.getByTestId("bar-close-timer");
    const div = document.createElement("div");
    div.style.background = chrome.down;
    expect(badge.style.background).toBe(div.style.background);
    vi.useRealTimers();
  });

  it("clamps its position so it never spills past paneBottom", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-06T13:30:45Z"));
    // lastPriceY is placed right at the pane's bottom edge; the badge must clamp
    // upward rather than render past paneBottom.
    render(
      <BarCloseTimer chrome={chrome} timeframe="1m" price="205.60" lastPriceY={395} rightAxisWidth={60} paneBottom={400} up={true} />,
    );
    const badge = screen.getByTestId("bar-close-timer");
    const top = parseFloat(badge.style.top);
    expect(top).toBeLessThanOrEqual(400);
    vi.useRealTimers();
  });

  it("does not intercept pointer events", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-06T13:30:45Z"));
    render(
      <BarCloseTimer chrome={chrome} timeframe="1m" price="205.60" lastPriceY={100} rightAxisWidth={60} paneBottom={400} up={true} />,
    );
    expect(screen.getByTestId("bar-close-timer").style.pointerEvents).toBe("none");
    vi.useRealTimers();
  });
});
