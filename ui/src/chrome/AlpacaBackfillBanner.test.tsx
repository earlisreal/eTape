// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AlpacaBackfillBanner } from "./AlpacaBackfillBanner";
import { ThemeProvider } from "./ThemeProvider";

function Wrapped({ onSetup, onDismiss }: { onSetup: () => void; onDismiss: () => void }) {
  return (
    <ThemeProvider>
      <AlpacaBackfillBanner onSetup={onSetup} onDismiss={onDismiss} />
    </ThemeProvider>
  );
}

describe("AlpacaBackfillBanner", () => {
  it("renders the 1-minute-history hint copy", () => {
    render(<Wrapped onSetup={() => {}} onDismiss={() => {}} />);
    const banner = screen.getByTestId("alpaca-backfill-banner");
    expect(banner).toBeTruthy();
    expect(banner.textContent).toContain("Alpaca");
    expect(banner.textContent).toContain("1-minute history");
  });

  it("calls onSetup when 'Set up Alpaca' is clicked", () => {
    const onSetup = vi.fn();
    render(<Wrapped onSetup={onSetup} onDismiss={() => {}} />);
    fireEvent.click(screen.getByTestId("alpaca-banner-setup"));
    expect(onSetup).toHaveBeenCalled();
  });

  it("calls onDismiss when the ✕ button is clicked", () => {
    const onDismiss = vi.fn();
    render(<Wrapped onSetup={() => {}} onDismiss={onDismiss} />);
    fireEvent.click(screen.getByTestId("alpaca-banner-dismiss"));
    expect(onDismiss).toHaveBeenCalled();
  });

  it("exposes an accessible name for the dismiss button", () => {
    render(<Wrapped onSetup={() => {}} onDismiss={() => {}} />);
    expect(screen.getByRole("button", { name: "Dismiss" })).toBeTruthy();
  });
});
