// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { Keycap } from "./Keycap";

describe("Keycap", () => {
  it("splits a combo into one kbd per key with symbol labels", () => {
    render(<ThemeProvider><Keycap combo="Ctrl+Shift+Backspace" /></ThemeProvider>);
    const kbds = screen.getAllByRole("group")[0].querySelectorAll("kbd");
    expect(kbds.length).toBe(3);
    expect(kbds[1].textContent).toBe("⇧");
    expect(kbds[2].textContent).toBe("⌫");
  });
});
