// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { LinkGroups } from "../linkGroups";
import { makeStores } from "../../data/registry";
import { WatchlistPanel } from "./WatchlistPanel";
import type { PanelProps } from "./registry";
import type { PanelConfig } from "../workspace";

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

function renderPanel(
  over: Partial<PanelConfig> = {},
  ackImpl?: () => Promise<{ status: "accepted" | "blocked"; reason?: string }>,
  groupProp?: PanelConfig["group"],
) {
  const stores = makeStores();
  const watchlist = stores.watchlist;
  const focus = vi.fn();
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  vi.spyOn(linkGroups, "focus").mockImplementation(focus);
  const onConfigChange = vi.fn();
  const config: PanelConfig = { id: "m-watchlist", panelId: "watchlist", group: null, settings: {}, ...over };
  const commands = { sendCommand: vi.fn(ackImpl ?? (async () => ({ status: "accepted" as const }))) };
  const props = { config, stores, linkGroups, onConfigChange, scheduler: {} as never,
    width: 400, height: 300, commands, group: groupProp } as unknown as PanelProps;
  render(<ThemeProvider><ToastProvider><WatchlistPanel {...props} /></ToastProvider></ThemeProvider>);
  return { watchlist, focus, onConfigChange, commands };
}

describe("WatchlistPanel", () => {
  it("renders rows in the snapshot's payload order (symbols array), not the rows array's order", () => {
    const { watchlist } = renderPanel();
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.ZZZ", "US.AAA"], rows: [
        { symbol: "US.AAA", changePct: 1, last: 10, volume: 100 },
        { symbol: "US.ZZZ", changePct: 2, last: 20, volume: 200 },
      ] } }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["ZZZ", "AAA"]);
  });

  it("renders dash placeholders for a symbol present in symbols but absent from rows", () => {
    const { watchlist } = renderPanel();
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.KO", "US.MISSING"], rows: [
        { symbol: "US.KO", changePct: 5, last: 62.1, volume: 1_000 },
      ] } }));
    expect(screen.getByText("MISSING")).toBeTruthy();
    const cells = screen.getByText("MISSING").closest("tr")!.querySelectorAll("td");
    expect([...cells].map((c) => c.textContent)).toEqual(["MISSING", "—", "—", "—"]);
  });

  it("sign-colors %Chg via palette.up/down", () => {
    const { watchlist } = renderPanel();
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.UP", "US.DOWN"], rows: [
        { symbol: "US.UP", changePct: 3.4, last: 1, volume: 1 },
        { symbol: "US.DOWN", changePct: -2.1, last: 1, volume: 1 },
      ] } }));
    const upCell = screen.getByText("+3.4%");
    const downCell = screen.getByText("−2.1%");
    expect(upCell.style.color).not.toBe(downCell.style.color);
    expect(upCell.style.color).not.toBe("");
    expect(downCell.style.color).not.toBe("");
  });

  it("clicking a column header sorts the rows", () => {
    const { watchlist } = renderPanel();
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.LOW", "US.HIGH"], rows: [
        { symbol: "US.LOW", changePct: 2, last: 1, volume: 1 },
        { symbol: "US.HIGH", changePct: 40, last: 1, volume: 1 },
      ] } }));
    // Default (unsorted) order matches payload order.
    expect([...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent)).toEqual(["LOW", "HIGH"]);
    fireEvent.click(screen.getByRole("columnheader", { name: /%Chg/ }));
    expect([...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent)).toEqual(["HIGH", "LOW"]);
  });

  it("double-click calls linkGroups.focus with the panel's group and the row's symbol", () => {
    const { watchlist, focus } = renderPanel({ group: "blue" });
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.KO"], rows: [
        { symbol: "US.KO", changePct: 5, last: 1, volume: 1 },
      ] } }));
    fireEvent.doubleClick(screen.getByText("KO"));
    expect(focus).toHaveBeenCalledWith("blue", "US.KO");
  });

  it("double-click falls back to green when the panel has no linked group", () => {
    const { watchlist, focus } = renderPanel({ group: null });
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.KO"], rows: [
        { symbol: "US.KO", changePct: 5, last: 1, volume: 1 },
      ] } }));
    fireEvent.doubleClick(screen.getByText("KO"));
    expect(focus).toHaveBeenCalledWith("green", "US.KO");
  });

  // config.group is frozen at panel creation (dockview never re-invokes the panel
  // factory with a fresh config after a later swatch re-pick) — PanelFrame threads
  // the live re-picked group through as the `group` prop instead. A panel that reads
  // config.group directly (bypassing that live prop) would broadcast to whatever
  // group the panel started in, not the one the user actually re-picked it into.
  it("double-click uses the live group prop, not the frozen config.group, after a group re-pick", () => {
    const { watchlist, focus } = renderPanel({ group: "green" }, undefined, "blue");
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.KO"], rows: [
        { symbol: "US.KO", changePct: 5, last: 1, volume: 1 },
      ] } }));
    fireEvent.doubleClick(screen.getByText("KO"));
    expect(focus).toHaveBeenCalledWith("blue", "US.KO");
  });

  it("add-input: Enter with a non-empty value sends WatchlistAdd and clears the input on an accepted ack", async () => {
    const { commands } = renderPanel({}, async () => ({ status: "accepted" }));
    const input = screen.getByLabelText("add symbol to watchlist") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "US.NVDA" } });
    await act(async () => { fireEvent.keyDown(input, { key: "Enter" }); });
    expect(commands.sendCommand).toHaveBeenCalledWith("WatchlistAdd", { symbol: "US.NVDA" });
    expect(input.value).toBe("");
  });

  it("add-input: keeps the typed value and shows a warn toast when the ack is blocked", async () => {
    renderPanel({}, async () => ({ status: "blocked", reason: "already watching 25 symbols" }));
    const input = screen.getByLabelText("add symbol to watchlist") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "US.NVDA" } });
    await act(async () => { fireEvent.keyDown(input, { key: "Enter" }); });
    expect(input.value).toBe("US.NVDA");
    expect(screen.getByText("already watching 25 symbols")).toBeTruthy();
  });

  it("right-click a row shows 'Remove ... from watchlist'; clicking it sends WatchlistRemove", () => {
    const { watchlist, commands } = renderPanel();
    act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
      payload: { refreshedAt: "2026-07-12T13:00:00.000Z", symbols: ["US.KO"], rows: [
        { symbol: "US.KO", changePct: 5, last: 1, volume: 1 },
      ] } }));
    const row = screen.getByText("KO").closest("tr") as HTMLElement;
    fireEvent.contextMenu(row, { clientX: 20, clientY: 30 });
    const btn = screen.getByRole("button", { name: "Remove KO from watchlist" });
    expect(btn).toBeTruthy();
    fireEvent.click(btn);
    expect(commands.sendCommand).toHaveBeenCalledWith("WatchlistRemove", { symbol: "US.KO" });
  });

  it("renders the empty state (with the same add input) when symbols is empty", () => {
    renderPanel();
    expect(screen.getByText("Add a symbol to start your watchlist")).toBeTruthy();
    expect(screen.getAllByLabelText("add symbol to watchlist")).toHaveLength(1);
  });

  it("dims the data columns when the snapshot goes stale, re-evaluated on a timer", () => {
    vi.useFakeTimers();
    try {
      const { watchlist } = renderPanel();
      act(() => watchlist.apply({ kind: "snapshot", topic: "watchlist.rows",
        payload: { refreshedAt: new Date().toISOString(), symbols: ["US.KO"], rows: [
          { symbol: "US.KO", changePct: 5, last: 1, volume: 1 },
        ] } }));
      const lastCell = screen.getByText("1.00");
      expect(lastCell.style.opacity).toBe("1");
      act(() => { vi.advanceTimersByTime(12_000); });
      expect(lastCell.style.opacity).toBe("0.55");
    } finally {
      vi.useRealTimers();
    }
  });
});
