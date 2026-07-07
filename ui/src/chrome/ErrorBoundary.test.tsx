// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";
import { ThemeProvider } from "./ThemeProvider";
import { getPalette } from "../render/palette";

function Boom(): JSX.Element { throw new Error("panel exploded"); }

// jsdom normalizes inline hex colors to rgb(); compare against that form.
function toRgb(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
}

describe("ErrorBoundary", () => {
  it("renders an inline error card when a child throws", () => {
    render(
      <ThemeProvider>
        <ErrorBoundary label="Chart"><Boom /></ErrorBoundary>
      </ThemeProvider>,
    );
    const heading = screen.getByText(/Chart/);
    expect(heading).toBeTruthy();
    expect(screen.getByRole("button", { name: /reload/i })).toBeTruthy();
    const card = heading.parentElement as HTMLElement;
    const palette = getPalette("light");
    expect(card.style.color).toBe(toRgb(palette.danger));
    expect(card.style.backgroundColor).toBe(toRgb(palette.surface));
    expect(card.style.borderColor).toBe(toRgb(palette.danger));
  });
});
