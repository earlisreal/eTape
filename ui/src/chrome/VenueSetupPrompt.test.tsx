// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { VenueSetupPrompt } from "./VenueSetupPrompt";
import { modalTracker } from "./modalTracker";
import { AppProviders } from "../test/providers";

describe("VenueSetupPrompt", () => {
  it("renders the heading, lede, and broker chips", () => {
    render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={() => {}} onTryDemo={() => {}} /></AppProviders>);
    expect(screen.getByText("Add a broker to trade live")).toBeTruthy();
    expect(screen.getByText(/place orders/i)).toBeTruthy();
    expect(screen.getByText(/deeper 1-minute chart history/i)).toBeTruthy();
    for (const b of ["TradeZero", "Alpaca", "moomoo"]) expect(screen.getByText(b)).toBeTruthy();
  });

  it("fires onConfigure(false) when 'Configure venues' is clicked without ticking the checkbox", () => {
    const onConfigure = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={onConfigure} onDismiss={() => {}} onTryDemo={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByRole("button", { name: "Configure venues" }));
    expect(onConfigure).toHaveBeenCalledWith(false);
  });

  it("fires onConfigure(true) when the checkbox is ticked first", () => {
    const onConfigure = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={onConfigure} onDismiss={() => {}} onTryDemo={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "Configure venues" }));
    expect(onConfigure).toHaveBeenCalledWith(true);
  });

  it("fires onTryDemo when 'Try demo' is clicked, without also firing onConfigure/onDismiss", () => {
    const onTryDemo = vi.fn();
    const onConfigure = vi.fn();
    const onDismiss = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={onConfigure} onDismiss={onDismiss} onTryDemo={onTryDemo} /></AppProviders>);
    fireEvent.click(screen.getByRole("button", { name: "Try demo" }));
    expect(onTryDemo).toHaveBeenCalledTimes(1);
    expect(onConfigure).not.toHaveBeenCalled();
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it("fires onDismiss(false) when 'I'll do it later' is clicked without ticking the checkbox", () => {
    const onDismiss = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={onDismiss} onTryDemo={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(onDismiss).toHaveBeenCalledWith(false);
  });

  it("fires onDismiss(true) when the checkbox is ticked then dismissed", () => {
    const onDismiss = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={onDismiss} onTryDemo={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(onDismiss).toHaveBeenCalledWith(true);
  });

  it("dismisses on scrim click (outer overlay), not on a click inside the card", () => {
    const onDismiss = vi.fn();
    const { container } = render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={onDismiss} onTryDemo={() => {}} /></AppProviders>);
    // Click inside the card body (not on a button) must NOT dismiss — the card's
    // own onClick stopPropagation stops the scrim's handler from firing.
    fireEvent.click(screen.getByText("Add a broker to trade live"));
    expect(onDismiss).not.toHaveBeenCalled();
    const scrim = container.firstChild as HTMLElement;
    fireEvent.click(scrim);
    expect(onDismiss).toHaveBeenCalledWith(false);
  });

  it("dismisses on Escape", () => {
    const onDismiss = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={onDismiss} onTryDemo={() => {}} /></AppProviders>);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onDismiss).toHaveBeenCalledWith(false);
  });

  it("Escape reflects a ticked checkbox", () => {
    const onDismiss = vi.fn();
    render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={onDismiss} onTryDemo={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onDismiss).toHaveBeenCalledWith(true);
  });

  it("mirrors mount/unmount into modalTracker without leaking it stuck open", () => {
    expect(modalTracker.isOpen()).toBe(false);
    const { unmount } = render(<AppProviders><VenueSetupPrompt onConfigure={() => {}} onDismiss={() => {}} onTryDemo={() => {}} /></AppProviders>);
    expect(modalTracker.isOpen()).toBe(true);
    unmount();
    expect(modalTracker.isOpen()).toBe(false);
  });
});
