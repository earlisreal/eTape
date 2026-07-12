// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "../ThemeProvider";
import { LIGHT } from "../../render/palette";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import { OrderTicketPanel } from "./OrderTicketPanel";
import { makeStores } from "../../data/registry";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import type { AckMsg, ExecStatus, SubmitOrderArgs } from "../../wire/contract";
import type { PanelProps } from "./registry";
import type { OrderConfig } from "../exec/actionTemplate";

// orderConfig, when passed, is served back as the OrderConfigProvider's
// GetConfig read — lets deck-wiring tests seed a deck-enabled template
// without touching the default (deck-less) behavior every other test here relies on.
function mkProps(orderConfig?: OrderConfig) {
  const stores = makeStores();
  const sent: Array<{ name: string; args: unknown }> = [];
  const commands = { sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
    sent.push({ name, args });
    if (name === "GetConfig" && orderConfig) return { kind: "ack", corrId: "c", status: "accepted", value: orderConfig };
    return { kind: "ack", corrId: "c", status: "accepted", orderId: "ETX", value: undefined };
  }), sendQuery: vi.fn(async () => []) };
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const props = { config: { id: "t-ticket", panelId: "order-ticket", group: "green", settings: {} }, stores, scheduler: {} as never, width: 320, height: 400, linkGroups, commands, onConfigChange: () => {} } as PanelProps;
  return { props, stores, sent, linkGroups };
}
const status = (): ExecStatus => ({ masterArmed: true, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }] });
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
    await waitFor(() => expect((screen.getByTestId("symbol") as HTMLInputElement).value).toBe("AAPL"));
    expect(screen.getByTestId("bid").textContent).toContain("3.40");
    expect(screen.getByTestId("ask").textContent).toContain("3.50");
  });
  it("manual Shares submit sends a venue-tagged SubmitOrder", async () => {
    const { props, stores, linkGroups, sent } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } }); linkGroups.focus("green", "US.AAPL"); });
    wrap(props);
    fireEvent.change(screen.getByTestId("amount"), { target: { value: "100" } });
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.5" } });
    fireEvent.click(screen.getByTestId("side-BUY"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args).toMatchObject({ venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", session: "AUTO", qty: 100, limitPrice: 3.5 });
  });
  it("picking a Session carries it through to the submitted SubmitOrder", async () => {
    const { props, stores, linkGroups, sent } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } }); linkGroups.focus("green", "US.AAPL"); });
    wrap(props);
    fireEvent.change(screen.getByTestId("amount"), { target: { value: "100" } });
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.5" } });
    fireEvent.change(screen.getByTestId("session"), { target: { value: "EXTENDED" } });
    fireEvent.click(screen.getByTestId("side-BUY"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args).toMatchObject({ side: "BUY", session: "EXTENDED" });
  });
  it("clicking SELL submits that side directly, without a separate select step", async () => {
    const { props, stores, linkGroups, sent } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } }); linkGroups.focus("green", "US.AAPL"); });
    wrap(props);
    fireEvent.change(screen.getByTestId("amount"), { target: { value: "50" } });
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.4" } });
    fireEvent.click(screen.getByTestId("side-SELL"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args).toMatchObject({ venue: "alpaca-paper", symbol: "US.AAPL", side: "SELL", qty: 50, limitPrice: 3.4 });
  });
  it("Pos mode sizes off the amount as a percent of the held position", async () => {
    const { props, stores, linkGroups, sent } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      stores.exec.apply({ kind: "snapshot", topic: "exec.positions" as never, payload: [{ venue: "alpaca-paper", symbol: "US.AAPL", qty: 200, avgPrice: 3.4, unrealizedPnl: 0 }] });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    fireEvent.change(screen.getByTestId("mode"), { target: { value: "PositionFraction" } });
    fireEvent.change(screen.getByTestId("amount"), { target: { value: "50" } });
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.4" } });
    fireEvent.click(screen.getByTestId("side-SELL"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args.qty).toBe(100);
  });
  it("clicking the header bid/ask fills the price input", () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    fireEvent.click(screen.getByTestId("bid"));
    // QUOTE_DECIMALS is 3 (pinned decimal count for live quote/limit-price
    // display), so the filled price carries three decimals, not two.
    expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("3.400");
    fireEvent.click(screen.getByTestId("ask"));
    expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("3.500");
  });
  it("renders the stop input always, disabled unless type is STOP/STOP_LIMIT", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    expect((screen.getByTestId("stop") as HTMLInputElement).disabled).toBe(true); // default LIMIT
    fireEvent.change(screen.getByTestId("order-type"), { target: { value: "STOP" } });
    expect((screen.getByTestId("stop") as HTMLInputElement).disabled).toBe(false);
  });
  it("price stepper nudges by 10 cents and clamps at zero", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    fireEvent.click(screen.getByTestId("price-up"));
    expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("0.100");
    fireEvent.change(screen.getByTestId("price"), { target: { value: "3.5" } });
    fireEvent.click(screen.getByTestId("price-up"));
    expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("3.600");
    fireEvent.click(screen.getByTestId("price-down"));
    fireEvent.click(screen.getByTestId("price-down"));
    expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("3.400");
    fireEvent.change(screen.getByTestId("price"), { target: { value: "0.05" } });
    fireEvent.click(screen.getByTestId("price-down"));
    expect((screen.getByTestId("price") as HTMLInputElement).value).toBe("0.000");
  });
  it("stop stepper is disabled unless type is STOP/STOP_LIMIT", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    expect((screen.getByTestId("stop-up") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByTestId("order-type"), { target: { value: "STOP" } });
    expect((screen.getByTestId("stop-up") as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByTestId("stop-up"));
    expect((screen.getByTestId("stop") as HTMLInputElement).value).toBe("0.100");
  });
  it("shows a PRACTICE badge in the header only during replay (safety signal)", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    // Before any sys.session snapshot, SessionStore is "pending" — a muted
    // placeholder shows, never the confident PRACTICE badge.
    expect(screen.queryByTestId("practice-badge")).toBeNull();
    expect(screen.getByTestId("session-pending-badge")).toBeTruthy();
    act(() => { stores.session.apply({ kind: "snapshot", topic: "sys.session" as never, payload: { mode: "live" } }); });
    expect(screen.queryByTestId("practice-badge")).toBeNull();
    expect(screen.queryByTestId("session-pending-badge")).toBeNull();
    act(() => { stores.session.apply({ kind: "snapshot", topic: "sys.session" as never, payload: { mode: "replay", day: "2026-07-06", speed: 4 } }); });
    expect(screen.getByTestId("practice-badge").textContent).toBe("PRACTICE");
    expect(screen.queryByTestId("session-pending-badge")).toBeNull();
  });
  it("shows a PRACTICE badge during demo too, with a distinct color from replay's (safety signal)", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    act(() => { stores.session.apply({ kind: "snapshot", topic: "sys.session" as never, payload: { mode: "replay", day: "2026-07-06", speed: 4 } }); });
    const replayBackground = (screen.getByTestId("practice-badge") as HTMLElement).style.background;
    act(() => { stores.session.apply({ kind: "snapshot", topic: "sys.session" as never, payload: { mode: "demo" } }); });
    const demoBadge = screen.getByTestId("practice-badge") as HTMLElement;
    expect(demoBadge.textContent).toBe("PRACTICE");
    expect(screen.queryByTestId("session-pending-badge")).toBeNull();
    // Same badge/testid, but demo must render with palette.demo, not
    // palette.warn — mirrors the DemoBanner/ReplayBanner color split so a
    // demo session's ticket is never silently painted the replay color.
    // jsdom normalizes inline hex to rgb() on read, so normalize the
    // expected palette token the same way rather than comparing hex to rgb.
    const probe = document.createElement("span");
    probe.style.background = LIGHT.demo;
    expect(demoBadge.style.background).not.toBe(replayBackground);
    expect(demoBadge.style.background).toBe(probe.style.background);
  });
  it("changing the venue dropdown writes the group's focused venue", () => {
    const { props, stores, linkGroups } = mkProps();
    const twoVenues: ExecStatus = { ...status(), venues: [status().venues[0], { ...status().venues[0], venue: "tradezero" }] };
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: twoVenues }); });
    wrap(props);
    fireEvent.change(screen.getByTestId("venue"), { target: { value: "tradezero" } });
    expect(linkGroups.venueFor("green")).toBe("tradezero");
  });
  it("Enter commits an edited header symbol into the link group", async () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "MSFT" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(linkGroups.symbolFor("green")).toBe("US.MSFT"));
    expect(input.value).toBe("MSFT");
  });
  it("typing a lowercase bare ticker normalizes to a US.-qualified symbol on commit", async () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "tsla" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(linkGroups.symbolFor("green")).toBe("US.TSLA"));
  });
  it("committing an unmodified header symbol is a no-op — does not flip a non-US market prefix", async () => {
    // Regression: normalizeSymbol re-derives the market prefix from an
    // allow-list that defaults anything unprefixed to US. — committing the
    // *bare* text unchanged (e.g. tabbing in and hitting Enter with zero
    // intended edit) must not run that re-derivation, or an HK ticket like
    // this one would silently corrupt into US.00700.
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      linkGroups.focus("green", "HK.00700");
    });
    wrap(props);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    expect(input.value).toBe("00700");
    fireEvent.focus(input);
    fireEvent.keyDown(input, { key: "Enter" }); // no change event — text is untouched
    // Give any (incorrect) async commit a tick to land before asserting it didn't.
    await new Promise((r) => setTimeout(r, 0));
    expect(linkGroups.symbolFor("green")).toBe("HK.00700");
    expect(input.value).toBe("00700");
  });
  it("Escape after editing reverts the header without committing anything", () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "MSFT" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(input.value).toBe("AAPL");
    expect(linkGroups.symbolFor("green")).toBe("US.AAPL");
  });
  it("blurring after editing (without Enter) reverts the header without committing anything", () => {
    const { props, stores, linkGroups } = mkProps();
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "MSFT" } });
    fireEvent.blur(input);
    expect(input.value).toBe("AAPL");
    expect(linkGroups.symbolFor("green")).toBe("US.AAPL");
  });
  it("a pinned panel (group: null) commits an edited symbol via onConfigChange, not the link group", async () => {
    const { props, stores } = mkProps();
    const onConfigChange = vi.fn();
    const pinnedProps: PanelProps = { ...props, config: { ...props.config, group: null }, onConfigChange };
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(pinnedProps);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "tsla" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(onConfigChange).toHaveBeenCalledWith({ symbol: "US.TSLA" }));
    expect(input.value).toBe("TSLA");
  });
  it("a pinned panel does not call onConfigChange when committing an unmodified symbol", async () => {
    // Pinned-panel equivalent of the grouped no-op regression above: this
    // commit path is completely unvalidated (no toast, no revert), so an
    // unmodified commit reaching normalizeSymbol here would silently and
    // undetectably flip HK.00700 -> US.00700 in local config.
    const { props, stores } = mkProps();
    const onConfigChange = vi.fn();
    const pinnedProps: PanelProps = {
      ...props,
      config: { ...props.config, group: null, settings: { symbol: "HK.00700" } },
      onConfigChange,
    };
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(pinnedProps);
    const input = screen.getByTestId("symbol") as HTMLInputElement;
    expect(input.value).toBe("00700");
    fireEvent.focus(input);
    fireEvent.keyDown(input, { key: "Enter" }); // no change event — text is untouched
    await new Promise((r) => setTimeout(r, 0));
    expect(onConfigChange).not.toHaveBeenCalled();
    expect(input.value).toBe("00700");
  });
  it("shows an on-top label above every field", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    const { container } = wrap(props);
    // Scope to .col-head captions — plain getByText can collide with option
    // text that happens to match a label (e.g. the "Stop" order-type option).
    // Venue has no caption: it now lives in the header actions row (portaled
    // into PanelFrame's title bar when mounted under a frame), where the
    // select's own value is self-evident without a label.
    const captions = Array.from(container.querySelectorAll(".col-head")).map((el) => el.textContent);
    for (const label of ["Type", "TIF", "Session", "Price", "Stop", "Size", "Size by"]) {
      expect(captions).toContain(label);
    }
  });
  it("spells out order-type and sizing-mode options as full words", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    const typeOptions = Array.from(screen.getByTestId("order-type").querySelectorAll("option")).map((o) => o.textContent);
    expect(typeOptions).toEqual(["Limit", "Market", "Stop", "Stop Limit"]);
    const modeOptions = Array.from(screen.getByTestId("mode").querySelectorAll("option")).map((o) => o.textContent);
    expect(modeOptions).toEqual(["Shares", "Dollars", "Buying Power %", "Position"]);
  });
  it("defaults Session to Auto and lists all four options", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    expect((screen.getByTestId("session") as HTMLSelectElement).value).toBe("AUTO");
    const sessionOptions = Array.from(screen.getByTestId("session").querySelectorAll("option")).map((o) => o.textContent);
    expect(sessionOptions).toEqual(["Auto", "Regular", "Extended", "Overnight"]);
  });
  // Deck-wiring coverage (Task 3): unlike HotkeyDeck.test.tsx — which passes
  // venue/quote/etc. directly as props to a bare <HotkeyDeck> and mocks `oc`
  // — this exercises the real integration seam: the panel's own
  // venue/symbol/quote/positionQty derivation (lines ~56-69) flowing into
  // HotkeyDeck as props, and a click reaching the real OrderCommands.submit
  // → commands.sendCommand("SubmitOrder", ...) chain, not a mock.
  it("shows the deck's empty-state hint by default (no deck templates configured)", () => {
    const { props, stores } = mkProps();
    act(() => { stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() }); });
    wrap(props);
    expect(screen.getByTestId("deck-empty")).toBeTruthy();
  });
  it("renders a configured deck button under Strip 4 and fires it through the panel's own derived venue/quote", async () => {
    const deckConfig: OrderConfig = {
      activeVenue: "", templates: [{
        kind: "place", id: "buy1", label: "Buy 1", side: "BUY", type: "LIMIT", tif: "DAY",
        priceSource: "Ask", priceOffset: 0, sizing: { mode: "Shares", shares: 25 }, deck: true,
      }],
    };
    const { props, stores, linkGroups, sent } = mkProps(deckConfig);
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: status() });
      stores.quote.apply({ kind: "snapshot", topic: "md.quote" as never, payload: { symbol: "US.AAPL", bid: 3.4, ask: 3.5, last: 3.45, ts: "" } });
      linkGroups.focus("green", "US.AAPL");
    });
    wrap(props);
    await waitFor(() => expect(screen.getByTestId("deck-buy1")).toBeTruthy());
    expect(screen.queryByTestId("deck-empty")).toBeNull();
    fireEvent.click(screen.getByTestId("deck-buy1"));
    await waitFor(() => expect(sent.some((s) => s.name === "SubmitOrder")).toBe(true));
    const args = sent.find((s) => s.name === "SubmitOrder")?.args as SubmitOrderArgs;
    expect(args).toMatchObject({ venue: "alpaca-paper", symbol: "US.AAPL", side: "BUY", qty: 25, limitPrice: 3.5 });
  });
});
