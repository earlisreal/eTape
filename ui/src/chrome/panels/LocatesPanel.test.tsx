// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { makeStores } from "../../data/registry";
import type { AckMsg, ExecStatus } from "../../wire/contract";
import { LinkGroups, BroadcastChannelBus } from "../linkGroups";
import { ThemeProvider } from "../ThemeProvider";
import { ToastProvider } from "../Toast";
import { OrderConfigProvider } from "../exec/useOrderConfig";
import type { PanelProps } from "./registry";
import { LocatesPanel } from "./LocatesPanel";

afterEach(cleanup);

const status = (): ExecStatus => ({
  masterArmed: true,
  global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
  venues: [{ venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false, note: "", lastReconcileMs: null, gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 } }],
});

type PanelTestOptions = {
  execStatus?: ExecStatus;
  eligibility?: unknown;
  quote?: unknown;
  query?: (name: string, args: unknown) => unknown | Promise<unknown>;
  command?: (name: string, args: unknown) => Promise<AckMsg>;
};

function mkProps(options: PanelTestOptions = {}) {
  const stores = makeStores();
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const sentQueries: Array<{ name: string; args: unknown }> = [];
  const sentCommands: Array<{ name: string; args: unknown }> = [];
  const commands = {
    sendQuery: vi.fn(async (name: string, args: unknown): Promise<unknown> => {
      sentQueries.push({ name, args });
      const handled = options.query?.(name, args);
      if (handled !== undefined) return handled;
      if (name === "QueryLocateEligibility") return options.eligibility ?? { supported: true, found: true, borrowStatus: "hard_to_borrow", shortable: true, marginable: true, tradable: true, error: "" };
      if (name === "QueryLocateQuotes") {
        const symbol = (args as { symbols?: string[] }).symbols?.[0] ?? "US.AAPL";
        return options.quote ?? { quotes: [{ symbol, availableQty: 1200, price: "0.0123", quotedAt: "2026-07-06T13:30:00Z" }], errors: [], error: "" };
      }
      return { locates: [], nextPageToken: "", error: "" };
    }),
    sendCommand: vi.fn(async (name: string, args: unknown): Promise<AckMsg> => {
      sentCommands.push({ name, args });
      if (options.command) return options.command(name, args);
      if (name === "RequestLocate") {
        const a = args as { symbol: string; qty: number; limitPrice: string; allOrNone: boolean };
        return { kind: "ack", corrId: "c", status: "accepted", value: {
          id: "loc-test-1", symbol: a.symbol, requestedQty: a.qty, limitPrice: a.limitPrice, allOrNone: a.allOrNone,
          status: "active", createdAt: "2026-07-06T13:30:00Z", locatedQty: a.qty, locatedPrice: a.limitPrice,
          totalFee: "1.2300", expiresAt: "2026-07-07T13:30:00Z",
        } };
      }
      return { kind: "ack", corrId: "c", status: "accepted" };
    }),
  };
  const props = {
    config: { id: "locates-test", panelId: "locates", group: "green", settings: {} },
    stores,
    scheduler: {} as never,
    width: 320,
    height: 500,
    linkGroups,
    commands,
    symbol: "US.AAPL",
    onConfigChange: () => {},
  } as PanelProps;
  return { props, stores, linkGroups, sentQueries, sentCommands, execStatus: options.execStatus ?? status() };
}

function panelNode(props: PanelProps) {
  return <ThemeProvider><OrderConfigProvider commands={props.commands}><ToastProvider><LocatesPanel {...props} /></ToastProvider></OrderConfigProvider></ThemeProvider>;
}

function renderPanel(props: PanelProps) {
  return render(panelNode(props));
}

describe("LocatesPanel", () => {
  it("requests quotes only explicitly, confirms, and sends no short order", async () => {
    const { props, stores, sentQueries, sentCommands, execStatus } = mkProps();
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);

    await waitFor(() => expect(screen.getByTestId("locates-borrow-status").textContent).toBe("HARD TO BORROW"));
    expect(sentQueries.some((call) => call.name === "QueryLocateQuotes")).toBe(false);

    fireEvent.click(screen.getByTestId("get-locate-quote"));
    await waitFor(() => expect(screen.getByTestId("locate-quote")).toBeTruthy());
    expect((screen.getByTestId("locates-max-fee") as HTMLInputElement).value).toBe("0.0123");

    fireEvent.click(screen.getByTestId("request-locate"));
    fireEvent.click(screen.getByRole("button", { name: /^REQUEST LOCATE$/ }));
    await waitFor(() => expect(screen.getByTestId("locate-success")).toBeTruthy());

    const request = sentCommands.find((call) => call.name === "RequestLocate");
    expect(request?.args).toMatchObject({ venue: "alpaca-paper", symbol: "US.AAPL", qty: 100, limitPrice: "0.0123", allOrNone: true });
    expect((request?.args as { idempotencyKey: string }).idempotencyKey).toBeTruthy();
    expect(sentCommands.some((call) => call.name === "SubmitOrder")).toBe(false);
  });

  it("ignores duplicate confirmation clicks while a request is pending", async () => {
    let resolveRequest!: (value: AckMsg) => void;
    const pending = new Promise<AckMsg>((resolve) => { resolveRequest = resolve; });
    const { props, stores, sentCommands, execStatus } = mkProps({
      command: async (name) => name === "RequestLocate" ? pending : { kind: "ack", corrId: "c", status: "accepted" },
    });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status")).toBeTruthy());
    fireEvent.click(screen.getByTestId("get-locate-quote"));
    await waitFor(() => expect(screen.getByTestId("locate-quote")).toBeTruthy());
    fireEvent.click(screen.getByTestId("request-locate"));
    fireEvent.click(screen.getByRole("button", { name: /^REQUEST LOCATE$/ }));
    fireEvent.click(screen.getByRole("button", { name: /REQUESTING/ }));
    expect(sentCommands.filter((call) => call.name === "RequestLocate")).toHaveLength(1);
    resolveRequest({ kind: "ack", corrId: "c", status: "accepted", value: {
      id: "loc-pending", symbol: "US.AAPL", requestedQty: 100, limitPrice: "0.0123", allOrNone: true,
      status: "active", createdAt: "2026-07-06T13:30:00Z", locatedQty: 100, locatedPrice: "0.0123",
      totalFee: "1.2300", expiresAt: "2026-07-07T13:30:00Z",
    } });
    await waitFor(() => expect(screen.getByTestId("locate-success")).toBeTruthy());
  });

  it("blocks quantities that are not positive multiples of 100", async () => {
    const { props, stores, execStatus } = mkProps();
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status")).toBeTruthy());
    fireEvent.change(screen.getByTestId("locates-quantity"), { target: { value: "50" } });
    expect(screen.getByTestId("locates-quantity-error").textContent).toContain("multiple of 100");
    expect((screen.getByTestId("get-locate-quote") as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows ETB as no-locate-required and disables the workflow", async () => {
    const { props, stores, execStatus } = mkProps({ eligibility: { supported: true, found: true, borrowStatus: "easy_to_borrow", shortable: true, marginable: true, tradable: true, error: "" } });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status").textContent).toBe("EASY TO BORROW"));
    expect(screen.getByText("No locate required. Submit a normal short order.")).toBeTruthy();
    expect((screen.getByTestId("get-locate-quote") as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows non-shortable assets and disables locate controls", async () => {
    const { props, stores, execStatus } = mkProps({ eligibility: { supported: true, found: true, borrowStatus: "hard_to_borrow", shortable: false, marginable: true, tradable: true, error: "" } });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status").textContent).toBe("NOT SHORTABLE"));
    expect(screen.getByText("This asset is not shortable.")).toBeTruthy();
    expect((screen.getByTestId("get-locate-quote") as HTMLButtonElement).disabled).toBe(true);
  });

  it("does not redirect a non-Alpaca venue to an Alpaca account", () => {
    const mixed: ExecStatus = { ...status(), venues: [{ ...status().venues[0], venue: "tradezero-live", broker: "tradezero" }, status().venues[0]] };
    const { props, stores, linkGroups, sentQueries, execStatus } = mkProps({ execStatus: mixed });
    act(() => {
      stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus });
      linkGroups.focusVenue("green", "tradezero-live");
    });
    renderPanel(props);
    expect(screen.getByTestId("locates-unsupported").textContent).toContain("tradezero-live");
    expect(screen.getByText("Select an Alpaca venue to request locates.")).toBeTruthy();
    expect(sentQueries.some((call) => call.name === "QueryLocateEligibility")).toBe(false);
  });

  it("supports selecting among multiple Alpaca venues", async () => {
    const two: ExecStatus = { ...status(), venues: [...status().venues, { ...status().venues[0], venue: "alpaca-live" }] };
    const { props, stores, sentQueries, execStatus } = mkProps({ execStatus: two });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect((screen.getByTestId("locates-venue") as HTMLSelectElement).value).toBe("alpaca-paper"));
    fireEvent.change(screen.getByTestId("locates-venue"), { target: { value: "alpaca-live" } });
    await waitFor(() => expect(sentQueries.some((call) => call.name === "QueryLocateEligibility" && (call.args as { venue: string }).venue === "alpaca-live")).toBe(true));
  });

  it("clears the previous quote when the symbol or venue changes", async () => {
    const two: ExecStatus = { ...status(), venues: [...status().venues, { ...status().venues[0], venue: "alpaca-live" }] };
    const { props, stores, linkGroups, execStatus } = mkProps({ execStatus: two });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    const view = renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status")).toBeTruthy());
    fireEvent.click(screen.getByTestId("get-locate-quote"));
    await waitFor(() => expect(screen.getByTestId("locate-quote")).toBeTruthy());

    view.rerender(panelNode({ ...props, symbol: "US.TSLA" }));
    await waitFor(() => expect(screen.queryByTestId("locate-quote")).toBeNull());

    fireEvent.click(screen.getByTestId("get-locate-quote"));
    await waitFor(() => expect(screen.getByTestId("locate-quote")).toBeTruthy());
    act(() => linkGroups.focusVenue("green", "alpaca-live"));
    await waitFor(() => expect(screen.queryByTestId("locate-quote")).toBeNull());
  });

  it("loads history lazily and applies the active symbol filter", async () => {
    const { props, stores, sentQueries, execStatus } = mkProps();
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect(sentQueries.some((call) => call.name === "QueryLocates")).toBe(true));
    expect(sentQueries.filter((call) => call.name === "QueryLocates").every((call) => (call.args as { status: string }).status === "active")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "HISTORY" }));
    await waitFor(() => expect(sentQueries.some((call) => call.name === "QueryLocates" && (call.args as { status: string }).status === "expired")).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "ACTIVE" }));
    fireEvent.click(screen.getByRole("button", { name: "This Symbol" }));
    await waitFor(() => expect(sentQueries.some((call) => call.name === "QueryLocates" && (call.args as { symbol: string }).symbol === "US.AAPL")).toBe(true));
  });

  it("keeps the same idempotency key for an unchanged retry and shows provider errors", async () => {
    const { props, stores, sentCommands, execStatus } = mkProps({
      command: async (name) => name === "RequestLocate"
        ? { kind: "ack", corrId: "c", status: "blocked", reason: "requested price is no longer available" }
        : { kind: "ack", corrId: "c", status: "accepted" },
    });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status")).toBeTruthy());
    fireEvent.click(screen.getByTestId("get-locate-quote"));
    await waitFor(() => expect(screen.getByTestId("locate-quote")).toBeTruthy());
    fireEvent.click(screen.getByTestId("request-locate"));
    fireEvent.click(screen.getByRole("button", { name: /^REQUEST LOCATE$/ }));
    await waitFor(() => expect(screen.getByTestId("locates-error").textContent).toContain("price is no longer available"));
    const first = sentCommands.filter((call) => call.name === "RequestLocate")[0].args as { idempotencyKey: string };
    fireEvent.click(screen.getByTestId("request-locate"));
    fireEvent.click(screen.getByRole("button", { name: /^REQUEST LOCATE$/ }));
    await waitFor(() => expect(sentCommands.filter((call) => call.name === "RequestLocate")).toHaveLength(2));
    const second = sentCommands.filter((call) => call.name === "RequestLocate")[1].args as { idempotencyKey: string };
    expect(second.idempotencyKey).toBe(first.idempotencyKey);
  });

  it("ignores a quote response that belongs to the previous symbol", async () => {
    let resolveQuote!: (value: unknown) => void;
    const quote = new Promise<unknown>((resolve) => { resolveQuote = resolve; });
    const { props, stores, execStatus } = mkProps({ query: (name) => name === "QueryLocateQuotes" ? quote : undefined });
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status" as never, payload: execStatus }));
    const view = renderPanel(props);
    await waitFor(() => expect(screen.getByTestId("locates-borrow-status")).toBeTruthy());
    fireEvent.click(screen.getByTestId("get-locate-quote"));
    view.rerender(panelNode({ ...props, symbol: "US.TSLA" }));
    resolveQuote({ quotes: [{ symbol: "US.AAPL", availableQty: 1000, price: "0.0123", quotedAt: "2026-07-06T13:30:00Z" }], errors: [], error: "" });
    await waitFor(() => expect(screen.queryByTestId("locate-quote")).toBeNull());
  });
});
