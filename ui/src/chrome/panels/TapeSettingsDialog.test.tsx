// @vitest-environment jsdom
// ui/src/chrome/panels/TapeSettingsDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TapeSettingsDialog } from "./TapeSettingsDialog";
import { getTvChrome } from "../../render/chart/tvTheme";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("TapeSettingsDialog", () => {
  it("shows the current minimum trade size", () => {
    render(<TapeSettingsDialog chrome={chrome} minSize={250} onClose={() => {}} onApply={() => {}} />);
    expect((screen.getByLabelText("minimum trade size") as HTMLInputElement).value).toBe("250");
  });

  it("applies an edited value on Ok", () => {
    const onApply = vi.fn();
    render(<TapeSettingsDialog chrome={chrome} minSize={0} onClose={() => {}} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("minimum trade size"), { target: { value: "500" } });
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(500);
  });

  it("Defaults resets the draft to 0", () => {
    const onApply = vi.fn();
    render(<TapeSettingsDialog chrome={chrome} minSize={300} onClose={() => {}} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "Defaults" }));
    expect((screen.getByLabelText("minimum trade size") as HTMLInputElement).value).toBe("0");
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(0);
  });

  it("clamps a negative or garbage draft to 0 on Ok", () => {
    const onApply = vi.fn();
    render(<TapeSettingsDialog chrome={chrome} minSize={0} onClose={() => {}} onApply={onApply} />);
    const input = screen.getByLabelText("minimum trade size") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "-5" } });
    // The input's own onChange already clamps to >= 0 (mirrors the old inline
    // input's behavior), so the draft here is 0, not negative.
    expect(input.value).toBe("0");
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onApply).toHaveBeenCalledWith(0);
  });

  it("closes without applying on Cancel", () => {
    const onApply = vi.fn();
    const onClose = vi.fn();
    render(<TapeSettingsDialog chrome={chrome} minSize={0} onClose={onClose} onApply={onApply} />);
    fireEvent.change(screen.getByLabelText("minimum trade size"), { target: { value: "999" } });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onApply).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });
});
