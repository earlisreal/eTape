// @vitest-environment jsdom
import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { SettingsModal, type SettingsSection } from "./SettingsModal";
import { ToastProvider } from "./Toast";
import { AppProviders } from "../test/providers";
import type { AckMsg } from "../wire/contract";

// `commands` is a required prop (Task 11: Venues & creds wiring) — every render
// needs a stub so components that fire commands on mount/click (VenuesSection's
// GetVenueSetup) don't throw.
const mkCommands = () => ({ sendCommand: vi.fn().mockResolvedValue({ kind: "ack", corrId: "", status: "accepted" } as AckMsg) });

describe("SettingsModal", () => {
  it("returns null when closed", () => {
    const { container } = render(<AppProviders><SettingsModal open={false} section="appearance" onSection={() => {}} onClose={() => {}} commands={mkCommands()} /></AppProviders>);
    expect(container.firstChild).toBeNull();
  });
  it("shows the four sections and switches", () => {
    const onSection = vi.fn();
    render(<AppProviders><SettingsModal open section="appearance" onSection={onSection} onClose={() => {}} commands={mkCommands()} /></AppProviders>);
    // Codebase convention (see TopBar.test.tsx / Catalog.test.tsx) is plain
    // vitest/chai matchers — @testing-library/jest-dom isn't installed here.
    expect(screen.getByRole("button", { name: /appearance/i })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /sounds/i }));
    expect(onSection).toHaveBeenCalledWith("sounds");
  });
  it("appearance toggles theme", () => {
    render(<AppProviders><SettingsModal open section="appearance" onSection={() => {}} onClose={() => {}} commands={mkCommands()} /></AppProviders>);
    fireEvent.click(screen.getByLabelText(/dark/i));
    expect(document.documentElement.dataset.theme).toBe("dark");
  });
  it("has four nav items, routes to Venues & creds, and threads commands through to fire GetVenueSetup", async () => {
    const commands = mkCommands();
    // A tiny stateful wrapper so clicking a nav item actually re-renders with
    // the new section — SettingsModal itself is controlled by its parent. Wrapped
    // in a local ToastProvider (on top of the shared AppProviders) because the
    // Venues & creds section calls useToasts(); AppProviders omits ToastProvider
    // deliberately (see test/providers.tsx) so it doesn't affect the other tests.
    function Wrapper() {
      const [section, setSection] = useState<SettingsSection>("appearance");
      return <AppProviders><ToastProvider><SettingsModal open section={section} onSection={setSection} onClose={() => {}} commands={commands} /></ToastProvider></AppProviders>;
    }
    const { container } = render(<Wrapper />);

    const nav = screen.getByRole("navigation");
    expect(within(nav).getAllByRole("button")).toHaveLength(4);

    const panel = (container.firstChild as HTMLElement).firstChild as HTMLElement;
    expect(panel.style.width).toBe("920px");
    // Fixed-size modal: constant height regardless of active section, and no
    // maxHeight left over from the old shrink-wrap-to-content behavior.
    expect(panel.style.height).toBe("min(640px, 85vh)");
    expect(panel.style.maxHeight).toBe("");
    // Only the content pane scrolls — the nav stays pinned.
    const content = panel.children[1] as HTMLElement;
    expect(content.style.overflow).toBe("auto");

    fireEvent.click(screen.getByRole("button", { name: /venues & creds/i }));
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetVenueSetup", {}));
  });
});
