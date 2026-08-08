// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { getTvChrome } from "../../render/chart/tvTheme";
import { LadderSettingsDialog } from "./LadderSettingsDialog";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("LadderSettingsDialog", () => {
  it("shows the current depth", () => {
    render(<LadderSettingsDialog chrome={chrome} levels={35} onClose={() => {}} onApply={() => {}} />);
    expect((screen.getByLabelText("depth levels") as HTMLInputElement).value).toBe("35");
  });

  it("applies an edited value on Ok", () => {
    const onApply = vi.fn();
    render(<LadderSettingsDialog chrome={chrome} levels={10} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("depth levels"), { target: { value: "60" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(60);
  });

  it("defaults an empty value to 10 on Ok", () => {
    const onApply = vi.fn();
    render(<LadderSettingsDialog chrome={chrome} levels={35} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("depth levels"), { target: { value: "" } });
    expect((screen.getByLabelText("depth levels") as HTMLInputElement).value).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(10);
  });

  it("resets Defaults to 10", () => {
    const onApply = vi.fn();
    render(<LadderSettingsDialog chrome={chrome} levels={35} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "Defaults" }));
    expect((screen.getByLabelText("depth levels") as HTMLInputElement).value).toBe("10");
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(10);
  });

  it("clamps values above 60 and below 1", () => {
    const onApply = vi.fn();
    const { rerender } = render(<LadderSettingsDialog chrome={chrome} levels={10} onClose={() => {}} onApply={onApply} />);
    const input = screen.getByLabelText("depth levels");
    fireEvent.change(input, { target: { value: "100" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenLastCalledWith(60);

    onApply.mockClear();
    rerender(<LadderSettingsDialog chrome={chrome} levels={10} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("depth levels"), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenLastCalledWith(1);
  });

  it("floors non-integer values", () => {
    const onApply = vi.fn();
    render(<LadderSettingsDialog chrome={chrome} levels={10} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("depth levels"), { target: { value: "10.8" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(10);
  });

  it("closes without applying on Cancel", () => {
    const onApply = vi.fn();
    const onClose = vi.fn();
    render(<LadderSettingsDialog chrome={chrome} levels={35} onClose={onClose} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("depth levels"), { target: { value: "60" } });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onApply).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });
});
