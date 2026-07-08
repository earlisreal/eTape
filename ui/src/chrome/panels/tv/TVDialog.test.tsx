// @vitest-environment jsdom
// ui/src/chrome/panels/tv/TVDialog.test.tsx
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen } from "@testing-library/react";
import { TVDialog } from "./TVDialog";
import { getTvChrome } from "../../../render/chart/tvTheme";
import { modalTracker } from "../../modalTracker";

afterEach(cleanup);
const chrome = getTvChrome("light");

describe("TVDialog", () => {
  it("renders title + children and toggles modalTracker while mounted", () => {
    expect(modalTracker.isOpen()).toBe(false);
    const { unmount } = render(
      <TVDialog title="Settings" chrome={chrome} onClose={() => {}}>
        <div>body</div>
      </TVDialog>,
    );
    expect(screen.getByText("Settings")).toBeTruthy();
    expect(screen.getByText("body")).toBeTruthy();
    expect(modalTracker.isOpen()).toBe(true);
    unmount();
    expect(modalTracker.isOpen()).toBe(false);
  });

  it("closes on scrim click, Escape, and the close button", () => {
    const onClose = vi.fn();
    render(<TVDialog title="D" chrome={chrome} onClose={onClose}><div>x</div></TVDialog>);
    fireEvent.click(screen.getByTestId("tv-dialog-scrim"));
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(screen.getByLabelText("close dialog"));
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it("does not close when the dialog body is clicked", () => {
    const onClose = vi.fn();
    render(<TVDialog title="D" chrome={chrome} onClose={onClose}><div>x</div></TVDialog>);
    fireEvent.click(screen.getByTestId("tv-dialog-box"));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("renders tabs and fires onTab", () => {
    const onTab = vi.fn();
    render(
      <TVDialog title="D" chrome={chrome} onClose={() => {}} tabs={["Inputs", "Style"]} activeTab="Inputs" onTab={onTab}>
        <div>x</div>
      </TVDialog>,
    );
    fireEvent.click(screen.getByRole("tab", { name: "Style" }));
    expect(onTab).toHaveBeenCalledWith("Style");
  });

  it("renders footer buttons and fires their handlers", () => {
    const onDefaults = vi.fn(); const onOk = vi.fn(); const onClose = vi.fn();
    render(<TVDialog title="D" chrome={chrome} onClose={onClose} footer={{ onDefaults, onOk }}><div>x</div></TVDialog>);
    fireEvent.click(screen.getByRole("button", { name: "Defaults" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Ok" }));
    expect(onDefaults).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1); // Cancel === onClose
    expect(onOk).toHaveBeenCalledTimes(1);
  });
});
