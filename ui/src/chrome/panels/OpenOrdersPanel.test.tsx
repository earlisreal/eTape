// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OpenOrdersPanel } from "./OpenOrdersPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, ExecStatus, Order } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps(settings: Record<string, unknown> = {}) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const onConfigChange = vi.fn();
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }), sendQuery: vi.fn(async () => []) };
  const props = { config: { id: "t-orders", panelId: "open-orders", group: null, settings }, stores, scheduler: {} as never, width: 500, height: 200, linkGroups: {} as never, commands, onConfigChange } as PanelProps;
  return { props, stores, sent, onConfigChange };
}
const order = (id: string, o: Partial<Order> = {}): Order => ({ venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...o });
const statusReconciling = (): ExecStatus => ({ masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: true, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });
const wrap = (p: PanelProps) => render(<ThemeProvider><ToastProvider><OpenOrdersPanel {...p} /></ToastProvider></ThemeProvider>);

describe("OpenOrdersPanel", () => {
  it("shows an optimistic order as a Pending chip (bronze .chip-pending)", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.addOptimistic({ args: { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 10, limitPrice: 3.5, stopPrice: 0 }, id: "ET9", createdMs: 100 }));
    const chip = screen.getByText("Pending");
    expect(chip.className).toContain("chip-pending");
    expect(chip.getAttribute("data-chip")).toBe("pending");
  });
  it("shows a working order as a Submitted/Accepted chip (green outline .chip-working)", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "ACCEPTED" }), order("ET2", { status: "SUBMITTED", createdMs: 2 })] }));
    const accepted = screen.getByText("Accepted");
    expect(accepted.className).toContain("chip-working");
    expect(accepted.getAttribute("data-chip")).toBe("working");
    const submitted = screen.getByText("Submitted");
    expect(submitted.className).toContain("chip-working");
  });
  it("shows a reject reason verbatim, a rejected chip, and no cancel button on a terminal row", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "REJECTED", rejectReason: "R78: market order in extended hours" })] }));
    expect(screen.getByText(/R78: market order in extended hours/)).toBeTruthy();
    const chip = screen.getByText("Rejected");
    expect(chip.className).toContain("chip-rejected");
    expect(chip.getAttribute("data-chip")).toBe("rejected");
    expect(screen.queryByTestId("cancel-ET1")).toBeNull();
  });
  it("shows a terminal, non-rejected status as plain muted text (no chip)", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "FILLED" })] }));
    const filled = screen.getByText("Filled");
    expect(filled.className).not.toContain("chip");
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
  it("shows the stream-gap reconcile badge, bronze, with the exact copy", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: statusReconciling() }));
    const badge = screen.getByTestId("reconcile-badge");
    expect(badge.textContent).toBe("stream gap — reconciled, verify");
    expect(badge.className).toContain("chip-pending");
  });
  it("defaults to created-time descending with no sort-active header", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [
      order("ET1", { symbol: "US.AAPL", createdMs: 1 }),
      order("ET2", { symbol: "US.MSFT", createdMs: 3 }),
      order("ET3", { symbol: "US.TSLA", createdMs: 2 }),
    ] }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["MSFT", "TSLA", "AAPL"]); // createdMs desc: ET2(3), ET3(2), ET1(1)
    expect(document.querySelectorAll("thead .sort-active").length).toBe(0);
  });
  it("clicking the Symbol header sorts by symbol and persists it via onConfigChange", () => {
    const { props, stores, onConfigChange } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [
      order("ET1", { symbol: "US.AAPL", createdMs: 1 }),
      order("ET2", { symbol: "US.MSFT", createdMs: 3 }),
      order("ET3", { symbol: "US.TSLA", createdMs: 2 }),
    ] }));
    fireEvent.click(screen.getByRole("columnheader", { name: /Symbol/ }));
    expect(onConfigChange).toHaveBeenCalledWith(expect.objectContaining({ sort: { col: "symbol", dir: "desc" } }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["TSLA", "MSFT", "AAPL"]);
  });
  it("restores a persisted sort from settings", () => {
    const { props, stores } = mkProps({ sort: { col: "symbol", dir: "asc" } });
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [
      order("ET1", { symbol: "US.AAPL", createdMs: 1 }),
      order("ET2", { symbol: "US.MSFT", createdMs: 3 }),
      order("ET3", { symbol: "US.TSLA", createdMs: 2 }),
    ] }));
    const symbols = [...document.querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
    expect(symbols).toEqual(["AAPL", "MSFT", "TSLA"]);
    expect(screen.getByRole("columnheader", { name: /Symbol/ }).className).toContain("sort-active");
  });
});
