// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import type { ActionTemplate, OrderConfig } from "./actionTemplate";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus } from "../../wire/contract";

// Real parameter type — casting a function's own param as `never` fails typecheck.
function Harness(props: Parameters<typeof useHotkeys>[0]) { useHotkeys(props); return null; }
const status = (masterArmed: boolean): ExecStatus => ({ masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });

// This suite exercises useHotkeys' own dispatch behavior, not eTape's actual
// defaults — DEFAULT_ORDER_CONFIG ships blank (no default templates/hotkeys),
// so seed the shared OrderConfigProvider context (via its GetConfig read)
// with a local fixture carrying the two bindings these tests fire: Ctrl+1 for
// a place order, Ctrl+Shift+K for KillSwitch.
const SAMPLE_TEMPLATES: ActionTemplate[] = [
  { kind: "place", id: "buy-5k", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, hotkey: "Ctrl+1" },
  { kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" },
];
const SAMPLE_ORDER_CONFIG: OrderConfig = { activeVenue: "", templates: SAMPLE_TEMPLATES };

// Async + wraps render in `act` with a microtask flush: OrderConfigProvider's
// GetConfig read (and the setConfig it triggers) resolves on a microtask, and
// the templates it carries — including the Ctrl+1 / Ctrl+Shift+K bindings
// these tests rely on — only exist in `config` after that resolves. Without
// the flush, the keydown below fires against the provider's pre-load initial
// state, which is DEFAULT_ORDER_CONFIG's now-empty template list.
async function setup(masterArmed: boolean) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = {
    sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => {
      sent.push({ name: n, args: a });
      if (n === "GetConfig") return { kind: "ack", corrId: "c", status: "accepted", value: SAMPLE_ORDER_CONFIG };
      return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined };
    }),
  };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(masterArmed) });
  stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: { venue: "alpaca-paper", equity: 100, buyingPower: 100000, availableCash: 100, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1 } });
  stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
  linkGroups.focus("green", "US.AAPL");
  await act(async () => {
    render(
      <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
        <Harness stores={stores} commands={commands} linkGroups={linkGroups} group="green" />
      </OrderConfigProvider></ToastProvider></ThemeProvider>,
    );
    await Promise.resolve();
  });
  return { sent };
}

// Braced bodies (not implicit-return expressions): an arrow function that
// *returns* a value from beforeEach — here, the callable MockInstance from
// mockReturnValue() — is treated by Vitest as an implicit per-test teardown
// and invoked again after afterEach's restoreAllMocks() has already reverted
// the spy, calling jsdom's real hasFocus() unbound and throwing "not a valid
// instance of Document". Braces make beforeEach/afterEach return undefined.
beforeEach(() => { vi.spyOn(document, "hasFocus").mockReturnValue(true); });
afterEach(() => { vi.restoreAllMocks(); });

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
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
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
    linkGroups.focus("green", "US.AAPL");
    linkGroups.focusVenue("green", "tradezero"); // green group's venue is the SECOND one
    await act(async () => {
      render(
        <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
          <Harness stores={stores} commands={commands} linkGroups={linkGroups} group="green" />
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
