// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider, useTheme } from "../ThemeProvider";
import { TradeHistoryTable } from "./TradeHistoryTable";
import { makeStores } from "../../data/registry";
import { LIGHT } from "../../render/palette";
import type { ClosedTradeRow } from "../../wire/contract";
import type { PanelProps } from "./registry";

// jsdom normalizes inline hex colors to rgb() on the CSSStyleDeclaration.
const hexToRgb = (hex: string): string => {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};

const row = (o: Partial<ClosedTradeRow>): ClosedTradeRow => ({
  venue: "alpaca-paper", symbol: "US.AAPL", isLong: true, qty: 10,
  entryPrice: 100, exitPrice: 105, realized: 50, openMs: 1000, closeMs: 2000, seq: 1,
  ...o,
});
const snap = (payload: ClosedTradeRow[]) => ({ kind: "snapshot" as const, topic: "exec.trades" as never, payload });

function mkProps(settings: Record<string, unknown> = {}) {
  const stores = makeStores();
  const configChanges: Array<Record<string, unknown>> = [];
  const props = {
    config: { id: "t-tradehistory", panelId: "account", group: null, settings },
    stores, scheduler: {} as never, width: 800, height: 400,
    linkGroups: {} as never,
    commands: { sendCommand: async () => ({ kind: "ack" as const, corrId: "c", status: "accepted" as const }), sendQuery: async () => [] },
    onConfigChange: (s: Record<string, unknown>) => configChanges.push(s),
  } as PanelProps;
  return { props, stores, configChanges };
}

// TradeHistoryTable takes palette as a prop (AccountPanel resolves it once via
// useTheme() and passes it down to every table, same as PositionsTable) — this
// harness mirrors that call site inside a ThemeProvider.
function Harness(props: { stores: PanelProps["stores"]; config: PanelProps["config"]; onConfigChange: PanelProps["onConfigChange"]; venue: string }) {
  const { palette } = useTheme();
  return <TradeHistoryTable {...props} palette={palette} />;
}
function renderHarness(props: ReturnType<typeof mkProps>["props"], venue: string) {
  return render(
    <ThemeProvider>
      <Harness stores={props.stores} config={props.config} onConfigChange={props.onConfigChange} venue={venue} />
    </ThemeProvider>,
  );
}

describe("TradeHistoryTable", () => {
  it("renders rows sorted by closeMs descending by default", () => {
    const { props, stores } = mkProps();
    act(() => {
      stores.trades.apply(snap([
        row({ seq: 1, symbol: "US.AAPL", closeMs: 1000 }),
        row({ seq: 2, symbol: "US.MSFT", closeMs: 3000 }),
        row({ seq: 3, symbol: "US.TSLA", closeMs: 2000 }),
      ]));
    });
    renderHarness(props, "alpaca-paper");
    const rows = screen.getAllByRole("row").slice(1); // drop header row
    expect(rows[0].textContent).toContain("MSFT");
    expect(rows[1].textContent).toContain("TSLA");
    expect(rows[2].textContent).toContain("AAPL");
  });

  it("colors a winning trade's Realized cell with palette.up and a losing trade's with palette.down", () => {
    const { props, stores } = mkProps();
    act(() => {
      stores.trades.apply(snap([
        row({ seq: 1, symbol: "US.AAPL", realized: 50, closeMs: 1000 }),
        row({ seq: 2, symbol: "US.MSFT", realized: -30, closeMs: 2000 }),
      ]));
    });
    renderHarness(props, "alpaca-paper");
    // Row-level Realized mirrors PositionsTable's unrealizedPnl cell (AccountPanel.tsx:182):
    // plain formatPrice, no $ sign, ASCII hyphen-minus from toFixed (not money()'s unicode minus).
    const winCell = screen.getByText("50.00");
    const lossCell = screen.getByText("-30.00");
    expect(winCell.style.color).toBe(hexToRgb(LIGHT.up));
    expect(lossCell.style.color).toBe(hexToRgb(LIGHT.down));
  });

  it("filters rows to the selected venue", () => {
    const { props, stores } = mkProps();
    act(() => {
      stores.trades.apply(snap([
        row({ seq: 1, venue: "alpaca-paper", symbol: "US.AAPL", closeMs: 1000 }),
        row({ seq: 2, venue: "tradezero-live", symbol: "US.MSFT", closeMs: 2000 }),
      ]));
    });
    renderHarness(props, "alpaca-paper");
    expect(screen.getByText("AAPL")).toBeTruthy();
    expect(screen.queryByText("MSFT")).toBeNull();
  });

  it("clicking a sortable header sorts and persists under settings.tradesSort (not the generic sort key)", () => {
    const { props, stores, configChanges } = mkProps();
    act(() => {
      stores.trades.apply(snap([
        row({ seq: 1, symbol: "US.AAPL", qty: 10, closeMs: 1000 }),
        row({ seq: 2, symbol: "US.MSFT", qty: 300, closeMs: 2000 }),
      ]));
    });
    renderHarness(props, "alpaca-paper");
    fireEvent.click(screen.getByText("Qty"));
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("MSFT"); // desc by qty: 300 before 10
    expect(configChanges.at(-1)).toEqual({ tradesSort: { col: "qty", dir: "desc" } });
  });

  it("restores a persisted tradesSort setting across a re-mount", () => {
    const { props, stores } = mkProps({ tradesSort: { col: "qty", dir: "asc" } });
    act(() => {
      stores.trades.apply(snap([
        row({ seq: 1, symbol: "US.AAPL", qty: 300, closeMs: 1000 }),
        row({ seq: 2, symbol: "US.MSFT", qty: 10, closeMs: 2000 }),
      ]));
    });
    renderHarness(props, "alpaca-paper");
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("MSFT"); // asc by qty: 10 before 300
    expect(rows[1].textContent).toContain("AAPL");
  });

  it("renders Opened/Closed/Duration via formatClock/formatDuration", () => {
    const { props, stores } = mkProps();
    const openMs = Date.parse("2026-07-06T13:30:00Z");
    const closeMs = openMs + 192000; // +3m12s
    act(() => {
      stores.trades.apply(snap([row({ seq: 1, symbol: "US.AAPL", openMs, closeMs })]));
    });
    renderHarness(props, "alpaca-paper");
    expect(screen.getByText("09:30:00")).toBeTruthy();
    expect(screen.getByText("09:33:12")).toBeTruthy();
    expect(screen.getByText("03m 12s")).toBeTruthy();
  });
});
