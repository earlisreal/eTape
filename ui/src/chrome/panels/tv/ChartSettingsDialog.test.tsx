// @vitest-environment jsdom
// ui/src/chrome/panels/tv/ChartSettingsDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { ChartSettingsDialog, DEFAULT_CHART_SETTINGS } from "./ChartSettingsDialog";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("ChartSettingsDialog", () => {
  it("defaults expose the four toggles on, watermark off", () => {
    expect(DEFAULT_CHART_SETTINGS).toEqual({ sessionShading: true, grid: true, volume: true, watermark: false });
  });

  it("shows the read-only ET timezone", () => {
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={() => {}} />);
    expect(screen.getByText("ET")).toBeTruthy();
  });

  it("applies flipped toggles on Ok", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByLabelText("grid"));
    fireEvent.click(screen.getByLabelText("symbol watermark"));
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith({ sessionShading: true, grid: false, volume: true, watermark: true });
  });
});
