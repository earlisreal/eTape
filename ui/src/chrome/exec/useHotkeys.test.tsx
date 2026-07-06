// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "./useOrderConfig";
import { useHotkeys } from "./useHotkeys";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus } from "../../wire/contract";

// Real parameter type — casting a function's own param as `never` fails typecheck.
function Harness(props: Parameters<typeof useHotkeys>[0]) { useHotkeys(props); return null; }
const status = (masterArmed: boolean): ExecStatus => ({ masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });

function setup(masterArmed: boolean) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (n: string, a: unknown): Promise<AckMsg> => { sent.push({ name: n, args: a }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined }; }) };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(masterArmed) });
  stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: { venue: "alpaca-paper", equity: 100, buyingPower: 100000, availableCash: 100, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1 } });
  stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
  linkGroups.focus("green", "US.AAPL");
  render(
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
      <Harness stores={stores} commands={commands} linkGroups={linkGroups} group="green" />
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
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
    const { sent } = setup(true);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true);
  });
  it("blocks a place-hotkey when disarmed (no send)", async () => {
    const { sent } = setup(false);
    await act(async () => { fireEvent.keyDown(window, { key: "1", ctrlKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(false);
  });
  it("fires a management hotkey (kill) even when disarmed", async () => {
    const { sent } = setup(false);
    await act(async () => { fireEvent.keyDown(window, { key: "k", ctrlKey: true, shiftKey: true }); await Promise.resolve(); });
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(true);
  });
});
