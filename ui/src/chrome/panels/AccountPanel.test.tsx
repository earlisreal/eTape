// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { AccountPanel } from "./AccountPanel";
import { makeStores } from "../../data/registry";
import { LIGHT } from "../../render/palette";
import { LinkGroups } from "../linkGroups";
import { FakeBus, FakeBusHub } from "../../../test/fakes";

// jsdom normalizes inline hex colors to rgb() on the CSSStyleDeclaration.
const hexToRgb = (hex: string): string => {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};
import type { AckMsg, AccountRow, ExecStatus, PositionRow, SubmitOrderArgs } from "../../wire/contract";
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
    venue, broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false,
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

  // --- Task 4: HoverButton default overlay on the arm chip, dynamic armed color preserved base-style ---
  it("hovering an arm chip applies the default hover overlay without losing its armed styling on mouseleave", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) }));
    const btn = screen.getByTestId("venue-arm-alpaca-paper") as HTMLButtonElement;
    expect(btn.style.background).toBe("transparent");

    fireEvent.mouseEnter(btn);
    expect(btn.style.background).toBe("var(--surface)");
    expect(btn.style.color).toBe("var(--text)");

    fireEvent.mouseLeave(btn);
    expect(btn.style.background).toBe("transparent");
    expect(btn.style.color).toBe(hexToRgb(LIGHT.accent)); // armed color restored, not stuck on the overlay
  });

  // --- color discipline (Task 9 review fix): arm chip is bronze/muted, never green/amber ---
  it("styles per-venue arm chips bronze/muted rather than green/amber", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status(true) }));
    const btn = screen.getByTestId("venue-arm-alpaca-paper") as HTMLButtonElement;
    expect(btn.getAttribute("data-armed")).toBe("true");
    expect(btn.style.color).toBe(hexToRgb(LIGHT.accent));
    expect(btn.style.color).not.toBe(hexToRgb(LIGHT.up));
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
  it("annotates Flatten with the venue's armed state but keeps it clickable when disarmed", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: {
        masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
        venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: false, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
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
    const rows = screen.getAllByRole("row").slice(1); // drop header row
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
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("MSFT"); // desc by qty: 300 before 10
    expect(configChanges.at(-1)).toEqual({ sort: { col: "qty", dir: "desc" } });
  });
});
