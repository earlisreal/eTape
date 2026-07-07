// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { OrderTicketPanel } from "./OrderTicketPanel";
import { makeStores } from "../../data/registry";
import { getPalette } from "../../render/palette";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus, SubmitOrderArgs } from "../../wire/contract";
import type { PanelProps } from "./registry";

function mkProps() {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => { sent.push({ name, args }); return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined }; }), sendQuery: vi.fn(async () => []) };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const props = { config: { id: "t-ticket", panelId: "order-ticket", group: "green", settings: {} }, stores, scheduler: {} as never, width: 320, height: 400, linkGroups, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent, linkGroups };
}
const status = (): ExecStatus => ({ masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, venueArmed: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });
const wrap = (p: PanelProps) => render(
  <ThemeProvider><ToastProvider><OrderConfigProvider commands={p.commands}><OrderTicketPanel {...p} /></OrderConfigProvider></ToastProvider></ThemeProvider>,
);

describe("OrderTicketPanel", () => {
  it("follows the link-group symbol and shows bid/ask", async () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    expect(await screen.findByText("AAPL")).toBeTruthy();
    expect(screen.getByTestId("bid").textContent).toContain("3.40");
    expect(screen.getByTestId("ask").textContent).toContain("3.50");
  });
  it("manual Shares submit sends a venue-tagged SubmitOrder", async () => {
    const { props, stores, linkGroups, sent } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } }); linkGroups.focus("green", "US.AAPL"); });
    wrap(props);
    fireEvent.change(screen.getByTestId("amount"), { target: { value: "100" } });
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.5" } });
    fireEvent.click(screen.getByTestId("submit"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args).toMatchObject({ venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", qty: 100, limitPrice: 3.5 });
  });
  it("kill switch fires KillSwitch even without arming logic", () => {
    const { props, sent } = mkProps();
    wrap(props);
    fireEvent.click(screen.getByTestId("kill"));
    expect(sent.some((s) => s.name === "KillSwitch")).toBe(true);
  });
  it("shows a DISARMED badge when the active venue is disarmed", async () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: { ...status(), venues: [{ ...status().venues[0], venueArmed: false }] } });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    expect((await screen.findByTestId("ticket-armed-state")).textContent).toMatch(/DISARMED/i);
  });
  it("shows an ARMED badge when master and the active venue are armed, and exposes an order-type testid", async () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    expect((await screen.findByTestId("ticket-armed-state")).textContent).toBe("ARMED");
    expect(screen.getByTestId("order-type")).toBeTruthy();
  });
  // Color-discipline regression (final-branch review, Finding 2): armed/disarmed
  // is UI state, never market-direction green/red — matches AccountPanel's
  // arm-chip formula (palette.accent when active, palette.textMuted when not).
  it("colors the armed indicator bronze/muted, never green/red", async () => {
    const palette = getPalette("light"); // ThemeProvider defaults to light
    const { props: armedProps, stores: armedStores, linkGroups: armedLinks } = mkProps();
    act(() => {
      armedStores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      armedLinks.focus("green", "US.AAPL");
    });
    const armedRender = wrap(armedProps);
    const armedEl = await screen.findByTestId("ticket-armed-state");
    expect(armedEl.style.color).toBe(hexToRgb(palette.accent));
    expect(armedEl.style.color).not.toBe(hexToRgb(palette.up));
    armedRender.unmount();

    const { props: disarmedProps, stores: disarmedStores, linkGroups: disarmedLinks } = mkProps();
    act(() => {
      disarmedStores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: { ...status(), venues: [{ ...status().venues[0], venueArmed: false }] } });
      disarmedLinks.focus("green", "US.AAPL");
    });
    wrap(disarmedProps);
    const disarmedEl = await screen.findByTestId("ticket-armed-state");
    expect(disarmedEl.style.color).toBe(hexToRgb(palette.textMuted));
    expect(disarmedEl.style.color).not.toBe(hexToRgb(palette.warn));
  });
});

function hexToRgb(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
}
