// @vitest-environment jsdom
// ui/src/chrome/panels/tv/ChartSettingsDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { ChartSettingsDialog, DEFAULT_CHART_SETTINGS, chartSettingsRollbackProjection, normalizeChartSettings } from "./ChartSettingsDialog";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("ChartSettingsDialog", () => {
  it("defaults expose five toggles; Volume is an indicator", () => {
    expect(DEFAULT_CHART_SETTINGS).toEqual({ sessionShading: true, grid: true, watermark: false, barCloseTimer: true, visibleExtrema: true });
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
    expect(onApply).toHaveBeenCalledWith({ sessionShading: true, grid: false, watermark: true, barCloseTimer: true, visibleExtrema: true });
  });

  it("toggles bar-close timer on and off", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByLabelText("bar-close timer"));
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith({ sessionShading: true, grid: true, watermark: false, barCloseTimer: false, visibleExtrema: true });
  });

  it("toggles visible high/low callouts", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByLabelText("show visible high/low"));
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith({ sessionShading: true, grid: true, watermark: false, barCloseTimer: true, visibleExtrema: false });
  });

  it("normalizes legacy settings while projecting Volume false for rollback", () => {
    const settings = normalizeChartSettings({ sessionShading: false, volume: true });
    expect(settings).toEqual({ sessionShading: false, grid: true, watermark: false, barCloseTimer: true, visibleExtrema: true });
    expect(chartSettingsRollbackProjection({ sessionShading: false, volume: true }, settings)).toMatchObject({ volume: false });
  });
});
