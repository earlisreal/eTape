// @vitest-environment jsdom
// ui/src/chrome/panels/tv/IndicatorPickerDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { IndicatorPickerDialog } from "./IndicatorPickerDialog";
import { getTvChrome } from "../../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("IndicatorPickerDialog", () => {
  it("lists all 5 catalog indicators", () => {
    render(<IndicatorPickerDialog chrome={chrome} onClose={() => {}} onAdd={() => {}} />);
    for (const label of ["VWAP", "EMA", "SMA", "Volume", "MACD"]) {
      expect(screen.getByRole("button", { name: `add ${label}` })).toBeTruthy();
    }
  });

  it("filters by search text (case-insensitive)", () => {
    render(<IndicatorPickerDialog chrome={chrome} onClose={() => {}} onAdd={() => {}} />);
    fireEvent.change(screen.getByPlaceholderText("Search"), { target: { value: "ma" } });
    expect(screen.queryByRole("button", { name: "add EMA" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "add SMA" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "add VWAP" })).toBeNull();
  });

  it("adds and closes on row click", () => {
    const onAdd = vi.fn(); const onClose = vi.fn();
    render(<IndicatorPickerDialog chrome={chrome} onClose={onClose} onAdd={onAdd} />);
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    expect(onAdd).toHaveBeenCalledWith("EMA");
    expect(onClose).toHaveBeenCalled();
  });
});
