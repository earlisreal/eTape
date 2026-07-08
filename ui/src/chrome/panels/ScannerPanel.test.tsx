// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { ScannerPanel } from "./ScannerPanel";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel(over: Partial<PanelConfig> = {}, variant: "scanner" | "movers" = "scanner") {
  const stores = makeStores();
  const scanner = stores.scanner;
  const focus = vi.fn();
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  vi.spyOn(linkGroups, "focus").mockImplementation(focus);
  const onConfigChange = vi.fn();
  const config: PanelConfig = { id: "m-scanner", panelId: "scanner", group: null,
    settings: {}, ...over };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async () => ({ status: "accepted" }) } } as unknown as PanelProps & { variant: "scanner" | "movers" };
  render(<ThemeProvider><ScannerPanel {...props} variant={variant} /></ThemeProvider>);
  return { scanner, focus, onConfigChange };
}

describe("ScannerPanel", () => {
  it("waits before data, then renders ranked rows", () => {
    const { scanner } = renderPanel();
    expect(screen.getByText(/waiting/i)).toBeTruthy();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 62.1, floatShares: 4_300_000_000, volume: 1_250_000 },
        { symbol: "US.WXYZ", changePct: null, last: null, floatShares: 21_000_000, volume: 0 },
      ] } }));
    expect(screen.getByText("US.KO")).toBeTruthy();
    expect(screen.getByText("+18.4%")).toBeTruthy();
  });

  it("renders no-print rows as em dash, never 0", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.WXYZ", changePct: null, last: null, floatShares: null, volume: 0 }] } }));
    const rowCells = screen.getByText("US.WXYZ").closest("tr")!.querySelectorAll("td");
    expect([...rowCells].map((c) => c.textContent)).toContain("—");
    expect([...rowCells].some((c) => c.textContent === "0%")).toBe(false);
  });

  it("applies the min-%-change threshold", () => {
    const { scanner } = renderPanel({ settings: { thresholds: { minChangePct: 10, floatCapShares: null, minVolume: 0 } } });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    expect(screen.queryByText("US.KO")).toBeTruthy();
    expect(screen.queryByText("US.LOW")).toBeNull();
  });

  it("row double-click publishes focus to the panel's linked group", () => {
    const { scanner, focus } = renderPanel({ group: "blue" });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.doubleClick(screen.getByText("US.KO"));
    expect(focus).toHaveBeenCalledWith("blue", "US.KO");
  });

  it("row double-click falls back to green when the panel is pinned (no linked group)", () => {
    const { scanner, focus } = renderPanel({ group: null });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.doubleClick(screen.getByText("US.KO"));
    expect(focus).toHaveBeenCalledWith("green", "US.KO");
  });

  it("a single row click only highlights the row — it never loads the symbol into the group", () => {
    const { scanner, focus } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.click(screen.getByText("US.KO"));
    expect(focus).not.toHaveBeenCalled();
    const row = screen.getByText("US.KO").closest("tr") as HTMLElement;
    expect(row.style.background).toBe("rgba(154, 106, 27, 0.16)");
  });

  it("has no persistent input row on load; the ⚙ button reveals the filter inputs", () => {
    renderPanel();
    expect(screen.queryByLabelText("min change %")).toBeNull();
    expect(screen.queryByLabelText("float cap")).toBeNull();
    expect(screen.queryByLabelText("min volume")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    expect(screen.getByLabelText("min change %")).toBeTruthy();
    expect(screen.getByLabelText("float cap")).toBeTruthy();
    expect(screen.getByLabelText("min volume")).toBeTruthy();
  });

  it("the summary line reflects the active thresholds", () => {
    renderPanel({ settings: { thresholds: { minChangePct: 10, floatCapShares: 20_000_000, minVolume: 100_000 } } });
    expect(screen.getByText("change ≥ 10% · float ≤ 20M · vol ≥ 100k")).toBeTruthy();
  });

  it("summary line reads 'no filters' when thresholds are off", () => {
    renderPanel();
    expect(screen.getByText("no filters")).toBeTruthy();
  });

  it("editing a threshold in the popover and clicking Apply persists via onConfigChange", () => {
    const { onConfigChange } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    fireEvent.change(screen.getByLabelText("min change %"), { target: { value: "7" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({ minChangePct: 7 }) }));
  });

  it("Reset defaults clears the draft inputs without persisting until Apply", () => {
    const { onConfigChange } = renderPanel({ settings: { thresholds: { minChangePct: 10, floatCapShares: null, minVolume: 0 } } });
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    fireEvent.click(screen.getByRole("button", { name: "Reset defaults" }));
    expect((screen.getByLabelText("min change %") as HTMLInputElement).value).toBe("0");
    expect(onConfigChange).not.toHaveBeenCalled();
  });

  it("default view sorts by % change descending", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.HIGH", changePct: 40, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["US.HIGH", "US.LOW"]);
  });

  it("clicking the % header toggles sort direction and persists it via onConfigChange", () => {
    const { scanner, onConfigChange } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.HIGH", changePct: 40, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    fireEvent.click(screen.getByRole("columnheader", { name: /%/ }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({
      sort: { col: "changePct", dir: "asc" } }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["US.LOW", "US.HIGH"]);
  });

  it("movers variant has no filter button and applies no thresholds", () => {
    const { scanner } = renderPanel(
      { settings: { thresholds: { minChangePct: 50, floatCapShares: null, minVolume: 0 } } },
      "movers",
    );
    expect(screen.queryByRole("button", { name: /filters/i })).toBeNull();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth",
      payload: { refreshedAt: "2026-07-08T14:00:00.000Z", rows: [
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 }] } }));
    expect(screen.getByText("US.LOW")).toBeTruthy(); // not filtered out despite minChangePct:50
  });

  it("follows the live session label", () => {
    const { scanner } = renderPanel({}, "movers");
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "afterhours",
      payload: { refreshedAt: "2026-07-08T21:00:00.000Z", rows: [
        { symbol: "US.AH", changePct: 3, last: 1, floatShares: 1, volume: 1 }] } }));
    expect(screen.getByText(/after-hours/i)).toBeTruthy();
  });
});
