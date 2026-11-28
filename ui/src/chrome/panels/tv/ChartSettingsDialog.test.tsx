// @vitest-environment jsdom
// ui/src/chrome/panels/tv/ChartSettingsDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { ChartSettingsDialog, DEFAULT_CHART_SETTINGS, chartSettingsRollbackProjection, normalizeChartSettings } from "./ChartSettingsDialog";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("ChartSettingsDialog", () => {
  it.each([undefined, null, "true", 1])("treats invalid/missing override %s as inheritance", (volumeOverlap) => {
    expect(normalizeChartSettings({ volumeOverlap })).not.toHaveProperty("volumeOverlap");
  });
  it.each([true, false])("preserves the explicit override %s", (volumeOverlap) => {
    expect(normalizeChartSettings({ volumeOverlap }).volumeOverlap).toBe(volumeOverlap);
  });
  it("discards an overlap draft on cancel", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("Overlap volume"), { target: { value: "on" } });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onApply).not.toHaveBeenCalled();
  });
  it("can return an explicit Volume Overlap override to the app default", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={normalizeChartSettings({ volumeOverlap: false })}
      globalVolumeOverlap={true} onClose={() => {}} onApply={onApply} />);
    const choice = screen.getByLabelText("Overlap volume");
    expect((choice as HTMLSelectElement).value).toBe("off");
    fireEvent.change(choice, { target: { value: "default" } });
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    const applied = onApply.mock.calls[0][0];
    expect(applied).not.toHaveProperty("volumeOverlap");
    expect(chartSettingsRollbackProjection({ volumeOverlap: false, other: 7 }, applied))
      .toEqual({ ...DEFAULT_CHART_SETTINGS, other: 7, volume: false });
  });
  it.each([undefined, null, "false"])("shows trade markers with missing or malformed setting %s", (showTradeMarkers) => {
    expect(normalizeChartSettings({ showTradeMarkers }).showTradeMarkers).toBe(true);
  });

  it.each(["Ok", "Cancel"])("applies trade marker visibility only on Ok (%s)", (button) => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    const checkbox = screen.getByRole<HTMLInputElement>("checkbox", { name: "Show trade markers" });
    expect(checkbox.checked).toBe(true);
    fireEvent.click(checkbox);
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: button }));
    if (button === "Ok") {
      expect(onApply).toHaveBeenCalledWith({ ...DEFAULT_CHART_SETTINGS, showTradeMarkers: false });
      expect(normalizeChartSettings(onApply.mock.calls[0][0]).showTradeMarkers).toBe(false);
    } else {
      expect(onApply).not.toHaveBeenCalled();
    }
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
    expect(onApply).toHaveBeenCalledWith({ ...DEFAULT_CHART_SETTINGS, grid: false, watermark: true });
  });

  it("toggles bar-close timer on and off", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByLabelText("bar-close timer"));
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith({ ...DEFAULT_CHART_SETTINGS, barCloseTimer: false });
  });

  it("toggles visible high/low callouts", () => {
    const onApply = vi.fn();
    render(<ChartSettingsDialog chrome={chrome} settings={DEFAULT_CHART_SETTINGS} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByLabelText("show visible high/low"));
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith({ ...DEFAULT_CHART_SETTINGS, visibleExtrema: false });
  });

  it("normalizes legacy settings while projecting Volume false for rollback", () => {
    const settings = normalizeChartSettings({ sessionShading: false, volume: true });
    expect(settings).toEqual({ ...DEFAULT_CHART_SETTINGS, sessionShading: false });
    expect(chartSettingsRollbackProjection({ sessionShading: false, volume: true }, settings)).toMatchObject({ volume: false });
  });
});
