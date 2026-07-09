// @vitest-environment jsdom
// ui/src/chrome/panels/tv/IndicatorPickerPopover.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { IndicatorPickerPopover } from "./IndicatorPickerPopover";
import { LIGHT } from "../../../render/palette";

afterEach(cleanup);

describe("IndicatorPickerPopover", () => {
  it("lists all 5 catalog indicators", () => {
    render(<IndicatorPickerPopover palette={LIGHT} anchor={null} onClose={() => {}} onAdd={() => {}} />);
    for (const label of ["VWAP", "EMA", "SMA", "Volume", "MACD"]) {
      expect(screen.getByRole("button", { name: `add ${label}` })).toBeTruthy();
    }
  });

  it("filters by search text (case-insensitive)", () => {
    render(<IndicatorPickerPopover palette={LIGHT} anchor={null} onClose={() => {}} onAdd={() => {}} />);
    fireEvent.change(screen.getByPlaceholderText("Search"), { target: { value: "ma" } });
    expect(screen.queryByRole("button", { name: "add EMA" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "add SMA" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "add VWAP" })).toBeNull();
  });

  it("adds and closes on row click", () => {
    const onAdd = vi.fn(); const onClose = vi.fn();
    render(<IndicatorPickerPopover palette={LIGHT} anchor={null} onClose={onClose} onAdd={onAdd} />);
    fireEvent.click(screen.getByRole("button", { name: "add EMA" }));
    expect(onAdd).toHaveBeenCalledWith("EMA");
    expect(onClose).toHaveBeenCalled();
  });

  it("closes on Escape", () => {
    const onClose = vi.fn();
    render(<IndicatorPickerPopover palette={LIGHT} anchor={null} onClose={onClose} onAdd={() => {}} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("closes on outside mousedown", () => {
    const onClose = vi.fn();
    render(<IndicatorPickerPopover palette={LIGHT} anchor={null} onClose={onClose} onAdd={() => {}} />);
    fireEvent.mouseDown(document.body);
    expect(onClose).toHaveBeenCalled();
  });
});
