// @vitest-environment jsdom
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ScreenerPanel } from "./ScreenerPanel";
import { ThemeProvider } from "../ThemeProvider";
import { makeStores } from "../../data/registry";
import { LinkGroups } from "../linkGroups";
import { DEFAULT_SCREENER_FILTERS } from "../screenerFilters";
import type { ScreenerRow, ScreenerState } from "../../wire/contract";

const screenState = (overrides: Partial<ScreenerState> = {}): ScreenerState => ({ panelId: "main/screen", runId: 1, status: "complete", asOf: "2026-11-04", startedAt: "", finishedAt: "", filters: { ...DEFAULT_SCREENER_FILTERS }, universe: 100, candidates: 2, checked: 2, unavailable: 0, warning: "", error: "", rows: [], ...overrides });
const valueOf = (label: string) => (screen.getByLabelText(label) as HTMLInputElement).value;

function setup(settings: Record<string, unknown> = {}) {
  const stores = makeStores();
  const bus = { post: vi.fn(), onMessage: () => () => {}, close: vi.fn() };
  const linkGroups = new LinkGroups(bus, () => {});
  const focus = vi.spyOn(linkGroups, "focus").mockImplementation(() => {});
  const commands = { sendCommand: vi.fn(async () => ({ kind: "ack" as const, corrId: "test", status: "accepted" as const })), sendQuery: vi.fn(async () => ({})) };
  const onConfigChange = vi.fn();
  render(<ThemeProvider><ScreenerPanel config={{ id: "screen", panelId: "screener", group: "red", settings }} screenerId="main/screen" stores={stores} commands={commands} onConfigChange={onConfigChange} linkGroups={linkGroups} /></ThemeProvider>);
  return { stores, commands, onConfigChange, focus };
}
const row = (symbol: string, distancePct = 3): ScreenerRow => ({ symbol, name: symbol, close: 97, high: 100, previousHigh: 100, distancePct, newHigh: false, closeConfirmed: false, historyStart: "2020-01-02" });

describe("ScreenerPanel", () => {
  it("runs the agreed EOD high settings and persists only its settings patch", async () => {
    const { commands, onConfigChange } = setup({ unrelated: "preserve" });
    expect(valueOf("High lookback")).toBe("all-time");
    fireEvent.change(screen.getByLabelText("High lookback"), { target: { value: "trading-days" } });
    fireEvent.change(screen.getByLabelText("Trading days"), { target: { value: "90" } });
    fireEvent.change(screen.getByLabelText("High condition"), { target: { value: "new" } });
    expect(screen.queryByLabelText("Within % of high")).toBeNull();
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Run screen" })));
    expect(commands.sendCommand).toHaveBeenCalledWith("RunScreener", { panelId: "main/screen", filters: { ...DEFAULT_SCREENER_FILTERS, lookback: "trading-days", tradingDays: 90, mode: "new" } });
    expect(onConfigChange.mock.calls.every(([patch]) => Object.keys(patch).join() === "screenerFilters")).toBe(true);
  });
  it("shows stable results, sorts them, navigates, and saves watchlist symbols", async () => {
    const { stores, commands, focus } = setup();
    act(() => stores.screener.apply({ kind: "snapshot", topic: "screener.state", payload: screenState({ rows: [row("US.ZZZ", 4), row("US.AAA", 1)] }) }));
    expect(screen.getByRole("status").textContent).toContain("2026-11-04");
    const table = screen.getByRole("table", { name: "Screener results" });
    expect(within(table).getAllByRole("row")[1].textContent).toContain("AAA");
    fireEvent.doubleClick(screen.getByText("AAA"));
    expect(focus).toHaveBeenCalledWith("red", "US.AAA");
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add AAA to watchlist" })));
    expect(commands.sendCommand).toHaveBeenCalledWith("WatchlistAdd", { symbol: "US.AAA" });
    fireEvent.change(screen.getByLabelText("High lookback"), { target: { value: "6-month" } });
    expect(screen.getByText("Filters changed. Run screen to update results.")).toBeTruthy();
    expect(screen.getByText("AAA")).toBeTruthy();
  });
  it("saves and restores presets including a zero momentum bound", () => {
    const { onConfigChange } = setup();
    fireEvent.click(screen.getByText("Additional filters"));
    fireEvent.change(screen.getByLabelText("Minimum 1M change (%)"), { target: { value: "0" } });
    expect(valueOf("Minimum 1M change (%)")).toBe("0");
    fireEvent.click(screen.getByText("Save a preset"));
    fireEvent.change(screen.getByLabelText("Preset name"), { target: { value: "My swing screen" } });
    fireEvent.click(screen.getByRole("button", { name: "Save preset" }));
    expect(onConfigChange).toHaveBeenLastCalledWith({ screenerPresets: [{ name: "My swing screen", filters: { ...DEFAULT_SCREENER_FILTERS, minChangePct: 0 } }] });
    fireEvent.change(screen.getByLabelText("Screener preset"), { target: { value: "builtin-new" } });
    expect(valueOf("High condition")).toBe("new");
    fireEvent.change(screen.getByLabelText("Screener preset"), { target: { value: "saved:My swing screen" } });
    expect(valueOf("Minimum 1M change (%)")).toBe("0");
  });
  it("shows incomplete coverage and permits cancellation", async () => {
    const { stores, commands } = setup();
    act(() => stores.screener.apply({ kind: "snapshot", topic: "screener.state", payload: screenState({ status: "running", unavailable: 2 }) }));
    expect(screen.getByText(/2 stocks unavailable/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "Screening…" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Cancel" })));
    expect(commands.sendCommand).toHaveBeenCalledWith("CancelScreener", { panelId: "main/screen" });
  });
});
