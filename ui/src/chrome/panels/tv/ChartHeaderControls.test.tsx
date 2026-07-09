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

// jsdom/cssstyle normalizes hex assignments to rgb() on readback; run every
// expected color through the same normalization so we're not hardcoding it.
const cssColor = (hex: string): string => {
  const div = document.createElement("div");
  div.style.color = hex;
  return div.style.color;
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
    expect(screen.getByPlaceholderText("Search")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    expect(base.onAddIndicator).toHaveBeenCalledWith("EMA");
    expect(screen.queryByPlaceholderText("Search")).toBeNull();
  });

  it("has no symbol button — the ledger header it portals into already shows the symbol", () => {
    render(<ChartHeaderControls {...base} />);
    expect(screen.queryByRole("button", { name: /symbol/i })).toBeNull();
  });

  // Task 4: hovering the active timeframe must keep its accent color (not flatten
  // to plain text), while hovering an inactive one brightens muted -> full text.
  it("hovering the active timeframe preserves accent color; hovering an inactive one brightens to full text", () => {
    render(<ChartHeaderControls {...base} />);
    const active = screen.getByRole("button", { name: "timeframe 1m" }) as HTMLButtonElement;
    const inactive = screen.getByRole("button", { name: "timeframe 5m" }) as HTMLButtonElement;

    fireEvent.mouseEnter(active);
    expect(active.style.background).toBe(cssColor(LIGHT.surface));
    expect(active.style.color).toBe(cssColor(LIGHT.accent));
    fireEvent.mouseLeave(active);

    fireEvent.mouseEnter(inactive);
    expect(inactive.style.background).toBe(cssColor(LIGHT.surface));
    expect(inactive.style.color).toBe(cssColor(LIGHT.text));
    expect(inactive.style.color).not.toBe(cssColor(LIGHT.textMuted));
  });

  it("hovering the indicators trigger and the screenshot/settings icon buttons applies the palette-derived overlay", () => {
    render(<ChartHeaderControls {...base} />);
    const indicators = screen.getByRole("button", { name: "indicators" }) as HTMLButtonElement;
    const screenshot = screen.getByRole("button", { name: "screenshot" }) as HTMLButtonElement;
    const settings = screen.getByRole("button", { name: "chart settings" }) as HTMLButtonElement;

    for (const btn of [indicators, screenshot, settings]) {
      fireEvent.mouseEnter(btn);
      expect(btn.style.background).toBe(cssColor(LIGHT.surface));
      expect(btn.style.color).toBe(cssColor(LIGHT.text));
      fireEvent.mouseLeave(btn);
    }
  });
});
