// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { LIGHT } from "../../render/palette";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider, useToasts } from "../Toast";
import { ExportTradesPopover } from "./ExportTradesPopover";

function Harness({ commands, onClose = () => {} }: { commands: { sendQuery: (name: string, args: unknown) => Promise<unknown> }; onClose?: () => void }) {
  const toast = useToasts();
  const anchor = document.createElement("button");
  document.body.appendChild(anchor);
  return <ExportTradesPopover palette={LIGHT} anchor={anchor} venue="sim" commands={commands} toast={toast} onClose={onClose} />;
}

function wrap(commands: { sendQuery: (name: string, args: unknown) => Promise<unknown> }, onClose: () => void = () => {}) {
  // ToastProvider always renders ToastHost, which reads palette via useTheme() —
  // needs a ThemeProvider ancestor, same as Toast.test.tsx's setup().
  return render(<ThemeProvider><ToastProvider><Harness commands={commands} onClose={onClose} /></ToastProvider></ThemeProvider>);
}

describe("ExportTradesPopover", () => {
  beforeEach(() => {
    (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = vi.fn(() => "blob:mock");
    (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn();
  });

  it("defaults to All time and downloads a CSV via an anchor click", async () => {
    const csv = "datetime,symbol,action,price,shares,fees,externalId\n2026-07-10T09:31:05,NVDA,BUY,120.5,100,0,etape:sim:12\n";
    const calls: Array<{ name: string; args: unknown }> = [];
    const sendQuery = vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { csv, count: 1 }; });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);

    fireEvent.click(screen.getByTestId("export-download"));

    await waitFor(() => expect(clickSpy).toHaveBeenCalledTimes(1));
    expect(calls).toEqual([{ name: "ExportFills", args: { venue: "sim", preset: "all", from: "", to: "" } }]);
    const anchor = clickSpy.mock.instances[0] as unknown as HTMLAnchorElement;
    expect(anchor.download).toBe("etape-sim-all.csv");
    expect(onClose).toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it("shows an info toast and does not download when there are no fills", async () => {
    const sendQuery = vi.fn(async () => ({ csv: "datetime,symbol,action,price,shares,fees,externalId\n", count: 0 }));
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);

    fireEvent.click(screen.getByTestId("export-download"));

    await waitFor(() => expect(screen.getByText(/No fills to export/)).toBeTruthy());
    expect(clickSpy).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it("Custom preset reveals date inputs, disables Download until both are set, and forwards from/to", async () => {
    const calls: Array<{ name: string; args: unknown }> = [];
    const sendQuery = vi.fn(async (name: string, args: unknown) => { calls.push({ name, args }); return { csv: "h\n", count: 1 }; });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    wrap({ sendQuery });

    fireEvent.change(screen.getByTestId("export-preset"), { target: { value: "custom" } });
    expect((screen.getByTestId("export-download") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(screen.getByTestId("export-from"), { target: { value: "2026-07-01" } });
    expect((screen.getByTestId("export-download") as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(screen.getByTestId("export-to"), { target: { value: "2026-07-03" } });
    expect((screen.getByTestId("export-download") as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByTestId("export-download"));
    await waitFor(() => expect(clickSpy).toHaveBeenCalledTimes(1));
    expect(calls).toEqual([{ name: "ExportFills", args: { venue: "sim", preset: "custom", from: "2026-07-01", to: "2026-07-03" } }]);
    clickSpy.mockRestore();
  });

  it("shows a danger toast when the export query rejects, and does not close", async () => {
    const sendQuery = vi.fn(async () => { throw new Error("ws disconnected"); });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);

    fireEvent.click(screen.getByTestId("export-download"));

    await waitFor(() => expect(screen.getByText(/Export failed/)).toBeTruthy());
    expect(clickSpy).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it("closes on Escape without downloading", () => {
    const sendQuery = vi.fn(async () => ({ csv: "h\n", count: 1 }));
    const onClose = vi.fn();
    wrap({ sendQuery }, onClose);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });
});
