// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OpenOrdersPanel } from "./OpenOrdersPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, ExecStatus, Order } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps() {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }), sendQuery: vi.fn(async () => []) };
  const props = { config: { id: "t-orders", panelId: "open-orders", group: null, settings: {} }, stores, scheduler: {} as never, width: 500, height: 200, linkGroups: {} as never, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent };
}
const order = (id: string, o: Partial<Order> = {}): Order => ({ venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...o });
const statusReconciling = (): ExecStatus => ({ masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: true, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });
const wrap = (p: PanelProps) => render(<ThemeProvider><ToastProvider><OpenOrdersPanel {...p} /></ToastProvider></ThemeProvider>);

describe("OpenOrdersPanel", () => {
  it("shows an optimistic order as Pending", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.addOptimistic({ args: { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0 }, id: "ET9", createdMs: 100 }));
    expect(screen.getByText("Pending")).toBeTruthy();
  });
  it("shows a reject reason verbatim and no cancel button on a terminal row", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "REJECTED", rejectReason: "R78: market order in extended hours" })] }));
    expect(screen.getByText(/R78: market order in extended hours/)).toBeTruthy();
    expect(screen.queryByTestId("cancel-ET1")).toBeNull();
  });
  it("cancel on a working row sends CancelOrder; Cancel All cancels every working order", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1"), order("ET2", { status: "SUBMITTED" }), order("ET3", { status: "FILLED" })] }));
    fireEvent.click(screen.getByTestId("cancel-ET1"));
    expect(sent.at(-1)).toEqual({ name: "CancelOrder", args: { venue: "alpaca-paper", orderId: "ET1" } });
    fireEvent.click(screen.getByTestId("cancel-all"));
    expect(sent.filter((s) => s.name === "CancelOrder").map((s) => (s.args as { orderId: string }).orderId).sort()).toEqual(["ET1", "ET1", "ET2"]);
  });
  it("shows the StreamGap reconcile badge when a venue is reconciling", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: statusReconciling() }));
    expect(screen.getByText(/verify before acting/i)).toBeTruthy();
  });
});
