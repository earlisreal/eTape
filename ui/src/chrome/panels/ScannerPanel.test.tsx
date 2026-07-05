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

function renderPanel(over: Partial<PanelConfig> = {}) {
  const stores = makeStores();
  const scanner = stores.scanner;
  const focus = vi.fn();
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  vi.spyOn(linkGroups, "focus").mockImplementation(focus);
  const onConfigChange = vi.fn();
  const config: PanelConfig = { id: "m-scanner", panelId: "scanner", group: null,
    settings: { targetGroup: "green" }, ...over };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands: { sendCommand: async () => ({ status: "accepted" }) } } as unknown as PanelProps & { session: "premarket" };
  render(<ThemeProvider><ScannerPanel {...props} session="premarket" /></ThemeProvider>);
  return { scanner, focus, onConfigChange };
}

describe("ScannerPanel", () => {
  it("waits before data, then renders ranked rows", () => {
    const { scanner } = renderPanel();
    expect(screen.getByText(/waiting/i)).toBeTruthy();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-06T13:30:00Z", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 62.1, floatShares: 4_300_000_000, volume: 1_250_000 },
        { symbol: "US.WXYZ", changePct: null, last: null, floatShares: 21_000_000, volume: 0 },
      ] } }));
    expect(screen.getByText("US.KO")).toBeTruthy();
    expect(screen.getByText("+18.4%")).toBeTruthy();
  });

  it("renders no-print rows as em dash, never 0", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "t", rows: [{ symbol: "US.WXYZ", changePct: null, last: null, floatShares: null, volume: 0 }] } }));
    const rowCells = screen.getByText("US.WXYZ").closest("tr")!.querySelectorAll("td");
    expect([...rowCells].map((c) => c.textContent)).toContain("—");
    expect([...rowCells].some((c) => c.textContent === "0%")).toBe(false);
  });

  it("applies the min-%-change threshold", () => {
    const { scanner } = renderPanel({ settings: { targetGroup: "green", thresholds: { minChangePct: 10, floatCapShares: null, minVolume: 0 } } });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "t", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    expect(screen.queryByText("US.KO")).toBeTruthy();
    expect(screen.queryByText("US.LOW")).toBeNull();
  });

  it("row click publishes focus to the target group", () => {
    const { scanner, focus } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "t", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.click(screen.getByText("US.KO"));
    expect(focus).toHaveBeenCalledWith("green", "US.KO");
  });

  it("editing a threshold persists via onConfigChange", () => {
    const { onConfigChange } = renderPanel();
    fireEvent.change(screen.getByLabelText(/min change/i), { target: { value: "7" } });
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({ minChangePct: 7 }) }));
  });
});
