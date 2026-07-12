// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { GeneralSection } from "./GeneralSection";
import { AppProviders } from "../test/providers";
import type { ToastApi } from "./Toast";
import type { Workspace } from "./workspace";

// Codebase convention (see SettingsModal.test.tsx) is plain vitest/chai
// matchers — @testing-library/jest-dom isn't installed here.
const mkWorkspace = (): Workspace => ({ name: "main", panels: [], layout: {}, groups: {}, linkVenues: {} });
const mkToast = (): ToastApi => ({ push: vi.fn(), dismiss: vi.fn() });

function renderSection() {
  return render(
    <AppProviders>
      <GeneralSection getWorkspace={mkWorkspace} onImportWorkspace={() => {}} toast={mkToast()} />
    </AppProviders>,
  );
}

describe("GeneralSection", () => {
  it("renders the theme toggle and switches to dark", () => {
    renderSection();
    fireEvent.click(screen.getByLabelText(/dark/i));
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("renders the sounds controls", () => {
    renderSection();
    expect(screen.getByTestId("sound-enabled")).toBeTruthy();
  });

  it("defaults the ext-hours buffer to 1 and steps up by 0.1", () => {
    renderSection();
    const input = screen.getByTestId("ext-buffer") as HTMLInputElement;
    expect(input.value).toBe("1");
    fireEvent.click(screen.getByTestId("ext-buffer-up"));
    expect(input.value).toBe("1.1");
  });

  it("shows raw typed text while editing, then clamps the ext-hours buffer to 10 on blur", () => {
    renderSection();
    const input = screen.getByTestId("ext-buffer") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "50" } });
    expect(input.value).toBe("50");
    fireEvent.blur(input);
    expect(input.value).toBe("10");
  });

  it("renders the layout backup panel", () => {
    renderSection();
    expect(screen.getByTestId("download-json")).toBeTruthy();
  });
});
