// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import type { ActionTemplate, OrderConfig } from "./actionTemplate";
import { makeStores } from "../../data/registry";
import type { AckMsg, ExecStatus } from "../../wire/contract";
import type { HotkeyTarget } from "../hotkeyTarget";
import { modalTracker } from "../modalTracker";

// Real parameter type — casting a function's own param as `never` fails typecheck.
function Harness(props: Parameters<typeof useHotkeys>[0]) { useHotkeys(props); return null; }
const status = (masterArmed: boolean): ExecStatus => ({ masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });

// This suite exercises useHotkeys' own dispatch behavior, not eTape's actual
// defaults — DEFAULT_ORDER_CONFIG ships blank (no default templates/hotkeys),
// so seed the shared OrderConfigProvider context (via its GetConfig read)
// with a local fixture carrying the bindings these tests fire.
const SAMPLE_TEMPLATES: ActionTemplate[] = [
  { kind: "place", id: "buy-5k", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, hotkey: "Ctrl+1" },
  { kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" },
  { kind: "manage", id: "cancel-last", label: "Cancel Last", action: "CancelLast", hotkey: "Ctrl+Backspace" },
  { kind: "manage", id: "cancel-focused", label: "Cancel Focused", action: "CancelAllFocused", hotkey: "Ctrl+Shift+C" },
  { kind: "manage", id: "cancel-everything", label: "Cancel Everything", action: "CancelAllEverything", hotkey: "Ctrl+Shift+E" },
];
const SAMPLE_ORDER_CONFIG: OrderConfig = { activeVenue: "", templates: SAMPLE_TEMPLATES };
const TARGET: HotkeyTarget = { ownerWindow: "test-window", panel: "chart-1", group: "green", symbol: "US.AAPL", venue: "alpaca-paper", revision: 1 };

// Async + wraps render in `act` with a microtask flush: OrderConfigProvider's
// GetConfig read (and the setConfig it triggers) resolves on a microtask, and
// the templates it carries — including the Ctrl+1 / Ctrl+Shift+K bindings
// these tests rely on — only exist in `config` after that resolves. Without
// the flush, the keydown below fires against the provider's pre-load initial
// state, which is DEFAULT_ORDER_CONFIG's now-empty template list.
async function setup(masterArmed: boolean, target: HotkeyTarget | null = TARGET) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = {
    sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => {
      sent.push({ name: n, args: a });
      if (n === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: SAMPLE_ORDER_CONFIG };
      return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined };
    }),
  };
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(masterArmed) });
  stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: { venue: "alpaca-paper", equity: 100, buyingPower: 100000, availableCash: 100, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1 } });
  stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
  await act(async () => {
    render(
      <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
        <Harness stores={stores} commands={commands} target={target} />
      </OrderConfigProvider></ToastProvider></ThemeProvider>,
    );
    await Promise.resolve();
  });
  return { sent, stores };
}

// Braced bodies (not implicit-return expressions): an arrow function that
// *returns* a value from beforeEach — here, the callable MockInstance from
// mockReturnValue() — is treated by Vitest as an implicit per-test teardown
// and invoked again after afterEach's restoreAllMocks() has already reverted
// the spy, calling jsdom's real hasFocus() unbound and throwing "not a valid
// instance of Document". Braces make beforeEach/afterEach return undefined.
beforeEach(() => { vi.spyOn(document, "hasFocus").mockReturnValue(true); });
afterEach(() => { modalTracker.setOpen(false); vi.restoreAllMocks(); });

describe("useHotkeys", () => {
  it("fires a place-hotkey when armed", async () => {
    const { sent } = await setup(true);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true);
  });
  it("blocks a place-hotkey when disarmed (no send)", async () => {
    const { sent } = await setup(false);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(false);
  });
  it("fires a management hotkey (kill) even when disarmed", async () => {
    const { sent } = await setup(false);
    await act(async () => { fireEvent.keyDown(window, { key: "k", ctrlKey: true, shiftKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(true);
  });
  it("blocks a scoped hotkey without a grouped target and shows a warning", async () => {
    const { sent } = await setup(true, null);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(false);
    expect(screen.getByRole("alert").textContent).toMatch(/grouped panel target/i);
  });
  it("consumes repeat events without firing the binding", async () => {
    const { sent } = await setup(true);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true, repeat: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(false);
  });
  it("pauses scoped actions while a modal is open or an editor has focus", async () => {
    const modal = await setup(true);
    act(() => modalTracker.setOpen(true));
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(modal.sent.some((s) => s.name === "SubmitOrder")).toBe(false);

    modalTracker.setOpen(false);
    const editor = document.createElement("input");
    document.body.appendChild(editor);
    editor.focus();
    const focused = await setup(true);
    await act(async () => { fireEvent.keyDown(editor, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(focused.sent.some((s) => s.name === "SubmitOrder")).toBe(false);
    editor.remove();
  });
  it("keeps Kill Switch available without a target, modal, editor focus, or arm", async () => {
    const editor = document.createElement("input");
    document.body.appendChild(editor);
    editor.focus();
    const { sent } = await setup(false, null);
    act(() => modalTracker.setOpen(true));
    await act(async () => { fireEvent.keyDown(editor, { key: "k", ctrlKey: true, shiftKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(true);
    editor.remove();
  });
  it("keeps Cancel All Everything available without a target, modal, editor focus, or arm", async () => {
    const editor = document.createElement("input");
    document.body.appendChild(editor);
    editor.focus();
    const { sent, stores } = await setup(false, null);
    vi.spyOn(stores.exec, "workingOrdersFor").mockReturnValue([{ id: "ETX", venue: "alpaca-paper" } as never]);
    act(() => modalTracker.setOpen(true));
    await act(async () => { fireEvent.keyDown(editor, { key: "E", ctrlKey: true, shiftKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "CancelOrder" && (s.args as { orderId: string }).orderId === "ETX")).toBe(true);
    editor.remove();
  });
  it("requires a symbol before scoped cancels", async () => {
    const { sent } = await setup(true, { ownerWindow: TARGET.ownerWindow, panel: TARGET.panel, group: TARGET.group, venue: "alpaca-paper", revision: TARGET.revision });
    await act(async () => { fireEvent.keyDown(window, { key: "Backspace", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "CancelOrder")).toBe(false);
    expect(screen.getByRole("alert").textContent).toMatch(/no symbol/i);
  });
  it("blocks a place-hotkey when the document lacks OS focus, even when armed", async () => {
    vi.spyOn(document, "hasFocus").mockReturnValue(false);
    const { sent } = await setup(true);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(false);
  });
  it("fires the place hotkey at the group's focused venue, not just the first venue", async () => {
    const stores = makeStores();
    const sent: Array<{ name: string; args: unknown }> = [];
    const commands = {
      sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => {
        sent.push({ name: n, args: a });
        if (n === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: SAMPLE_ORDER_CONFIG };
        return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined };
      }),
    };
    const twoArmed: ExecStatus = {
      masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
      venues: [
        { venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } },
        { venue: "tradezero", broker: "tradezero", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } },
      ],
    };
    stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: twoArmed });
    stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "tradezero", payload: { venue: "tradezero", equity: 100, buyingPower: 100000, availableCash: 100, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1 } });
    stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
    const target: HotkeyTarget = { ...TARGET, venue: "tradezero" };
    await act(async () => {
      render(
        <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
          <Harness stores={stores} commands={commands} target={target} />
        </OrderConfigProvider></ToastProvider></ThemeProvider>,
      );
      await Promise.resolve();
    });
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    const submit = sent.find((s) => s.name === "SubmitOrder");
    expect(submit).toBeTruthy();
    expect((submit!.args as { venue: string }).venue).toBe("tradezero");
  });
});
