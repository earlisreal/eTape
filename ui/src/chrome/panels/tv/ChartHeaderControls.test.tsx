// @vitest-environment jsdom
// ui/src/chrome/panels/tv/ChartHeaderControls.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { ChartHeaderControls, TIMEFRAMES } from "./ChartHeaderControls";
import { LIGHT } from "../../../render/palette";

afterEach(cleanup);
const base = {
  palette: LIGHT, timeframe: "1m",
  onTimeframe: vi.fn(), onAddIndicator: vi.fn(), onScreenshot: vi.fn(), onOpenSettings: vi.fn(),
};

describe("ChartHeaderControls", () => {
  it("renders all 9 timeframe buttons and marks the active one", () => {
    render(<ChartHeaderControls {...base} />);
    for (const tf of TIMEFRAMES) expect(screen.getByRole("button", { name: `timeframe ${tf}` })).toBeTruthy();
    const active = screen.getByRole("button", { name: "timeframe 1m" });
    expect(active.getAttribute("aria-pressed")).toBe("true");
    expect(active.style.fontWeight).toBe("700");
    const inactive = screen.getByRole("button", { name: "timeframe 5m" });
    expect(inactive.style.fontWeight).toBe("500");
    // jsdom normalizes inline hex colors to rgb() — compare colors, not styling identity.
    expect(active.style.color).not.toBe(inactive.style.color);
  });

  it("fires callbacks for timeframe, screenshot, settings", () => {
    render(<ChartHeaderControls {...base} />);
    fireEvent.click(screen.getByRole("button", { name: "timeframe 5m" }));
    fireEvent.click(screen.getByRole("button", { name: "screenshot" }));
    fireEvent.click(screen.getByRole("button", { name: "chart settings" }));
    expect(base.onTimeframe).toHaveBeenCalledWith("5m");
    expect(base.onScreenshot).toHaveBeenCalled();
    expect(base.onOpenSettings).toHaveBeenCalled();
  });

  it("opens an indicator dropdown on click, and picking an entry adds it and closes the dropdown", () => {
    render(<ChartHeaderControls {...base} />);
    const trigger = screen.getByRole("button", { name: "indicators" });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByRole("button", { name: "add EMA" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    expect(base.onAddIndicator).toHaveBeenCalledWith("EMA");
    expect(screen.queryByRole("button", { name: "add EMA" })).toBeNull();
  });

  it("has no symbol button — the ledger header it portals into already shows the symbol", () => {
    render(<ChartHeaderControls {...base} />);
    expect(screen.queryByRole("button", { name: /symbol/i })).toBeNull();
  });
});
