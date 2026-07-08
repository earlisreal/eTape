// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVToolbar.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVToolbar, TIMEFRAMES } from "./TVToolbar";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");
const base = {
  chrome, symbol: "US.AAPL", timeframe: "1m",
  onSymbolClick: vi.fn(), onTimeframe: vi.fn(),
  onOpenIndicators: vi.fn(), onScreenshot: vi.fn(), onOpenSettings: vi.fn(),
};

describe("TVToolbar", () => {
  it("renders all 9 timeframe buttons and marks the active one", () => {
    render(<TVToolbar {...base} />);
    for (const tf of TIMEFRAMES) expect(screen.getByRole("button", { name: `timeframe ${tf}` })).toBeTruthy();
    expect(screen.getByRole("button", { name: "timeframe 1m" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("fires callbacks for timeframe, symbol, indicators, screenshot, settings", () => {
    render(<TVToolbar {...base} />);
    fireEvent.click(screen.getByRole("button", { name: "timeframe 5m" }));
    fireEvent.click(screen.getByRole("button", { name: /symbol AAPL/i }));
    fireEvent.click(screen.getByRole("button", { name: "indicators" }));
    fireEvent.click(screen.getByRole("button", { name: "screenshot" }));
    fireEvent.click(screen.getByRole("button", { name: "chart settings" }));
    expect(base.onTimeframe).toHaveBeenCalledWith("5m");
    expect(base.onSymbolClick).toHaveBeenCalled();
    expect(base.onOpenIndicators).toHaveBeenCalled();
    expect(base.onScreenshot).toHaveBeenCalled();
    expect(base.onOpenSettings).toHaveBeenCalled();
  });

  it("has no chart-type switcher and no search icon in the symbol button", () => {
    const { container } = render(<TVToolbar {...base} />);
    expect(screen.queryByRole("button", { name: "chart type" })).toBeNull();
    // The magnifier was decorative (the button only focuses the chart) — gone.
    expect(container.querySelector('button[aria-label="symbol AAPL"] svg')).toBeNull();
  });
});
