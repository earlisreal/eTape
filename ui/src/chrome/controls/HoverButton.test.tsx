// @vitest-environment jsdom
import { createRef } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { HoverButton } from "./HoverButton";

describe("HoverButton", () => {
  it("applies the default hover overlay on mouseEnter and reverts it on mouseLeave", () => {
    render(<HoverButton>click me</HoverButton>);
    const btn = screen.getByText("click me");

    expect(btn.style.background).toBe("");
    expect(btn.style.color).toBe("");

    fireEvent.mouseEnter(btn);
    expect(btn.style.background).toBe("var(--surface)");
    expect(btn.style.color).toBe("var(--text)");

    fireEvent.mouseLeave(btn);
    expect(btn.style.background).toBe("");
    expect(btn.style.color).toBe("");
  });

  it("uses a custom hoverStyle instead of the default when supplied", () => {
    render(
      <HoverButton hoverStyle={{ background: "#2a2f3a", color: "#e8e8e8" }}>
        island button
      </HoverButton>,
    );
    const btn = screen.getByText("island button");

    fireEvent.mouseEnter(btn);
    expect(btn.style.background).toBe("rgb(42, 47, 58)");
    expect(btn.style.color).toBe("rgb(232, 232, 232)");
  });

  it("suppresses the hover overlay while disabled, even after mouseEnter", () => {
    render(<HoverButton disabled>disabled btn</HoverButton>);
    const btn = screen.getByText("disabled btn") as HTMLButtonElement;

    fireEvent.mouseEnter(btn);
    expect(btn.style.background).toBe("");
    expect(btn.style.color).toBe("");
    expect(btn.disabled).toBe(true);
  });

  it("resolves a forwarded ref to the underlying button DOM node", () => {
    const ref = createRef<HTMLButtonElement>();
    render(<HoverButton ref={ref}>ref btn</HoverButton>);
    expect(ref.current).toBeInstanceOf(HTMLButtonElement);
    expect(ref.current?.textContent).toBe("ref btn");
  });

  it("passes through onClick, aria-label, and type, and still invokes caller mouse handlers", () => {
    const onClick = vi.fn();
    const onMouseEnter = vi.fn();
    const onMouseLeave = vi.fn();
    render(
      <HoverButton
        onClick={onClick}
        aria-label="submit-order"
        type="submit"
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
      >
        submit
      </HoverButton>,
    );
    const btn = screen.getByRole("button", { name: "submit-order" }) as HTMLButtonElement;
    expect(btn.type).toBe("submit");

    fireEvent.click(btn);
    expect(onClick).toHaveBeenCalledTimes(1);

    fireEvent.mouseEnter(btn);
    expect(onMouseEnter).toHaveBeenCalledTimes(1);
    expect(btn.style.background).toBe("var(--surface)");

    fireEvent.mouseLeave(btn);
    expect(onMouseLeave).toHaveBeenCalledTimes(1);
    expect(btn.style.background).toBe("");
  });
});
