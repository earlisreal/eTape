// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StepField } from "./StepField";

describe("StepField", () => {
  it("reports every keystroke via onType, not a coerced number", () => {
    const onType = vi.fn();
    render(<StepField ariaLabel="offset" testid="offset" value="0" onType={onType} onStep={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("offset"), { target: { value: "0." } });
    expect(onType).toHaveBeenCalledWith("0.");
  });

  it("emits onStep(1) from the up button and onStep(-1) from the down button", () => {
    const onStep = vi.fn();
    render(<StepField ariaLabel="offset" testid="offset" value="0" onType={vi.fn()} onStep={onStep} />);
    fireEvent.click(screen.getByTestId("offset-up"));
    fireEvent.click(screen.getByTestId("offset-down"));
    expect(onStep.mock.calls).toEqual([[1], [-1]]);
  });

  it("calls onBlur when provided", () => {
    const onBlur = vi.fn();
    render(<StepField ariaLabel="offset" testid="offset" value="0" onType={vi.fn()} onStep={vi.fn()} onBlur={onBlur} />);
    fireEvent.blur(screen.getByLabelText("offset"));
    expect(onBlur).toHaveBeenCalledTimes(1);
  });

  it("disables the input and both buttons when disabled", () => {
    render(<StepField ariaLabel="offset" testid="offset" value="0" onType={vi.fn()} onStep={vi.fn()} disabled />);
    expect((screen.getByLabelText("offset") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByTestId("offset-up") as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId("offset-down") as HTMLButtonElement).disabled).toBe(true);
  });
});
