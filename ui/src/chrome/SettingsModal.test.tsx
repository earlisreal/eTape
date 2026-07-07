// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SettingsModal } from "./SettingsModal";
import { AppProviders } from "../test/providers";

describe("SettingsModal", () => {
  it("returns null when closed", () => {
    const { container } = render(<AppProviders><SettingsModal open={false} section="appearance" onSection={() => {}} onClose={() => {}} /></AppProviders>);
    expect(container.firstChild).toBeNull();
  });
  it("shows the three sections and switches", () => {
    const onSection = vi.fn();
    render(<AppProviders><SettingsModal open section="appearance" onSection={onSection} onClose={() => {}} /></AppProviders>);
    // Codebase convention (see TopBar.test.tsx / Catalog.test.tsx) is plain
    // vitest/chai matchers — @testing-library/jest-dom isn't installed here.
    expect(screen.getByRole("button", { name: /appearance/i })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /sounds/i }));
    expect(onSection).toHaveBeenCalledWith("sounds");
  });
  it("appearance toggles theme", () => {
    render(<AppProviders><SettingsModal open section="appearance" onSection={() => {}} onClose={() => {}} /></AppProviders>);
    fireEvent.click(screen.getByLabelText(/dark/i));
    expect(document.documentElement.dataset.theme).toBe("dark");
  });
});
