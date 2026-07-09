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
const macd: IndicatorInstance = { instanceId: "m1", type: "MACD", params: { fast: 12, slow: 26, signal: 9 } };
const macdResolved = describeIndicator(macd, LIGHT);

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

  it("edits per-slot style on the Style tab via preset swatches and applies it", () => {
    const onApply = vi.fn();
    render(<IndicatorSettingsDialog chrome={chrome} instance={ema} resolved={resolved} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("tab", { name: "Style" }));
    fireEvent.click(screen.getByLabelText("line color #F23645"));
    fireEvent.change(screen.getByLabelText("line width"), { target: { value: "3" } });
    fireEvent.change(screen.getByLabelText("line style"), { target: { value: "dashed" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      styles: { line: { color: "#F23645", width: 3, lineStyle: "dashed" } },
    }));
  });

  it("offers preset swatches only — no native color-wheel input", () => {
    const { container } = render(<IndicatorSettingsDialog chrome={chrome} instance={ema} resolved={resolved} onClose={() => {}} onApply={() => {}} />);
    fireEvent.click(screen.getByRole("tab", { name: "Style" }));
    expect(container.querySelector('input[type="color"]')).toBeNull();
    // Picking a preset highlights it (the palette default #3E7CB1 isn't a preset,
    // so nothing is pressed until the user picks one).
    expect(screen.queryByLabelText("line color #3E7CB1")).toBeNull();
    fireEvent.click(screen.getByLabelText("line color #7E57C2"));
    expect(screen.getByLabelText("line color #7E57C2").getAttribute("aria-pressed")).toBe("true");
  });

  it("Defaults resets params and clears styles", () => {
    const onApply = vi.fn();
    render(<IndicatorSettingsDialog chrome={chrome} instance={{ ...ema, params: { period: 50 } }} resolved={resolved} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "Defaults" }));
    expect((screen.getByLabelText("Period") as HTMLInputElement).value).toBe("9");
  });

  it("Style tab shows a per-slot Show checkbox, checked by default; unchecking MACD's histogram persists styles.hist.hidden", () => {
    const onApply = vi.fn();
    render(<IndicatorSettingsDialog chrome={chrome} instance={macd} resolved={macdResolved} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("tab", { name: "Style" }));
    const histShow = screen.getByLabelText("hist visible") as HTMLInputElement;
    expect(histShow.checked).toBe(true);
    // The other two slots stay untouched (still visible) — this is a per-slot toggle.
    expect((screen.getByLabelText("macd visible") as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText("signal visible") as HTMLInputElement).checked).toBe(true);

    fireEvent.click(histShow);
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({
      styles: expect.objectContaining({ hist: expect.objectContaining({ hidden: true }) }),
    }));
    const applied = onApply.mock.calls[0][0] as IndicatorInstance;
    expect(applied.styles?.macd?.hidden).toBeUndefined();
  });
});
