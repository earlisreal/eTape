// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Catalog } from "./Catalog";
import { ThemeProvider } from "./ThemeProvider";

describe("Catalog", () => {
  it("lists both presets and the non-dev panel index", () => {
    // Vitest sets NODE_ENV=test so import.meta.env.DEV is TRUE by default; stub it
    // false to assert the prod behaviour (Smoke hidden).
    vi.stubEnv("DEV", false);
    render(<ThemeProvider><Catalog onAddPanel={() => {}} onApplyPreset={() => {}} /></ThemeProvider>);
    // Codebase convention (see TopBar.test.tsx) is plain vitest/chai matchers —
    // @testing-library/jest-dom (toBeInTheDocument) isn't installed here.
    expect(screen.getByText("Monitoring")).toBeTruthy();
    expect(screen.getByText("Trading")).toBeTruthy();
    expect(screen.getByText("Chart")).toBeTruthy();
    expect(screen.queryByText("Smoke")).toBeNull(); // dev panel hidden when DEV=false
    vi.unstubAllEnvs();
  });
  it("adds a panel on click and applies a preset on click", () => {
    const onAddPanel = vi.fn(), onApplyPreset = vi.fn();
    render(<ThemeProvider><Catalog onAddPanel={onAddPanel} onApplyPreset={onApplyPreset} /></ThemeProvider>);
    fireEvent.click(screen.getByText("Chart"));
    expect(onAddPanel).toHaveBeenCalledWith("chart");
    fireEvent.click(screen.getByText("Monitoring"));
    expect(onApplyPreset).toHaveBeenCalledWith("monitoring");
  });
});
