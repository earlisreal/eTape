// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, act, fireEvent, cleanup, within } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { ScannerPanel } from "./ScannerPanel";
import { PanelHeaderSlotContext } from "./headerSlot";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";

afterEach(cleanup);

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel(
  over: Partial<PanelConfig> = {},
  groupProp?: PanelConfig["group"],
  headerSlot?: HTMLElement,
  scannerSync?: PanelProps["scannerSync"],
) {
  const stores = makeStores();
  const scanner = stores.scanner;
  const focus = vi.fn();
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  vi.spyOn(linkGroups, "focus").mockImplementation(focus);
  const onConfigChange = vi.fn();
  const config: PanelConfig = { id: "m-scanner", panelId: "scanner", group: null,
    settings: {}, ...over };
  const commands = { sendCommand: vi.fn(async () => ({ status: "accepted" })) };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands, group: groupProp, scannerSync } as unknown as PanelProps;
  const view = render(<ThemeProvider><PanelHeaderSlotContext.Provider value={headerSlot}><ScannerPanel {...props} /></PanelHeaderSlotContext.Provider></ThemeProvider>);
  return { scanner, focus, onConfigChange, commands, ...view };
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
    expect(screen.getByText("KO")).toBeTruthy();
    expect(screen.getByText("+18.4%")).toBeTruthy();
  });

  it("keeps session and the icon-only Filters button in the panel header", () => {
    const slot = document.body.appendChild(document.createElement("div"));
    const { scanner, container, unmount } = renderPanel({}, undefined, slot, {
      selected: true,
      enabled: true,
      status: { kind: "following", availableCount: 4, targetCount: 4 },
      onSelect: vi.fn(),
      onToggle: vi.fn(),
    });
    try {
      act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
        payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [] } }));
      expect(within(slot).getByText("Scanner")).toBeTruthy();
      expect(within(slot).getByText("Pre-market")).toBeTruthy();
      expect(within(slot).queryByText("Sync to Following")).toBeNull();
      expect(within(slot).queryByText("4/4")).toBeNull();
      expect(within(slot).queryByText(/updated/i)).toBeNull();
      const filters = within(slot).getByRole("button", { name: "filters" });
      expect(filters.textContent).toBe("");
      fireEvent.click(filters);
      expect(within(container).getByText(/updated/i)).toBeTruthy();
      const sync = within(container).getByTestId("scanner-sync-control");
      const summary = within(container).getByTestId("scanner-filter-summary");
      const table = within(container).getByRole("table");
      expect(sync.compareDocumentPosition(summary) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(summary.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    } finally {
      unmount();
      slot.remove();
    }
  });

  it("selects Monitoring as the source, then toggles Sync without losing the source", () => {
    const onSelect = vi.fn();
    const onToggle = vi.fn();
    renderPanel({}, undefined, undefined, {
      selected: false,
      enabled: false,
      status: { kind: "disabled", availableCount: 0, targetCount: 4 },
      onSelect,
      onToggle,
    });
    fireEvent.click(screen.getByRole("button", { name: "Use this Scanner as Monitoring Source" }));
    expect(onSelect).toHaveBeenCalledOnce();
    cleanup();

    renderPanel({}, undefined, undefined, {
      selected: true,
      enabled: true,
      status: { kind: "following", availableCount: 4, targetCount: 4 },
      onSelect,
      onToggle,
    });
    expect(screen.getByText("4/4")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Disable Scanner Sync" }));
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("renders selected Sync as an accessible compact toggle", () => {
    const onToggle = vi.fn();
    renderPanel({}, undefined, undefined, {
      selected: true,
      enabled: true,
      status: { kind: "following", availableCount: 4, targetCount: 4 },
      onSelect: vi.fn(),
      onToggle,
    });
    const label = screen.getByText("Sync to Following");
    expect(label).toBeTruthy();
    expect(screen.getByText("ON")).toBeTruthy();
    expect(screen.getByText("4/4")).toBeTruthy();
    const toggle = screen.getByRole("button", { name: "Disable Scanner Sync" });
    expect(label.compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(toggle);
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("renders disabled Sync as OFF without a redundant status", () => {
    renderPanel({}, undefined, undefined, {
      selected: true,
      enabled: false,
      status: { kind: "disabled", availableCount: 0, targetCount: 4 },
      onSelect: vi.fn(),
      onToggle: vi.fn(),
    });
    expect(screen.getByText("OFF")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Enable Scanner Sync" }).getAttribute("aria-pressed")).toBe("false");
    expect(screen.queryByText("Sync off")).toBeNull();
  });

  it("renders incomplete coverage as a count without repeating Following", () => {
    renderPanel({}, undefined, undefined, {
      selected: true,
      enabled: true,
      status: { kind: "incomplete", availableCount: 3, targetCount: 6 },
      onSelect: vi.fn(),
      onToggle: vi.fn(),
    });
    expect(screen.getByText("3/6")).toBeTruthy();
    expect(screen.queryByText("Following 3/6")).toBeNull();
  });

  it("renders paused Sync compactly while retaining the detailed reason", () => {
    renderPanel({}, undefined, undefined, {
      selected: true,
      enabled: true,
      status: { kind: "paused", availableCount: 0, targetCount: 0, reason: "targets" },
      onSelect: vi.fn(),
      onToggle: vi.fn(),
    });
    expect(screen.getByText("Paused").getAttribute("title")).toBe("Paused — add a pinned Chart Panel");
    cleanup();

    renderPanel({}, undefined, undefined, {
      selected: true,
      enabled: true,
      status: { kind: "paused", availableCount: 0, targetCount: 6, reason: "rows" },
      onSelect: vi.fn(),
      onToggle: vi.fn(),
    });
    expect(screen.getByText("Paused").getAttribute("title")).toBe("Paused — waiting for Scanner rows");
  });

  it("offers a non-selected Scanner as the Monitoring source without a toggle", () => {
    const onSelect = vi.fn();
    renderPanel({}, undefined, undefined, {
      selected: false,
      enabled: false,
      status: { kind: "disabled", availableCount: 0, targetCount: 4 },
      onSelect,
      onToggle: vi.fn(),
    });
    expect(screen.getByText("Monitoring source")).toBeTruthy();
    expect(screen.getByText("Use this Scanner")).toBeTruthy();
    expect(screen.queryByText("ON")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Use this Scanner as Monitoring Source" }));
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it("renders the symbol column without the US. market prefix", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.KO", changePct: 18.4, last: 62.1, floatShares: 4_300_000_000, volume: 1_250_000 },
      ] } }));
    expect(screen.queryByText("US.KO")).toBeNull();
    expect(screen.getByText("KO")).toBeTruthy();
  });

  it("renders the derived SSR marker without decorating row actions", () => {
    const { scanner, focus } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.NVDA", shortSellRestricted: true, changePct: 12, last: 100, floatShares: 1, volume: 1 },
      ] } }));
    expect(screen.getByText("NVDA**")).toBeTruthy();
    fireEvent.doubleClick(screen.getByText("NVDA**"));
    expect(focus).toHaveBeenCalledWith("green", "US.NVDA");
    expect(focus).not.toHaveBeenCalledWith("green", "US.NVDA**");
  });

  it("renders an unrestricted scanner ticker without the marker", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.NVDA", shortSellRestricted: false, changePct: 12, last: 100, floatShares: 1, volume: 1 },
      ] } }));
    expect(screen.getByText("NVDA")).toBeTruthy();
    expect(screen.queryByText("NVDA**")).toBeNull();
  });

  it("renders no-print rows as em dash, never 0", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.WXYZ", changePct: null, last: null, floatShares: null, volume: 0 }] } }));
    const rowCells = screen.getByText("WXYZ").closest("tr")!.querySelectorAll("td");
    expect([...rowCells].map((c) => c.textContent)).toContain("—");
    expect([...rowCells].some((c) => c.textContent === "0%")).toBe(false);
  });

  it("row double-click publishes focus to the panel's linked group", () => {
    const { scanner, focus } = renderPanel({ group: "blue" });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.doubleClick(screen.getByText("KO"));
    expect(focus).toHaveBeenCalledWith("blue", "US.KO");
  });

  it("row double-click falls back to green when the panel is pinned (no linked group)", () => {
    const { scanner, focus } = renderPanel({ group: null });
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.doubleClick(screen.getByText("KO"));
    expect(focus).toHaveBeenCalledWith("green", "US.KO");
  });

  // config.group is frozen at panel creation (dockview never re-invokes the panel
  // factory with a fresh config after a later swatch re-pick) — PanelFrame threads
  // the live re-picked group through as the `group` prop instead.
  it("row double-click uses the live group prop, not the frozen config.group, after a group re-pick", () => {
    const { scanner, focus } = renderPanel({ group: "green" }, "blue");
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.doubleClick(screen.getByText("KO"));
    expect(focus).toHaveBeenCalledWith("blue", "US.KO");
  });

  it("a single row click only highlights the row — it never loads the symbol into the group", () => {
    const { scanner, focus } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    fireEvent.click(screen.getByText("KO"));
    expect(focus).not.toHaveBeenCalled();
    const row = screen.getByText("KO").closest("tr") as HTMLElement;
    expect(row.style.background).toBe("rgba(154, 106, 27, 0.16)");
  });

  it("right-click on a row shows an unconditional 'Add ... to watchlist' entry; clicking it sends WatchlistAdd for that row's symbol", () => {
    const { scanner, commands } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    const row = screen.getByText("KO").closest("tr") as HTMLElement;
    fireEvent.contextMenu(row, { clientX: 20, clientY: 30 });
    const btn = screen.getByRole("button", { name: "Add KO to watchlist" });
    expect(btn).toBeTruthy();
    fireEvent.click(btn);
    expect(commands.sendCommand).toHaveBeenCalledWith("WatchlistAdd", { symbol: "US.KO" });
  });

  it("hovering a non-selected, non-new-hit row shows the hover tint, cleared on mouse-leave", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    const row = screen.getByText("KO").closest("tr") as HTMLElement;
    expect(row.style.background).toBe("transparent");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe("rgba(154, 106, 27, 0.06)");
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe("transparent");
  });

  it("hovering a selected row leaves the selection background unchanged", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    const row = screen.getByText("KO").closest("tr") as HTMLElement;
    fireEvent.click(row);
    expect(row.style.background).toBe("rgba(154, 106, 27, 0.16)");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe("rgba(154, 106, 27, 0.16)");
  });

  it("hovering a new-hit row leaves the flash background unchanged", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [{ symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 }] } }));
    act(() => scanner.apply({ kind: "delta", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:05.000Z", rows: [
        { symbol: "US.KO", changePct: 5, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.NEW", changePct: 9, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    const row = screen.getByText("NEW").closest("tr") as HTMLElement;
    const newHitBackground = row.style.background;
    expect(newHitBackground).not.toBe("transparent");
    expect(newHitBackground).not.toBe("rgba(154, 106, 27, 0.06)");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe(newHitBackground);
  });

  it("has no persistent input row on load; the ⚙ button reveals the filter inputs", () => {
    renderPanel();
    expect(screen.queryByLabelText("min gain %")).toBeNull();
    expect(screen.queryByLabelText("float cap")).toBeNull();
    expect(screen.queryByLabelText("min volume")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    expect(screen.getByLabelText("min gain %")).toBeTruthy();
    expect(screen.getByLabelText("float cap")).toBeTruthy();
    expect(screen.getByLabelText("min volume")).toBeTruthy();
  });

  it("the summary line reflects the active thresholds", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket", payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [], filters: { mode: "gainers", minChangePct: 10, maxFloatShares: 20_000_000, minVolume: 100_000, floatUnit: "M", volumeUnit: "K" } } }));
    expect(screen.getByText(/change magnitude ≥ 10% · float ≤ 20M · vol ≥ 100k/)).toBeTruthy();
  });

  it("summary line reads 'no filters' when thresholds are off", () => {
    renderPanel();
    expect(screen.getByText(/no filters/)).toBeTruthy();
  });

  it("editing a threshold in the popover and clicking Apply persists via command", () => {
    const { commands } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    fireEvent.change(screen.getByLabelText("min gain %"), { target: { value: "7" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetScannerFilters", { filters: expect.objectContaining({ minChangePct: 7 }) });
  });

  it("Reset defaults clears the draft inputs without persisting until Apply", () => {
    const { onConfigChange } = renderPanel({ settings: { thresholds: { minChangePct: 10, floatCapShares: null, minVolume: 0 } } });
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    fireEvent.click(screen.getByRole("button", { name: "Reset defaults" }));
    expect((screen.getByLabelText("min gain %") as HTMLInputElement).value).toBe("0");
    expect(onConfigChange).not.toHaveBeenCalled();
  });

  it("offers Most active, hides change threshold, persists it, and resets sort to volume descending", () => {
    const { commands, onConfigChange } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /filters/i }));
    fireEvent.change(screen.getByLabelText("rank mode"), { target: { value: "most_active" } });
    expect(screen.queryByLabelText("min gain %")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetScannerFilters", { filters: expect.objectContaining({ mode: "most_active" }) });
    expect(onConfigChange).toHaveBeenCalledWith({ sort: { col: "vol", dir: "desc" } });
  });

  it("labels extended-hours Most active as approximate", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "afterhours", payload: {
      refreshedAt: "2026-07-08T21:00:00.000Z", rows: [], filters: { mode: "most_active", minChangePct: 99, maxFloatShares: null, minVolume: 0, floatUnit: "M", volumeUnit: "K" },
    } }));
    expect(screen.getByText(/Most active · approximate/)).toBeTruthy();
    expect(screen.queryByText(/change/)).toBeNull();
  });

  it("default view sorts by % change descending", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "premarket",
      payload: { refreshedAt: "2026-07-08T13:00:00.000Z", rows: [
        { symbol: "US.LOW", changePct: 2, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.HIGH", changePct: 40, last: 1, floatShares: 1, volume: 1 },
      ] } }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["HIGH", "LOW"]);
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
    expect(symbols).toEqual(["LOW", "HIGH"]);
  });

  it("follows the live session label", () => {
    const { scanner } = renderPanel();
    act(() => scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "afterhours",
      payload: { refreshedAt: "2026-07-08T21:00:00.000Z", rows: [
        { symbol: "US.AH", changePct: 3, last: 1, floatShares: 1, volume: 1 }] } }));
    expect(screen.getByText(/after-hours/i)).toBeTruthy();
  });
});
