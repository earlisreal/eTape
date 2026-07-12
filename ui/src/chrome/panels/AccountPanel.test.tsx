// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { AccountPanel } from "./AccountPanel";
import { makeStores } from "../../data/registry";
import { LinkGroups } from "../linkGroups";
import { FakeBus, FakeBusHub } from "../../../test/fakes";
import type { AckMsg, AccountRow, ClosedTradeRow, ExecStatus, Order, PositionRow, SubmitOrderArgs } from "../../wire/contract";
import type { PanelProps } from "./registry";
import type { LinkGroup } from "../linkGroups";

function mkProps(group: LinkGroup = null) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const configChanges: Array<Record<string, unknown>> = [];
  const commands = {
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted" }; }),
    sendQuery: vi.fn(async () => []),
  };
  const linkGroups = new LinkGroups(new FakeBus(new FakeBusHub()), () => {});
  const props = {
    config: { id: "t-account", panelId: "account", group, settings: {} },
    stores, scheduler: {} as never, width: 800, height: 400, linkGroups, commands,
    onConfigChange: (s: Record<string, unknown>) => configChanges.push(s),
  } as PanelProps;
  return { props, stores, sent, configChanges, linkGroups };
}
const acct = (venue: string, o: Partial<AccountRow> = {}): AccountRow => ({ venue, equity: 100, buyingPower: 400, availableCash: 50, sodEquity: 100, realized: 0, dayPnl: 0, leverage: 4, tsMs: 1, ...o });
const status = (masterArmed: boolean, ...venueIds: string[]): ExecStatus => ({
  masterArmed, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: (venueIds.length ? venueIds : ["alpaca-paper"]).map((venue) => ({
    venue, broker: "alpaca", connected: true, reconcilePending: false,
    note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
  })),
});
const pos = (o: Partial<PositionRow>): PositionRow => ({ venue: "alpaca-paper", symbol: "US.AAPL", qty: 300, avgPrice: 3.4, unrealizedPnl: 30, ...o });

function wrap(props: PanelProps) {
  return render(
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={props.commands}>
      <AccountPanel {...props} />
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
}

describe("AccountPanel", () => {
  // --- ported from AccountBarPanel.test.tsx ---
  it("shows — for equity before any account snapshot arrives", () => {
    const { props } = mkProps();
    wrap(props);
    expect(screen.getByTestId("acct-equity").textContent).toBe("—");
  });
  // --- new: venue dropdown scopes stats/positions (Task 10) ---
  it("scopes stats to the selected venue", () => {
    const { props, stores, linkGroups } = mkProps("green");
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper", "alpaca-live") });
      stores.exec.apply({ kind: "snapshot", topic: "exec.account" as never, key: "alpaca-paper", payload: acct("alpaca-paper", { equity: 99 }) });
      stores.exec.apply({ kind: "delta", topic: "exec.account" as never, key: "alpaca-live", payload: acct("alpaca-live", { equity: 12 }) });
      linkGroups.focusVenue("green", "alpaca-live");
    });
    wrap(props);
    expect(screen.getByTestId("acct-equity").textContent).toContain("12.00");
    fireEvent.change(screen.getByTestId("acct-venue"), { target: { value: "alpaca-paper" } });
    expect(screen.getByTestId("acct-equity").textContent).toContain("99.00");
  });

  it("filters positions to the selected venue and drops NET rows", () => {
    const { props, stores, linkGroups } = mkProps("green");
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper", "alpaca-live") });
      stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [
        pos({ venue: "alpaca-paper", symbol: "US.AAPL" }),
        pos({ venue: "alpaca-live", symbol: "US.MSFT" }),
        pos({ venue: null, symbol: "US.AAPL" }), // NET aggregate
      ] });
      linkGroups.focusVenue("green", "alpaca-paper");
    });
    wrap(props);
    expect(screen.queryByTestId("pos-net")).toBeNull();
    expect(screen.getByText("AAPL")).toBeTruthy();
    expect(screen.queryByText("MSFT")).toBeNull();
  });

  it("drops flat (0-qty) positions from the table and its count", () => {
    const { props, stores, linkGroups } = mkProps("green");
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper") });
      stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [
        pos({ venue: "alpaca-paper", symbol: "US.AAPL" }),
        pos({ venue: "alpaca-paper", symbol: "US.MSFT", qty: 0 }),
      ] });
      linkGroups.focusVenue("green", "alpaca-paper");
    });
    wrap(props);
    expect(screen.getByText("AAPL")).toBeTruthy();
    expect(screen.queryByText("MSFT")).toBeNull();
    expect(screen.getByText("Positions (1)")).toBeTruthy();
  });

  // --- ported from PositionsPanel.test.tsx ---
  it("flatten on a long row submits a SELL for the full qty (priced from the quote)", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.5, ask: 3.51, last: 3.5, ts: "" } });
      stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({ qty: 300 })] });
    });
    fireEvent.click(screen.getByTestId("flatten-alpaca-paper-US.AAPL"));
    const submit = sent.find((s) => s.name === "SubmitOrder");
    const args = submit?.args as SubmitOrderArgs;
    expect(args.side).toBe("SELL");
    expect(args.qty).toBe(300);
    expect(args.venue).toBe("alpaca-paper");
  });
  it("annotates Flatten with master's armed state but keeps it clickable when disarmed", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: {
        masterArmed: false, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
        venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
      } });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.5, ask: 3.51, last: 3.5, ts: "" } });
      stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({ qty: 300 })] });
    });
    const btn = screen.getByTestId("flatten-alpaca-paper-US.AAPL") as HTMLButtonElement;
    expect(btn.getAttribute("data-armed")).toBe("false");
    expect(btn.disabled).toBe(false);
    fireEvent.click(btn);
    expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true);
  });

  // --- new: sortable positions table (Task 16 sortColumns), default unrealizedPnl desc ---
  it("defaults to sorting positions by unrealized P&L descending", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
      stores.exec.apply({
        kind: "snapshot", topic: "exec.positions" as never,
        payload: [
          pos({ symbol: "US.AAPL", unrealizedPnl: 5 }),
          pos({ symbol: "US.MSFT", unrealizedPnl: 50 }),
          pos({ symbol: "US.TSLA", unrealizedPnl: -10 }),
        ],
      });
    });
    // Task 7: the panel now renders OrdersTable's (empty) table above the tab
    // body, adding its own header row — drop both header rows, not just one.
    const rows = screen.getAllByRole("row").slice(2);
    expect(rows[0].textContent).toContain("MSFT");
    expect(rows[1].textContent).toContain("AAPL");
    expect(rows[2].textContent).toContain("TSLA");
  });
  it("clicking the Qty column header sorts by qty and persists the sort via onConfigChange", () => {
    const { props, stores, configChanges } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
      stores.exec.apply({
        kind: "snapshot", topic: "exec.positions" as never,
        payload: [
          pos({ symbol: "US.AAPL", qty: 10, unrealizedPnl: 1 }),
          pos({ symbol: "US.MSFT", qty: 300, unrealizedPnl: 2 }),
        ],
      });
    });
    fireEvent.click(screen.getByText("Qty"));
    const rows = screen.getAllByRole("row").slice(2); // drop OrdersTable's + PositionsTable's header rows
    expect(rows[0].textContent).toContain("MSFT"); // desc by qty: 300 before 10
    // Task 7 renamed PositionsTable's persisted sort key to posSort (avoids
    // colliding with OrdersTable's ordersSort now that both live in one panel).
    expect(configChanges.at(-1)).toEqual({ posSort: { col: "qty", dir: "desc" } });
  });

  // --- Task 7: OrdersTable always visible, tab body (Positions/Trade History), resize handle ---
  describe("restructured layout (Task 7)", () => {
    const order = (o: Partial<Order> = {}): Order => ({
      venue: "alpaca-paper", id: "o1", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO",
      qty: 100, limitPrice: 3.5, stopPrice: 0, status: "SUBMITTED", executedQty: 0, leavesQty: 100,
      avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1000, updatedMs: 1000, ...o,
    });
    const trade = (o: Partial<ClosedTradeRow> = {}): ClosedTradeRow => ({
      venue: "alpaca-paper", symbol: "US.MSFT", isLong: true, qty: 10,
      entryPrice: 100, exitPrice: 105, realized: 50, openMs: 1000, closeMs: 2000, seq: 1, ...o,
    });

    it("keeps Open Orders visible regardless of the active tab", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        // Resolve the panel onto a real venue ("alpaca-paper", the sole venue
        // here) so OrdersTable/TradeHistoryTable's venue-scoping filter keeps
        // (rather than drops) the seeded rows below.
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order()] });
        stores.trades.apply({ kind: "snapshot", topic: "exec.trades" as never, payload: [trade()] });
      });
      expect(screen.getByText("Open Orders (1)")).toBeTruthy();
      fireEvent.click(screen.getByText("Trade History"));
      expect(screen.getByText("Open Orders (1)")).toBeTruthy();
      expect(screen.getByText(/MSFT/)).toBeTruthy(); // now on the Trade History tab
    });

    it("defaults to the Positions tab and swaps body content on tab click", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({ symbol: "US.AAPL" })] });
        stores.trades.apply({ kind: "snapshot", topic: "exec.trades" as never, payload: [trade({ symbol: "US.MSFT" })] });
      });
      expect(screen.getByTestId("flatten-alpaca-paper-US.AAPL")).toBeTruthy(); // PositionsTable content
      expect(screen.queryByText("MSFT")).toBeNull();

      fireEvent.click(screen.getByText("Trade History"));
      expect(screen.queryByTestId("flatten-alpaca-paper-US.AAPL")).toBeNull();
      expect(screen.getByText("MSFT")).toBeTruthy(); // TradeHistoryTable content

      fireEvent.click(screen.getByText(/^Positions/));
      expect(screen.getByTestId("flatten-alpaca-paper-US.AAPL")).toBeTruthy();
    });

    it("persists the active tab via onConfigChange and restores it on mount", () => {
      const { props, configChanges } = mkProps();
      wrap(props);
      fireEvent.click(screen.getByText("Trade History"));
      expect(configChanges.at(-1)).toEqual({ tab: "history" });

      const { props: props2 } = mkProps();
      props2.config.settings.tab = "history";
      wrap(props2);
      expect(screen.getAllByText("Trade History")[0]).toBeTruthy();
      // Positions body content (the "N open positions" strip) should be absent
      // since the history tab opened by default.
      expect(screen.queryByText(/open position/)).toBeNull();
    });

    it("clamps the resize handle drag at the lower bound (80px)", () => {
      const { props, configChanges } = mkProps();
      wrap(props);
      const handle = screen.getByTestId("orders-resize-handle");
      act(() => {
        fireEvent.mouseDown(handle, { clientY: 300 });
        fireEvent.mouseMove(window, { clientY: -5000 }); // drag far up — should clamp to 80
        fireEvent.mouseUp(window);
      });
      expect(configChanges.at(-1)).toEqual({ ordersHeight: 80 });
    });

    it("clamps the resize handle drag at the upper bound (height - 120)", () => {
      const { props, configChanges } = mkProps(); // props.height === 400 -> upper bound 280
      wrap(props);
      const handle = screen.getByTestId("orders-resize-handle");
      act(() => {
        fireEvent.mouseDown(handle, { clientY: 300 });
        fireEvent.mouseMove(window, { clientY: 5000 }); // drag far down — should clamp to height-120
        fireEvent.mouseUp(window);
      });
      expect(configChanges.at(-1)).toEqual({ ordersHeight: 280 });
    });

    it("does not leak mousemove/mouseup listeners after a drag completes", () => {
      const { props, configChanges } = mkProps();
      wrap(props);
      const handle = screen.getByTestId("orders-resize-handle");
      act(() => {
        fireEvent.mouseDown(handle, { clientY: 300 });
        fireEvent.mouseMove(window, { clientY: 350 });
        fireEvent.mouseUp(window);
      });
      const afterFirstDrag = configChanges.length;
      // A move/up dispatched with no active drag must not call onConfigChange again.
      act(() => {
        fireEvent.mouseMove(window, { clientY: 900 });
        fireEvent.mouseUp(window);
      });
      expect(configChanges.length).toBe(afterFirstDrag);
    });

    it("scopes OrdersTable rows to the selected venue", () => {
      const { props, stores, linkGroups } = mkProps("green");
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper", "alpaca-live") });
        stores.exec.apply({
          kind: "snapshot", topic: "exec.orders" as never,
          payload: [order({ id: "o1", venue: "alpaca-paper", symbol: "US.AAPL" }), order({ id: "o2", venue: "alpaca-live", symbol: "US.MSFT" })],
        });
        linkGroups.focusVenue("green", "alpaca-paper");
      });
      wrap(props);
      expect(screen.getByText("Open Orders (1)")).toBeTruthy();
      expect(screen.getByText("AAPL")).toBeTruthy();
      expect(screen.queryByText("MSFT")).toBeNull();
    });

    it("uses distinct persisted sort keys for Positions (posSort) and Orders (ordersSort)", () => {
      const { props, stores, configChanges } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({ symbol: "US.AAPL" })] });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order()] });
      });
      // Both tables render a "Symbol" header; OrdersTable's is first in DOM
      // order (it's rendered above the tab body). toggleSort always lands on
      // "desc" the first time a different column is clicked (sortColumns.ts).
      fireEvent.click(screen.getAllByText("Symbol")[0]);
      expect(configChanges.at(-1)).toEqual({ ordersSort: { col: "symbol", dir: "desc" } });

      fireEvent.click(screen.getByText("Qty")); // PositionsTable's Qty header — OrdersTable's is "Qty@Px", not ambiguous
      expect(configChanges.at(-1)).toEqual({ posSort: { col: "qty", dir: "desc" } });
    });
  });

  // --- ported from OpenOrdersPanel.test.tsx (Task 10; OpenOrdersPanel retired,
  // its table folded into AccountPanel's always-visible OrdersTable in Task 7).
  // Every test seeds exec.status with a venue before seeding orders — OrdersTable
  // filters stores.exec.orders() by the panel's resolved venue, and with no
  // exec.status seeded the panel resolves to no venue, so every order (including
  // optimistic ones, which carry the venue from their submit args) would be
  // filtered out.
  describe("orders section (ported from OpenOrdersPanel)", () => {
    const order = (id: string, o: Partial<Order> = {}): Order => ({
      venue: "alpaca-paper", id, symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO",
      qty: 10, limitPrice: 3.5, stopPrice: 0, status: "ACCEPTED", executedQty: 0, leavesQty: 10,
      avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1, ...o,
    });
    const statusReconciling = (): ExecStatus => ({
      masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
      venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: true, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
    });

    it("shows an optimistic order as a Pending chip (bronze .chip-pending)", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.addOptimistic({ args: { venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", type: "LIMIT", tif: "DAY", session: "AUTO", qty: 10, limitPrice: 3.5, stopPrice: 0 }, id: "ET9", createdMs: 100 });
      });
      const chip = screen.getByText("Pending");
      expect(chip.className).toContain("chip-pending");
      expect(chip.getAttribute("data-chip")).toBe("pending");
    });

    it("shows a working order as a Submitted/Accepted chip (green outline .chip-working)", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "ACCEPTED" }), order("ET2", { status: "SUBMITTED", createdMs: 2 })] });
      });
      const accepted = screen.getByText("Accepted");
      expect(accepted.className).toContain("chip-working");
      expect(accepted.getAttribute("data-chip")).toBe("working");
      const submitted = screen.getByText("Submitted");
      expect(submitted.className).toContain("chip-working");
    });

    it("shows a reject reason verbatim, a rejected chip, and no cancel button on a terminal row", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "REJECTED", rejectReason: "R78: market order in extended hours" })] });
      });
      expect(screen.getByText(/R78: market order in extended hours/)).toBeTruthy();
      const chip = screen.getByText("Rejected");
      expect(chip.className).toContain("chip-rejected");
      expect(chip.getAttribute("data-chip")).toBe("rejected");
      expect(screen.queryByTestId("cancel-ET1")).toBeNull();
    });

    it("shows a terminal, non-rejected status as plain muted text (no chip)", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "CANCELED" })] });
      });
      const canceled = screen.getByText("Canceled");
      expect(canceled.className).not.toContain("chip");
    });

    it("drops FILLED orders from the Open Orders table", () => {
      const { props, stores } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1", { status: "FILLED" }), order("ET2", { status: "ACCEPTED" })] });
      });
      expect(screen.getByText("Open Orders (1)")).toBeTruthy();
      expect(screen.queryByText("Filled")).toBeNull();
    });

    it("cancel on a working row sends CancelOrder; Cancel All cancels every working order", () => {
      const { props, stores, sent } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [order("ET1"), order("ET2", { status: "SUBMITTED" }), order("ET3", { status: "FILLED" })] });
      });
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
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [
          order("ET1", { symbol: "US.AAPL", createdMs: 1 }),
          order("ET2", { symbol: "US.MSFT", createdMs: 3 }),
          order("ET3", { symbol: "US.TSLA", createdMs: 2 }),
        ] });
      });
      const symbols = [...screen.getByTestId("orders-table").querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
      expect(symbols).toEqual(["MSFT", "TSLA", "AAPL"]); // createdMs desc: ET2(3), ET3(2), ET1(1)
      // PositionsTable's headers never carry a sort-active class (only OrdersTable
      // applies one), so this stays unscoped and unambiguous.
      expect(document.querySelectorAll("thead .sort-active").length).toBe(0);
    });

    it("clicking the Symbol header sorts by symbol and persists it via onConfigChange", () => {
      const { props, stores, configChanges } = mkProps();
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [
          order("ET1", { symbol: "US.AAPL", createdMs: 1 }),
          order("ET2", { symbol: "US.MSFT", createdMs: 3 }),
          order("ET3", { symbol: "US.TSLA", createdMs: 2 }),
        ] });
      });
      // OrdersTable renders first in the DOM (above the tab body), so index 0 is
      // its "Symbol" header, not PositionsTable's.
      fireEvent.click(screen.getAllByRole("columnheader", { name: /Symbol/ })[0]);
      expect(configChanges.at(-1)).toEqual({ ordersSort: { col: "symbol", dir: "desc" } });
      const symbols = [...screen.getByTestId("orders-table").querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
      expect(symbols).toEqual(["TSLA", "MSFT", "AAPL"]);
    });

    it("restores a persisted sort from settings", () => {
      const { props, stores } = mkProps();
      props.config.settings.ordersSort = { col: "symbol", dir: "asc" };
      wrap(props);
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) });
        stores.exec.apply({ kind: "snapshot", topic: "exec.orders" as never, payload: [
          order("ET1", { symbol: "US.AAPL", createdMs: 1 }),
          order("ET2", { symbol: "US.MSFT", createdMs: 3 }),
          order("ET3", { symbol: "US.TSLA", createdMs: 2 }),
        ] });
      });
      const symbols = [...screen.getByTestId("orders-table").querySelectorAll("tbody tr td:first-child")].map((td) => td.textContent);
      expect(symbols).toEqual(["AAPL", "MSFT", "TSLA"]);
      expect(screen.getAllByRole("columnheader", { name: /Symbol/ })[0].className).toContain("sort-active");
    });
  });

  describe("Export trades (Task 7 wiring)", () => {
    beforeEach(() => {
      (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = vi.fn(() => "blob:mock");
      (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn();
    });

    it("opens the Export popover from the Trade History tab row and downloads for the panel's selected venue", async () => {
      const { props, stores, linkGroups } = mkProps("green");
      const calls: Array<{ name: string; args: unknown }> = [];
      props.commands.sendQuery = vi.fn(async (name: string, args: unknown) => {
        calls.push({ name, args });
        return { csv: "datetime,symbol,action,price,shares,fees,externalId\n2026-07-10T09:31:05,NVDA,BUY,120.5,100,0,etape:alpaca-paper:1\n", count: 1 };
      });
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      act(() => {
        stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(false, "alpaca-paper") });
        linkGroups.focusVenue("green", "alpaca-paper");
      });
      wrap(props);
      fireEvent.click(screen.getByText("Trade History")); // Export lives in the Trade History tab row

      expect(screen.queryByTestId("export-download")).toBeNull(); // popover closed by default
      fireEvent.click(screen.getByTestId("acct-export"));
      expect(screen.getByTestId("export-download")).toBeTruthy(); // popover opened

      fireEvent.click(screen.getByTestId("export-download"));
      await waitFor(() => expect(clickSpy).toHaveBeenCalledTimes(1));
      expect(calls).toEqual([{ name: "ExportFills", args: { venue: "alpaca-paper", preset: "all", from: "", to: "" } }]);
      clickSpy.mockRestore();
    });
  });
});
