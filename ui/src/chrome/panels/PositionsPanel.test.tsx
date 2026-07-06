// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { PositionsPanel } from "./PositionsPanel";
import { makeStores } from "../../data/registry";
import type { AckMsg, PositionRow, SubmitOrderArgs } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps() {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX" }; }) };
  const props = { config: { id: "t-positions", panelId: "positions", group: null, settings: {} }, stores, scheduler: {} as never, width: 400, height: 200, linkGroups: {} as never, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent };
}
const pos = (o: Partial<PositionRow>): PositionRow => ({ venue: "alpaca-paper", symbol: "US.AAPL", qty: 300, avgPrice: 3.4, unrealizedPnl: 30, ...o });
const wrap = (p: PanelProps) => render(<ThemeProvider><ToastProvider><PositionsPanel {...p} /></ToastProvider></ThemeProvider>);

describe("PositionsPanel", () => {
  it("renders per-venue and net rows with colored unrealized P&L", () => {
    const { props, stores } = mkProps();
    wrap(props);
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [pos({}), pos({ venue: null, unrealizedPnl: 30 })] }));
    expect(screen.getAllByText("AAPL").length).toBe(2);
    expect(screen.getByTestId("pos-net").textContent).toMatch(/NET/);
  });
  it("flatten on a long row submits a SELL for the full qty (priced from the quote)", () => {
    const { props, stores, sent } = mkProps();
    wrap(props);
    act(() => {
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
});
