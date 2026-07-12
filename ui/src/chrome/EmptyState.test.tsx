// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { EmptyState } from "./EmptyState";
import { AppProviders } from "../test/providers";

describe("EmptyState", () => {
  it("renders the heading, lede, and Catalog regardless of showTryDemo", () => {
    render(<AppProviders><EmptyState onAddPanel={() => {}} onApplyPreset={() => {}} showTryDemo={false} onTryDemo={() => {}} onImportLayoutFile={() => {}} /></AppProviders>);
    expect(screen.getByText("Empty workspace")).toBeTruthy();
    expect(screen.getByText("Start from a preset")).toBeTruthy();
  });

  it("shows the 'Try demo' CTA when showTryDemo is true", () => {
    render(<AppProviders><EmptyState onAddPanel={() => {}} onApplyPreset={() => {}} showTryDemo={true} onTryDemo={() => {}} onImportLayoutFile={() => {}} /></AppProviders>);
    expect(screen.getByRole("button", { name: "Try demo" })).toBeTruthy();
    expect(screen.getByText(/new here\?/i)).toBeTruthy();
  });

  it("hides the 'Try demo' CTA when showTryDemo is false (e.g. already in a demo/replay session)", () => {
    render(<AppProviders><EmptyState onAddPanel={() => {}} onApplyPreset={() => {}} showTryDemo={false} onTryDemo={() => {}} onImportLayoutFile={() => {}} /></AppProviders>);
    expect(screen.queryByRole("button", { name: "Try demo" })).toBeNull();
  });

  it("clicking 'Try demo' calls onTryDemo, not onAddPanel/onApplyPreset", () => {
    const onTryDemo = vi.fn();
    const onAddPanel = vi.fn();
    const onApplyPreset = vi.fn();
    render(<AppProviders><EmptyState onAddPanel={onAddPanel} onApplyPreset={onApplyPreset} showTryDemo={true} onTryDemo={onTryDemo} onImportLayoutFile={() => {}} /></AppProviders>);
    screen.getByRole("button", { name: "Try demo" }).click();
    expect(onTryDemo).toHaveBeenCalledTimes(1);
    expect(onAddPanel).not.toHaveBeenCalled();
    expect(onApplyPreset).not.toHaveBeenCalled();
  });

  it("renders the 'Import layout' control regardless of showTryDemo, and forwards the picked file", () => {
    const onImportLayoutFile = vi.fn();
    render(<AppProviders>
      <EmptyState onAddPanel={() => {}} onApplyPreset={() => {}} showTryDemo={false} onTryDemo={() => {}} onImportLayoutFile={onImportLayoutFile} />
    </AppProviders>);
    expect(screen.getByRole("button", { name: "Import layout" })).toBeTruthy();

    const file = new File(["{}"], "layout.json", { type: "application/json" });
    const input = screen.getByTestId("empty-import-file") as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });

    expect(onImportLayoutFile).toHaveBeenCalledTimes(1);
    expect(onImportLayoutFile).toHaveBeenCalledWith(file);
    expect(input.value).toBe("");
  });
});
