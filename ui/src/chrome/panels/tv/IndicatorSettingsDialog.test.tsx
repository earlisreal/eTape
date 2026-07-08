// @vitest-environment jsdom
// ui/src/chrome/panels/tv/IndicatorSettingsDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { IndicatorSettingsDialog } from "./IndicatorSettingsDialog";
import { getTvChrome } from "../../../render/chart/tvTheme";
import { describeIndicator, type IndicatorInstance } from "../../../render/chart/indicatorSeries";
import { LIGHT } from "../../../render/palette";

afterEach(cleanup);
const chrome = getTvChrome("light");
const ema: IndicatorInstance = { instanceId: "e1", type: "EMA", params: { period: 9 } };
const resolved = describeIndicator(ema, LIGHT);

describe("IndicatorSettingsDialog", () => {
  it("shows an Inputs tab with a number input per param", () => {
    render(<IndicatorSettingsDialog chrome={chrome} instance={ema} resolved={resolved} onClose={() => {}} onApply={() => {}} />);
    const period = screen.getByLabelText("Period") as HTMLInputElement;
    expect(period.value).toBe("9");
  });

  it("applies an edited param on Ok", () => {
    const onApply = vi.fn();
    render(<IndicatorSettingsDialog chrome={chrome} instance={ema} resolved={resolved} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("Period"), { target: { value: "21" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ instanceId: "e1", params: { period: 21 } }));
  });

  it("edits per-slot style on the Style tab and applies it", () => {
    const onApply = vi.fn();
    render(<IndicatorSettingsDialog chrome={chrome} instance={ema} resolved={resolved} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("tab", { name: "Style" }));
    fireEvent.change(screen.getByLabelText("line color"), { target: { value: "#123456" } });
    fireEvent.change(screen.getByLabelText("line width"), { target: { value: "3" } });
    fireEvent.change(screen.getByLabelText("line style"), { target: { value: "dashed" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      styles: { line: { color: "#123456", width: 3, lineStyle: "dashed" } },
    }));
  });

  it("Defaults resets params and clears styles", () => {
    const onApply = vi.fn();
    render(<IndicatorSettingsDialog chrome={chrome} instance={{ ...ema, params: { period: 50 } }} resolved={resolved} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "Defaults" }));
    expect((screen.getByLabelText("Period") as HTMLInputElement).value).toBe("9");
  });
});
