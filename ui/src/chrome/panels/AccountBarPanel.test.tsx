// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { AccountBarPanel } from "./AccountBarPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, AccountRow, ExecStatus } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps(over: Partial<PanelProps> = {}) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }) };
  const props = { config: { id: "t-account", panelId: "account-bar", group: null, settings: {} }, stores, scheduler: {} as never, width: 800, height: 60, linkGroups: {} as never, commands, onConfigChange: () => {}, ...over } as PanelProps;
  return { props, stores, sent };
}
const acct = (venue: string, o: Partial<AccountRow> = {}): AccountRow => ({ venue, equity: 100, buyingPower: 400, availableCash: 50, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1, ...o });
const status = (masterArmed: boolean): ExecStatus => ({ masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });

function wrap(props: PanelProps) {
  return render(<ThemeProvider><ToastProvider><AccountBarPanel {...props} /></ToastProvider></ThemeProvider>);
}

describe("AccountBarPanel", () => {
  it("shows — for equity before any account snapshot arrives", () => {
    const { props } = mkProps();
    wrap(props);
    expect(screen.getByTestId("acct-equity").textContent).toBe("—");
  });
  it("aggregates day P&L across venues and shows armed state", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: acct("alpaca-paper", { dayPnl: 12 }) });
      stores.exec.apply({ kind: "delta", topic: "exec.account" as never, key: "tradezero-live", payload: acct("tradezero-live", { dayPnl: -5 }) });
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
    });
    expect(screen.getByTestId("acct-daypnl").textContent).toContain("7.00");
    expect(screen.getByTestId("arm-toggle").textContent).toMatch(/ARMED/i);
  });
  it("arm toggle sends Disarm when currently armed", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) }));
    fireEvent.click(screen.getByTestId("arm-toggle"));
    expect(sent.map((s) => s.name)).toContain("Disarm");
  });
  it("arms a venue when its per-venue control is clicked", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    const disarmedVenueStatus: ExecStatus = {
      masterArmed: false,
      global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
      venues: [{ venue: "sim-paper", broker: "alpaca", connected: true, venueArmed: false, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
    };
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: disarmedVenueStatus }));
    const btn = screen.getByTestId("venue-arm-sim-paper");
    expect(btn.getAttribute("data-armed")).toBe("false");
    fireEvent.click(btn);
    expect(sent).toContainEqual({ name: "Arm", args: { venue: "sim-paper" } });
  });
  it("disarms a venue when its per-venue control is clicked while armed", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) }));
    const btn = screen.getByTestId("venue-arm-alpaca-paper");
    expect(btn.getAttribute("data-armed")).toBe("true");
    fireEvent.click(btn);
    expect(sent).toContainEqual({ name: "Disarm", args: { venue: "alpaca-paper" } });
  });
  it("clicking one venue's control does not affect another venue's state or dispatch", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    const twoVenueStatus: ExecStatus = {
      masterArmed: false,
      global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
      venues: [
        { venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } },
        { venue: "tradezero-live", broker: "tradezero", connected: true, venueArmed: false, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } },
      ],
    };
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: twoVenueStatus }));
    const alpacaBtn = screen.getByTestId("venue-arm-alpaca-paper");
    const tzBtn = screen.getByTestId("venue-arm-tradezero-live");
    expect(alpacaBtn.getAttribute("data-armed")).toBe("true");
    expect(tzBtn.getAttribute("data-armed")).toBe("false");
    fireEvent.click(tzBtn);
    expect(sent).toContainEqual({ name: "Arm", args: { venue: "tradezero-live" } });
    expect(sent).not.toContainEqual({ name: "Disarm", args: { venue: "alpaca-paper" } });
    expect(sent).not.toContainEqual({ name: "Arm", args: { venue: "alpaca-paper" } });
    expect(alpacaBtn.getAttribute("data-armed")).toBe("true");
  });
});
