// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { Profiler } from "react";
import { render, screen, waitFor, fireEvent, act, cleanup, within } from "@testing-library/react";
import { AppShell } from "./AppShell";
import { collectPanelIds } from "./backup";
import { WorkspaceStore, type Workspace } from "./workspace";
import { buildMonitoringWorkspace } from "./presets";
import { makeStores } from "../data/registry";
import { LinkGroups, BroadcastChannelBus } from "./linkGroups";
import { DemandRegistry } from "../wire/DemandRegistry";
import { Scheduler } from "../render/Scheduler";
import { browserRaf } from "../render/surface";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider } from "./Toast";
import { OrderConfigProvider } from "./exec/useOrderConfig";
import { SoundConfigProvider } from "../sound/SoundConfigProvider";
import type { AccountRow, ExecStatus, VenueStatus, WatchlistRow } from "../wire/contract";
import { TAPE_MIN_WIDTH } from "../render/tape/tapeLayout";
import { DockviewApi } from "dockview";
import type { HotkeyTargetChannel, HotkeyTargetMessage } from "./hotkeyTarget";

// dockview's DockviewComponent constructor watches its container via a real
// ResizeObserver on mount, which jsdom doesn't implement.
class FakeResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = FakeResizeObserver;

// Mock lightweight-charts so the panel test never touches a real canvas.
// timeScaleApi is a stable object (not a fresh literal per call) so a test can hold
// a reference to e.g. resetTimeScale and assert it was invoked by the SUT.
const timeScaleApi = { timeToCoordinate: vi.fn(() => 0), scrollToRealTime: vi.fn(), scrollPosition: vi.fn(() => 0),
  coordinateToLogical: vi.fn(() => 0), logicalToCoordinate: vi.fn(() => 0), resetTimeScale: vi.fn(),
  scrollToPosition: vi.fn(), subscribeVisibleLogicalRangeChange: vi.fn(), unsubscribeVisibleLogicalRangeChange: vi.fn(),
  getVisibleRange: vi.fn(() => null), setVisibleRange: vi.fn() };
// priceScaleApi is a stable object (not a fresh literal per call) so a test can hold
// a reference to applyOptions and assert it was invoked by the SUT (mirrors timeScaleApi above).
const priceScaleApi = { applyOptions: vi.fn(), width: vi.fn(() => 60) };
// paneApis is a stable array (not a fresh literal per `panes()` call) so setPaneStretchFactor
// calls made through one `panes()` read are visible to a later `panes()` read in the same
// test — mirrors why timeScaleApi/priceScaleApi above are hoisted instead of inlined.
const paneApis = [
  { attachPrimitive: vi.fn(), getHeight: vi.fn(() => 400), getStretchFactor: vi.fn(() => 1), setStretchFactor: vi.fn() },
  { attachPrimitive: vi.fn(), getHeight: vi.fn(() => 120), getStretchFactor: vi.fn(() => 1), setStretchFactor: vi.fn() },
];
const chartApi = {
  addSeries: vi.fn(() => ({ setData: vi.fn(), update: vi.fn(), applyOptions: vi.fn(), setSeriesOrder: vi.fn(),
    attachPrimitive: vi.fn(), priceToCoordinate: vi.fn(() => 0), coordinateToPrice: vi.fn(() => 0) })),
  removeSeries: vi.fn(),
  panes: vi.fn(() => paneApis),
  priceScale: vi.fn(() => priceScaleApi),
  timeScale: vi.fn(() => timeScaleApi),
  applyOptions: vi.fn(), resize: vi.fn(), remove: vi.fn(),
  takeScreenshot: vi.fn(() => document.createElement("canvas")),
  subscribeCrosshairMove: vi.fn(),
  unsubscribeCrosshairMove: vi.fn(),
};
vi.mock("lightweight-charts", () => ({
  createChart: vi.fn(() => chartApi),
  createTextWatermark: vi.fn(() => ({ detach: vi.fn(), applyOptions: vi.fn() })),
  CandlestickSeries: "Candlestick", HistogramSeries: "Histogram", LineSeries: "Line",
  BarSeries: "Bar", AreaSeries: "Area", CrosshairMode: { Magnet: 1 },
}));

// jsdom has no PointerEvent constructor; dockview's tab-activation handler
// listens for "pointerdown" and reads `.button` off the event. A plain
// MouseEvent carries the same `.button`/`.shiftKey` fields dockview reads and
// dispatches under an arbitrary type string, so this stands in for a real
// pointerdown click on a dockview tab.
function clickTab(el: Element): void {
  el.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 }));
}

class TargetChannel implements HotkeyTargetChannel {
  readonly messages: HotkeyTargetMessage[] = [];
  private listener: ((event: MessageEvent<HotkeyTargetMessage>) => void) | undefined;
  postMessage(message: HotkeyTargetMessage): void { this.messages.push(message); }
  addEventListener(_type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void { this.listener = listener; }
  removeEventListener(_type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void {
    if (this.listener === listener) this.listener = undefined;
  }
  close(): void { this.listener = undefined; }
}

function mount(seed: Workspace, opts?: { workspaceName?: string; onTransitionApplied?: () => void; onRender?: () => void; hotkeyTargetChannel?: HotkeyTargetChannel }) {
  const stores = makeStores();
  const scheduler = new Scheduler(browserRaf, () => {});
  const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
  const commands = {
    sendCommand: vi.fn(async () => ({ kind: "ack" as const, corrId: "c", status: "accepted" as const, value: undefined })),
    sendQuery: vi.fn(async () => []),
  };
  const demandRegistry = new DemandRegistry({ sendCommand: commands.sendCommand, onState: () => {} });
  const saved: Workspace[] = [];
  const client = {
    sendCommand: vi.fn(async (name: string, args: unknown) => {
      if (name === "GetConfig") return { status: "accepted" as const, value: seed };
      if (name === "SetConfig") { saved.push(structuredClone((args as { value: Workspace }).value)); return { status: "accepted" as const }; }
      return { status: "accepted" as const };
    }),
  };
  // Debounce as fast as possible so tests don't need real timers/sleeps.
  const workspaceStore = new WorkspaceStore(client, 1);
  render(
    <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
      <SoundConfigProvider commands={commands}>
        <Profiler id="AppShell" onRender={() => opts?.onRender?.()}>
          <AppShell workspaceName={opts?.workspaceName ?? "default"} stores={stores} scheduler={scheduler} workspaceStore={workspaceStore}
            linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands} engineState="open"
            {...(opts?.hotkeyTargetChannel ? { hotkeyTargetChannel: opts.hotkeyTargetChannel } : {})}
            {...(opts?.onTransitionApplied ? { onTransitionApplied: opts.onTransitionApplied } : {})} />
        </Profiler>
      </SoundConfigProvider>
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
  return { saved, workspaceStore, linkGroups, stores, commands };
}

describe("AppShell execution subscription", () => {
  const seed: Workspace = {
    name: "default",
    layoutVersion: 8,
    panels: [{ id: "connection-1", panelId: "connection-status", group: null, settings: {} }],
    layout: null,
  };
  const venue = (over: Partial<VenueStatus> = {}): VenueStatus => ({
    venue: "alpaca-paper", broker: "alpaca", connected: true, reconcilePending: false,
    note: "", lastReconcileMs: null,
    gate: { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 },
    ...over,
  });
  const status = (masterArmed = false, venues = [venue()]): ExecStatus => ({
    masterArmed,
    global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues,
  });
  const account = (dayPnl: number): AccountRow => ({
    venue: "alpaca-paper", equity: 100, buyingPower: 400, availableCash: 50,
    sodEquity: 100, realized: 0, dayPnl, leverage: 4, tsMs: dayPnl,
    cycleStartMs: 0, cycleRealized: 0,
  });

  it("does not re-render for sequential account-only updates", async () => {
    let renders = 0;
    const { stores } = mount(seed, { onRender: () => { renders++; } });
    await waitFor(() => expect(screen.getByText("Link")).toBeTruthy());

    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: status() }));
    await waitFor(() => expect(screen.getByTestId("arm-chip").textContent).toContain("UNLOCK TRADING"));
    const afterStatus = renders;

    for (const dayPnl of [1, 2, 3, 4]) {
      act(() => stores.exec.apply({ kind: "delta", topic: "exec.account", key: "alpaca-paper", payload: account(dayPnl) }));
    }

    expect(renders).toBe(afterStatus);
  });

  it("does not re-render for shell-equivalent status replacements", async () => {
    let renders = 0;
    const { stores } = mount(seed, { onRender: () => { renders++; } });
    await waitFor(() => expect(screen.getByText("Link")).toBeTruthy());

    const initial = status();
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: initial }));
    await waitFor(() => expect(screen.getByTestId("arm-chip").textContent).toContain("UNLOCK TRADING"));
    const afterInitial = renders;

    act(() => stores.exec.apply({
      kind: "snapshot", topic: "exec.status",
      payload: {
        ...initial,
        global: { ...initial.global, maxDayLoss: 500 },
        venues: [venue({ connected: false, note: "reconnecting" })],
      },
    }));

    expect(renders).toBe(afterInitial);
  });

  it("still updates the arm chip for genuine master-arm changes", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.getByText("Link")).toBeTruthy());

    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: status(false) }));
    await waitFor(() => expect(screen.getByTestId("arm-chip").textContent).toContain("UNLOCK TRADING"));
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: status(true) }));
    await waitFor(() => expect(screen.getByTestId("arm-chip").textContent).toContain("LOCK TRADING"));
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: status(false) }));
    await waitFor(() => expect(screen.getByTestId("arm-chip").textContent).toContain("UNLOCK TRADING"));
  });

  it("does not re-render for watchlist row-only refreshes", async () => {
    let renders = 0;
    const { stores } = mount(seed, { onRender: () => { renders++; } });
    await waitFor(() => expect(screen.getByText("Link")).toBeTruthy());
    const afterMount = renders;

    const row = (last: number): WatchlistRow => ({ symbol: "US.AAPL", last, changePct: 1, volume: 1000 });
    publishWatchlist(stores, ["US.AAPL"], [row(100)], "2026-08-10T10:00:00.000Z");
    publishWatchlist(stores, ["US.AAPL"], [row(101)], "2026-08-10T10:00:01.000Z");

    expect(stores.watchlist.getSnapshot().rows.get("US.AAPL")?.last).toBe(101);
    expect(renders).toBe(afterMount);
  });
});

// Publishes a watchlist.rows snapshot with the given symbols — the shape
// WatchlistStore.apply expects (see WatchlistStore.test.ts); `rows`/`refreshedAt`
// are irrelevant to the mode-edge effect below, which only reads `.symbols`.
function publishWatchlist(stores: ReturnType<typeof mount>["stores"], symbols: string[], rows: WatchlistRow[] = [], refreshedAt: string | null = null) {
  act(() => stores.watchlist.apply({ kind: "snapshot", topic: "watchlist.rows", payload: { symbols, rows, refreshedAt } }));
}

function publishSessionMode(stores: ReturnType<typeof mount>["stores"], mode: "pending" | "live" | "demo") {
  act(() => stores.session.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } }));
}

describe("AppShell onConfigChange", () => {
  // Regression test for the final-review Finding 1 fix: PanelFrame's per-panel
  // component factory is captured ONCE by dockview at panel-creation time, so a
  // handler baked into that factory (onConfigChange) closes over whatever `ws`
  // existed at THAT panel's creation render — not the current one. A panel added
  // to the workspace AFTER an earlier panel was created must survive a later
  // onConfigChange call fired from that earlier panel's own (stale) closure.
  it("does not drop a later-added panel when an earlier panel's onConfigChange fires", async () => {
    const seed: Workspace = { name: "default", layoutVersion: 8, panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: {} }], layout: null };
    const { saved } = mount(seed);

    // Wait for the initial (pre-existing) panel's content to actually mount inside
    // dockview's portal target before doing anything else.
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getAllByText("Symbol")[0]).toBeTruthy());

    // Add a second panel via the "+ Add panel" popover — this changes `ws` in
    // AppShell's React state AFTER the open-orders PanelFrame factory (and the
    // onConfigChange closure baked into it) was already created.
    fireEvent.click(screen.getByText("+ Add panel"));
    fireEvent.click(screen.getByText("Stock Info"));

    // The Stock Info panel landed as a second tab in the same dockview group and is
    // now the active one — switch back to the open-orders tab (dockview only mounts
    // the active tab's content) before touching its sort header. dockview's tab
    // activates on `pointerdown`, not `click`.
    act(() => clickTab(screen.getByTestId("panel-tab-orders-1")));
    await waitFor(() => expect(screen.getAllByText("Symbol")[0]).toBeTruthy());

    // Trigger the pre-existing open-orders panel's onConfigChange path (sort-by
    // symbol persists via onConfigChange — see OpenOrdersPanel/AccountPanel).
    fireEvent.click(screen.getAllByText("Symbol")[0]);

    await waitFor(() => expect(saved.length).toBeGreaterThan(0));
    const last = saved[saved.length - 1];
    const panelIds = last.panels.map((p) => p.panelId);
    // Both the original open-orders panel AND the just-added Stock Info panel
    // (registry key "stock-info") must survive the save — the bug silently
    // dropped the latter.
    expect(panelIds).toContain("open-orders");
    expect(panelIds).toContain("stock-info");
    expect(last.panels).toHaveLength(2);
  });

  // Regression for the settings-clobber bug: onConfigChange now MERGES a patch
  // into the stored settings. Panels/PanelFrame only ever see the config frozen
  // at their creation, so under the old replace semantics any write (e.g. a
  // type-to-load symbol commit spreading frozen settings) wiped every sibling
  // key persisted since mount — a chart's indicators silently vanished from the
  // workspace after a symbol change.
  it("merges a settings patch without dropping sibling keys", async () => {
    const seed: Workspace = {
      name: "default",
      layoutVersion: 8,
      panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: { keepMe: "precious" } }],
      layout: null,
    };
    const { saved } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getAllByText("Symbol")[0]).toBeTruthy());

    // Sort-by-symbol on the Orders table (index 0 — it renders first, ahead of
    // the Positions/Trade-History tabs, both of which also have a "Symbol"
    // column) persists via onConfigChange with an `{ ordersSort }` patch.
    fireEvent.click(screen.getAllByText("Symbol")[0]);

    await waitFor(() => expect(saved.length).toBeGreaterThan(0));
    const settings = saved[saved.length - 1].panels[0].settings;
    expect(settings.keepMe).toBe("precious");   // sibling key survives the patch
    expect(settings.ordersSort).toBeTruthy();   // and the patch itself landed
  });
});

describe("AppShell custom panel headers", () => {
  it("renders a singleton header's group picker outside Dockview's clipped tab host", async () => {
    const seed: Workspace = { name: "default", layoutVersion: 8, panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: {} }], layout: null };
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getAllByText("Symbol")[0]).toBeTruthy());

    const host = screen.getByTestId("panel-tab-orders-1");
    fireEvent.click(within(host).getByLabelText("link group"));
    const picker = screen.getByText("Red group", { exact: true }).closest(".popover");
    expect(picker?.parentElement).toBe(document.body);
  });

  it("keeps the full-width header for one panel and restores tabs above it for a second", async () => {
    const seed: Workspace = { name: "default", layoutVersion: 8, panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: {} }], layout: null };
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getAllByText("Symbol")[0]).toBeTruthy());

    const tabStrip = () => document.querySelector(".dv-tabs-and-actions-container") as HTMLElement;
    expect(tabStrip().style.display).not.toBe("none");
    const firstHost = screen.getByTestId("panel-tab-orders-1");
    expect(within(firstHost).getByLabelText("close panel")).toBeTruthy();
    expect(firstHost.querySelector(".panel-focused-header")).not.toBeNull();

    fireEvent.click(screen.getByText("+ Add panel"));
    fireEvent.click(screen.getByText("Stock Info"));
    await waitFor(() => expect(document.querySelectorAll('[data-testid^="panel-tab-"]').length).toBe(2));
    expect(document.querySelectorAll(".dv-default-tab").length).toBe(2);
    expect(document.querySelectorAll(".etape-panel-tab-host").length).toBe(0);
    expect(document.querySelector(".panel-focused-header")).not.toBeNull();
    expect(screen.getAllByLabelText("Close tab")).toHaveLength(2);
    expect(screen.getByLabelText("close panel")).toBeTruthy();
  });
});

describe("AppShell group-symbol persistence (Bug 5: refresh resetting a grouped symbol to AAPL)", () => {
  // LinkGroups itself is rebuilt empty on every page load (App.tsx's useMemo);
  // without hydrating it from the saved workspace doc BEFORE panels mount, a
  // grouped panel's very first render would fall back to its own creation-time
  // settings.symbol seed (AAPL) instead of the group's actual last-focused symbol.
  it("hydrates LinkGroups from the saved workspace's groups map before panels mount", async () => {
    const seed: Workspace = {
      name: "default",
      layoutVersion: 8,
      panels: [{ id: "n1", panelId: "stock-info", group: "green", settings: {} }],
      layout: null,
      groups: { green: "US.NVDA" },
    };
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getByTestId("panel-symbol").textContent).toBe("NVDA"));
  });

  it("persists a group's focused-symbol change into the workspace doc", async () => {
    const seed: Workspace = {
      name: "default",
      layoutVersion: 8,
      panels: [{ id: "n1", panelId: "stock-info", group: "green", settings: {} }],
      layout: null,
    };
    const { saved, linkGroups } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getByTestId("panel-symbol")).toBeTruthy());

    act(() => { linkGroups.focus("green", "US.NVDA"); });

    await waitFor(() => expect(saved.some((w) => w.groups?.green === "US.NVDA")).toBe(true));
  });
});

describe("AppShell Monitoring Scanner Sync", () => {
  it("offers Follow Monitoring from a Scanner in another workspace", async () => {
    const seed: Workspace = {
      name: "trading-window",
      layoutVersion: 8,
      panels: [{ id: "source-scanner", panelId: "scanner", group: null, settings: {} }],
      layout: null,
    };
    mount(seed, { workspaceName: "trading-window" });

    await waitFor(() => expect(screen.getByRole("button", { name: "Follow Monitoring" })).toBeTruthy());
  });

  it("keeps a cross-window source by identity through close, deletion, replacement, and restart", async () => {
    const source: Workspace = {
      name: "source-window",
      layoutVersion: 8,
      panels: [{ id: "source-scanner", panelId: "scanner", group: null, settings: { sort: { col: "last", dir: "asc" } } }],
      layout: null,
    };
    const replacement: Workspace = {
      name: "replacement-window",
      layoutVersion: 8,
      panels: [{ id: "replacement-scanner", panelId: "scanner", group: null, settings: {} }],
      layout: null,
    };
    const docs = new Map<string, Workspace>([
      ["monitoring", { ...buildMonitoringWorkspace(), layout: null }],
      ["source-window", source],
      ["replacement-window", replacement],
    ]);
    const mountWindow = (name: string) => {
      const stores = makeStores();
      const scheduler = new Scheduler(browserRaf, () => {});
      const commands = {
        sendCommand: vi.fn(async () => ({ kind: "ack" as const, corrId: "c", status: "accepted" as const, value: undefined })),
        sendQuery: vi.fn(async () => []),
      };
      const client = {
        sendCommand: vi.fn(async (command: string, args: unknown) => {
          const key = (args as { key?: string }).key ?? "";
          if (command === "GetConfig") return { status: "accepted" as const, value: docs.get(key.replace(/^workspace\./, "")) ?? null };
          if (command === "SetConfig") {
            const value = (args as { value: Workspace }).value;
            docs.set(key.replace(/^workspace\./, ""), structuredClone(value));
          }
          return { status: "accepted" as const };
        }),
      };
      const workspaceStore = new WorkspaceStore(client, 1);
      const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
      const demandRegistry = new DemandRegistry({ sendCommand: commands.sendCommand, onState: () => {} });
      const view = render(
        <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
          <SoundConfigProvider commands={commands}>
            <AppShell workspaceName={name} stores={stores} scheduler={scheduler} workspaceStore={workspaceStore}
              linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands} engineState="open" />
          </SoundConfigProvider>
        </OrderConfigProvider></ToastProvider></ThemeProvider>,
      );
      return { ...view, stores, workspaceStore };
    };

    const monitor = mountWindow("monitoring");
    const sourceWindow = mountWindow("source-window");
    await waitFor(() => expect(within(sourceWindow.container).getByRole("button", { name: "Follow Monitoring" })).toBeTruthy());
    fireEvent.click(within(sourceWindow.container).getByRole("button", { name: "Follow Monitoring" }));
    await waitFor(() => expect(docs.get("monitoring")?.scannerSync).toEqual({
      enabled: true, sourceWorkspaceId: "source-window", sourcePanelId: "source-scanner",
    }));

    sourceWindow.unmount();
    act(() => monitor.stores.scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth", payload: {
      refreshedAt: "2026-08-15T08:00:00.000Z",
      rows: [
        { symbol: "US.A", changePct: 1, last: 30, floatShares: 1, volume: 1 },
        { symbol: "US.B", changePct: 2, last: 10, floatShares: 1, volume: 1 },
      ],
    } }));
    await waitFor(() => expect(docs.get("monitoring")?.panels.find((panel) => panel.id === "m-chart-red")?.settings.symbol).toBe("US.B"));
    expect(docs.get("monitoring")?.panels.find((panel) => panel.id === "m-chart-green")?.settings.symbol).toBe("US.A");

    await monitor.workspaceStore.flush();
    const sourceStore = new WorkspaceStore({
      sendCommand: vi.fn(async (command: string, args: unknown) => {
        const key = (args as { key?: string }).key ?? "";
        if (command === "GetConfig") return { status: "accepted" as const, value: docs.get(key.replace(/^workspace\./, "")) ?? null };
        if (command === "SetConfig") docs.set(key.replace(/^workspace\./, ""), structuredClone((args as { value: Workspace }).value));
        return { status: "accepted" as const };
      }),
    }, 1);
    sourceStore.save({ ...source, panels: [] });
    await sourceStore.flush();
    expect(docs.get("monitoring")?.scannerSync?.sourceWorkspaceId).toBe("source-window");
    await waitFor(() => expect(within(monitor.container).getByText("Paused — Scanner Source unavailable")).toBeTruthy());
    act(() => monitor.stores.scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth", payload: {
      refreshedAt: "2026-08-15T08:00:30.000Z",
      rows: [{ symbol: "US.C", changePct: 9, last: 1, floatShares: 1, volume: 1 }],
    } }));
    await new Promise((resolve) => setTimeout(resolve, 1100));
    expect(docs.get("monitoring")?.panels.find((panel) => panel.id === "m-chart-red")?.settings.symbol).toBe("US.B");

    const replacementWindow = mountWindow("replacement-window");
    await waitFor(() => expect(within(replacementWindow.container).getByRole("button", { name: "Follow Monitoring" })).toBeTruthy());
    fireEvent.click(within(replacementWindow.container).getByRole("button", { name: "Follow Monitoring" }));
    await waitFor(() => expect(docs.get("monitoring")?.scannerSync).toEqual({
      enabled: true, sourceWorkspaceId: "replacement-window", sourcePanelId: "replacement-scanner",
    }));

    monitor.unmount();
    const restarted = mountWindow("monitoring");
    act(() => restarted.stores.scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth", payload: {
      refreshedAt: "2026-08-15T08:01:00.000Z",
      rows: [{ symbol: "US.Z", changePct: 9, last: 1, floatShares: 1, volume: 1 }],
    } }));
    await waitFor(() => expect(docs.get("monitoring")?.panels.find((panel) => panel.id === "m-chart-red")?.settings.symbol).toBe("US.Z"));
    replacementWindow.unmount();
    restarted.unmount();
  });

  it("persists source selection, follows ranked rows, preserves chart settings, and remembers Sync off", async () => {
    const { saved, stores } = mount(buildMonitoringWorkspace(), { workspaceName: "monitoring" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Follow Monitoring" })).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "Follow Monitoring" }));
    await waitFor(() => expect(saved.some((workspace) => workspace.scannerSync?.enabled && workspace.scannerSync.sourcePanelId === "m-scanner")).toBe(true));

    act(() => stores.scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth", payload: {
      refreshedAt: "2026-08-15T08:00:00.000Z",
      rows: [
        { symbol: "US.A", changePct: 4, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.B", changePct: 3, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.C", changePct: 2, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.D", changePct: 1, last: 1, floatShares: 1, volume: 1 },
      ],
    } }));
    await waitFor(() => {
      const latest = saved[saved.length - 1];
      expect(latest?.panels.find((panel) => panel.id === "m-chart-red")?.settings).toEqual({ timeframe: "1m", symbol: "US.A" });
    });

    fireEvent.click(screen.getByRole("button", { name: "Disable Scanner Sync" }));
    await waitFor(() => expect(saved.some((workspace) => workspace.scannerSync?.enabled === false && workspace.scannerSync.sourcePanelId === "m-scanner")).toBe(true));
  });

  it("excludes linked charts from the target count", async () => {
    const { saved, stores } = mount(buildMonitoringWorkspace(), { workspaceName: "monitoring" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Follow Monitoring" })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Follow Monitoring" }));

    act(() => stores.scanner.apply({ kind: "snapshot", topic: "scanner.rank", key: "rth", payload: {
      refreshedAt: "2026-08-15T08:00:00.000Z",
      rows: [
        { symbol: "US.A", changePct: 4, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.B", changePct: 3, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.C", changePct: 2, last: 1, floatShares: 1, volume: 1 },
        { symbol: "US.D", changePct: 1, last: 1, floatShares: 1, volume: 1 },
      ],
    } }));
    await waitFor(() => expect(saved.some((workspace) => workspace.panels.find((panel) => panel.id === "m-chart-red")?.settings.symbol === "US.A")).toBe(true));

    fireEvent.click(within(screen.getByTestId("panel-tab-m-chart-green")).getByLabelText("link group"));
    fireEvent.click(screen.getByRole("button", { name: "Red group" }));

    await waitFor(() => expect(saved.some((workspace) => workspace.panels.find((panel) => panel.id === "m-chart-green")?.group === "red")).toBe(true));
    await waitFor(() => expect(saved.some((workspace) => workspace.panels.find((panel) => panel.id === "m-chart-yellow")?.settings.symbol === "US.B")).toBe(true));
  });
});

describe("AppShell venue-setup prompt (Task 3: venues/creds redesign)", () => {
  const VENUE_SETUP_HIDDEN_KEY = "etape.venueSetupHidden";
  const seed: Workspace = { name: "default", layoutVersion: 8, panels: [], layout: null };

  const emptyGate = { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 };
  const venueStatus = (id: string, broker: VenueStatus["broker"] = "alpaca"): VenueStatus => ({
    venue: id, broker, connected: true, reconcilePending: false,
    note: "", lastReconcileMs: null, gate: emptyGate,
  });
  const status = (venues: VenueStatus[]): ExecStatus => ({
    masterArmed: false,
    global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues,
  });
  const publishStatus = (stores: ReturnType<typeof mount>["stores"], venues: VenueStatus[]) => {
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: status(venues) }));
  };

  beforeEach(() => { localStorage.removeItem(VENUE_SETUP_HIDDEN_KEY); });
  afterEach(() => { localStorage.removeItem(VENUE_SETUP_HIDDEN_KEY); });

  it("does not show before the first exec.status snapshot arrives (no flash during connect)", async () => {
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
  });

  it("shows once exec.status arrives with zero venues", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());
  });

  it("still shows when only the auto-seeded sim practice venue is configured", async () => {
    // First run auto-seeds a paper "sim" venue (config.SeedDefaultIfMissing) --
    // that's not a real broker, so the nudge toward live trading must persist.
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("sim-paper", "sim")]);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());
  });

  it.each(["demo"] as const)(
    "does not show during a confirmed %s session, even with no real venue",
    async (mode) => {
      // Nudging toward configuring a broker "to trade live" makes no sense
      // mid-replay/demo — venue edits need an engine restart anyway, which
      // would kill the session. Regression: this modal blocked
      // e2e/replay-launcher's later assertions because it showed
      // unconditionally off "no real venue". "demo" mirrors "replay" here
      // (Task 3: widened SessionState.mode + AppShell's showVenueSetup gate).
      const { stores } = mount(seed);
      await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
      act(() => stores.session.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } }));
      publishStatus(stores, []);
      await waitFor(() => expect(stores.exec.status()?.venues.length).toBe(0));
      expect(screen.queryByText("Add a broker to trade live")).toBeNull();
    },
  );

  it("does not show once a real (non-sim) venue is configured", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("alpaca-paper")]);
    // Give any (absent) render a chance, then assert it never appeared.
    await waitFor(() => expect(stores.exec.status()?.venues.length).toBe(1));
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
  });

  it("does not show once a real venue joins the auto-seeded sim venue", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("sim-paper", "sim"), venueStatus("alpaca-paper", "alpaca")]);
    await waitFor(() => expect(stores.exec.status()?.venues.length).toBe(2));
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
  });

  it("clicking 'Configure venues' opens Settings on the Venues & creds section and closes the prompt", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "Configure venues" }));

    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
    // The nav button alone doesn't prove which section is active — SettingsModal
    // renders all 4 nav entries unconditionally regardless of the current
    // section. Assert on VenuesSection's own "Venues" heading (distinct from
    // e.g. AppearanceSection's "Theme" heading) to prove the click actually
    // routed to the Venues section, not just opened the modal on some other one.
    expect(screen.getByRole("button", { name: /venues & creds/i })).toBeTruthy();
    expect(screen.getByText("Venues")).toBeTruthy();
  });

  it("dismissing without ticking the checkbox hides it for the session but does not persist to localStorage", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
    expect(localStorage.getItem(VENUE_SETUP_HIDDEN_KEY)).toBeNull();

    // Re-publishing the same empty-venues status must not re-show it THIS session.
    publishStatus(stores, []);
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
  });

  it("dismissing without ticking the checkbox lets the prompt reappear on a fresh mount (simulated reload)", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
    expect(localStorage.getItem(VENUE_SETUP_HIDDEN_KEY)).toBeNull();

    cleanup(); // unmount this AppShell instance — simulates a fresh app launch

    const { stores: stores2 } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores2, []);
    // Untracked dismissal must NOT persist across launches — venues are still
    // empty, so the prompt is the non-negotiable half of the contract: it has
    // to come back.
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());
  });

  it("ticking 'don't show again' + dismissing persists the flag so a fresh mount with the same status stays hidden", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());

    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(localStorage.getItem(VENUE_SETUP_HIDDEN_KEY)).toBe("1");

    cleanup(); // unmount this AppShell instance — simulates a fresh app launch

    const { stores: stores2 } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores2, []);
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();
  });
});

describe("AppShell try-demo CTA (Task 6: U4 first-run affordances)", () => {
  // Zero panels so EmptyState (and its "Try demo" CTA) is the rendered
  // workspace surface throughout. Deliberately never publishes an exec.status
  // snapshot in these EmptyState-focused tests — execStatus stays null, which
  // keeps VenueSetupPrompt from also mounting (its own gate requires
  // execStatus !== null) and colliding with EmptyState's "Try demo" button
  // on an accessible name (see the dedicated VenueSetupPrompt-side test below,
  // which scopes its query with `within` instead, since production really
  // does mount both simultaneously in that scenario).
  const seed: Workspace = { name: "default", layoutVersion: 8, panels: [], layout: null };

  it("shows the CTA while sessionMode is pending (the default before the first snapshot)", async () => {
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    expect(screen.getByRole("button", { name: "Try demo" })).toBeTruthy();
  });

  it.each(["demo"] as const)(
    "hides the CTA during a confirmed %s session (already practicing — offering it again would be confusing)",
    async (mode) => {
      const { stores } = mount(seed);
      await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
      act(() => stores.session.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } }));
      expect(screen.queryByRole("button", { name: "Try demo" })).toBeNull();
    },
  );

  it("shows the CTA once a confirmed live session snapshot arrives", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    act(() => stores.session.apply({ kind: "snapshot", topic: "sys.session", payload: { mode: "live" } }));
    expect(screen.getByRole("button", { name: "Try demo" })).toBeTruthy();
  });

  it("clicking the EmptyState CTA sends StartDemo", async () => {
    const { commands } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "Try demo" }));
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("StartDemo", {}));
  });

  it("clicking 'Try demo' inside the venue-setup prompt also sends StartDemo", async () => {
    // Both AppShell.tsx call sites thread the SAME onTryDemo callback — this
    // proves the wiring reaches this second call site too, not just
    // EmptyState's. Zero venues makes VenueSetupPrompt mount alongside
    // EmptyState's own "Try demo" CTA (both true by default: no real venue,
    // pending session), so the query is scoped to the dialog to avoid an
    // ambiguous duplicate accessible name across the two surfaces.
    const { stores, commands } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    act(() => stores.exec.apply({
      kind: "snapshot", topic: "exec.status",
      payload: { masterArmed: false, global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 }, venues: [] },
    }));
    const dialog = await waitFor(() => screen.getByRole("dialog"));
    fireEvent.click(within(dialog).getByRole("button", { name: "Try demo" }));
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("StartDemo", {}));
  });
});

describe("AppShell Alpaca-1m-history hint banner", () => {
  const ALPACA_HINT_HIDDEN_KEY = "etape.alpacaHintHidden";
  const seed: Workspace = { name: "default", layoutVersion: 8, panels: [], layout: null };

  const emptyGate = { maxOrderValue: 0, maxPositionValue: 0, maxPositionShares: 0, maxOpenOrders: 0 };
  const venueStatus = (id: string, broker: VenueStatus["broker"]): VenueStatus => ({
    venue: id, broker, connected: true, reconcilePending: false,
    note: "", lastReconcileMs: null, gate: emptyGate,
  });
  const status = (venues: VenueStatus[]): ExecStatus => ({
    masterArmed: false,
    global: { maxDayLoss: 0, maxSymbolPositionValue: 0, maxSymbolPositionShares: 0 },
    venues,
  });
  const publishStatus = (stores: ReturnType<typeof mount>["stores"], venues: VenueStatus[]) => {
    act(() => stores.exec.apply({ kind: "snapshot", topic: "exec.status", payload: status(venues) }));
  };

  beforeEach(() => { localStorage.removeItem(ALPACA_HINT_HIDDEN_KEY); });
  afterEach(() => { localStorage.removeItem(ALPACA_HINT_HIDDEN_KEY); });

  it("does not show before the first exec.status snapshot arrives", async () => {
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });

  it("is hidden at zero venues while the venue-setup prompt is showing", async () => {
    // Suppressed so it doesn't double up with the one-shot venue-setup modal,
    // which covers this exact case first -- see the "appears ... once the
    // venue-setup prompt is dismissed" test below for the handoff.
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });

  it("is hidden at sim-only while the venue-setup prompt is showing", async () => {
    // The auto-seeded first-run sim venue is not a "real" venue -- the
    // venue-setup prompt covers this case first; the banner takes over once
    // that prompt is dismissed (see the test below).
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("sim-paper", "sim")]);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });

  it("appears at zero venues once the venue-setup prompt is dismissed", async () => {
    // This is the case the relaxed gate exists for: a fresh install with no
    // venues at all gets the one-shot modal first, then the persistent
    // banner takes over once that modal is out of the way.
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, []);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();

    await waitFor(() => expect(screen.getByTestId("alpaca-backfill-banner")).toBeTruthy());
  });

  it("appears at sim-only once the venue-setup prompt is dismissed", async () => {
    // The no-real-venue case this task exists to fix: Earl's machine has only
    // the auto-seeded sim venue, so the banner must hand off from the
    // venue-setup modal once dismissed, not stay suppressed forever.
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("sim-paper", "sim")]);
    await waitFor(() => expect(screen.getByText("Add a broker to trade live")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "I'll do it later" }));
    expect(screen.queryByText("Add a broker to trade live")).toBeNull();

    await waitFor(() => expect(screen.getByTestId("alpaca-backfill-banner")).toBeTruthy());
  });

  it("shows once a non-Alpaca venue is configured", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("tz-1", "tradezero")]);
    await waitFor(() => expect(screen.getByTestId("alpaca-backfill-banner")).toBeTruthy());
  });

  it("does not show once an Alpaca venue is configured", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("alpaca-paper", "alpaca")]);
    await waitFor(() => expect(stores.exec.status()?.venues.length).toBe(1));
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });

  it("does not show once an Alpaca venue joins a mix of other venues", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("tz-1", "tradezero"), venueStatus("alpaca-paper", "alpaca")]);
    await waitFor(() => expect(stores.exec.status()?.venues.length).toBe(2));
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });

  it.each(["demo"] as const)(
    "does not show during a confirmed %s session, even with no Alpaca venue",
    async (mode) => {
      // Venue edits need an engine restart, which would kill a replay/demo
      // session -- mirrors showVenueSetup's same guard. Uses a real non-sim
      // venue so showVenueSetup's own gate is already false here, isolating
      // this assertion to showAlpacaHint's own sessionMode guard.
      const { stores } = mount(seed);
      await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
      act(() => stores.session.apply({ kind: "snapshot", topic: "sys.session", payload: { mode } }));
      publishStatus(stores, [venueStatus("tz-1", "tradezero")]);
      await waitFor(() => expect(stores.exec.status()?.venues.length).toBe(1));
      expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
    },
  );

  it("clicking 'Set up Alpaca' opens Settings on the Venues & creds section and closes the banner", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("tz-1", "tradezero")]);
    await waitFor(() => expect(screen.getByTestId("alpaca-backfill-banner")).toBeTruthy());

    fireEvent.click(screen.getByTestId("alpaca-banner-setup"));

    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
    expect(screen.getByText("Venues")).toBeTruthy();
  });

  it("dismissing hides it for the session but does not persist to localStorage", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("tz-1", "tradezero")]);
    await waitFor(() => expect(screen.getByTestId("alpaca-backfill-banner")).toBeTruthy());

    fireEvent.click(screen.getByTestId("alpaca-banner-dismiss"));
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
    expect(localStorage.getItem(ALPACA_HINT_HIDDEN_KEY)).toBe("1");
  });

  it("staying dismissed persists across a fresh mount (simulated reload)", async () => {
    const { stores } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("tz-1", "tradezero")]);
    await waitFor(() => expect(screen.getByTestId("alpaca-backfill-banner")).toBeTruthy());

    fireEvent.click(screen.getByTestId("alpaca-banner-dismiss"));
    expect(localStorage.getItem(ALPACA_HINT_HIDDEN_KEY)).toBe("1");

    cleanup(); // unmount this AppShell instance — simulates a fresh app launch

    const { stores: stores2 } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores2, [venueStatus("tz-1", "tradezero")]);
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });

  it("is hidden when the engine WS is not open, even with a non-Alpaca venue configured", async () => {
    const stores = makeStores();
    const scheduler = new Scheduler(browserRaf, () => {});
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), () => {});
    const commands = {
      sendCommand: vi.fn(async () => ({ kind: "ack" as const, corrId: "c", status: "accepted" as const, value: undefined })),
      sendQuery: vi.fn(async () => []),
    };
    const demandRegistry = new DemandRegistry({ sendCommand: commands.sendCommand, onState: () => {} });
    const client = { sendCommand: vi.fn(async () => ({ status: "accepted" as const, value: seed })) };
    const workspaceStore = new WorkspaceStore(client, 1);
    render(
      <ThemeProvider><ToastProvider><OrderConfigProvider commands={commands}>
        <AppShell workspaceName="default" stores={stores} scheduler={scheduler} workspaceStore={workspaceStore}
          linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands} engineState="reconnecting" />
      </OrderConfigProvider></ToastProvider></ThemeProvider>,
    );
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    publishStatus(stores, [venueStatus("tz-1", "tradezero")]);
    expect(screen.queryByTestId("alpaca-backfill-banner")).toBeNull();
  });
});

describe("AppShell demo mode-edge orchestration (Task 13)", () => {
  // A single symbol-bearing, non-grouped Stock Info panel is enough to
  // exercise planDemoEntry's "remaining universe cycles across pinned
  // panels" rule (see demoTransition.ts) without dockview's grid math being
  // relevant here. It's a "news" panel rather than "chart" purely for that
  // reason, not for leak-avoidance: the file-wide lightweight-charts mock
  // above means real chart panels are safe to mount anywhere in this file.
  // That mock exists because of the "live(empty)->demo->live" test below
  // (line 726): AppShell.tsx's finishEntry (lines 390-391) auto-seeds the
  // Trading preset when demo mode is entered from an empty workspace, and
  // the Trading preset (presets.ts, id: "trading") contains three real
  // panelId: "chart" panels, mounted indirectly through that one test.
  const seed: Workspace = {
    name: "default",
    layoutVersion: 8,
    panels: [{ id: "info-1", panelId: "stock-info", group: null, settings: { symbol: "US.ORCL" } }],
    layout: null,
    groups: { green: "US.IBM" },
  };

  it("pending->demo: applies planDemoEntry's group/panel rewrite and auto-adds a Watchlist panel once the watchlist is non-empty (fast path)", async () => {
    const onTransitionApplied = vi.fn();
    const { stores, saved } = mount(seed, { onTransitionApplied });
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());

    // Non-empty BEFORE the mode flips: the barrier's "already non-empty,
    // proceed synchronously" branch.
    publishWatchlist(stores, ["US.MSFT", "US.GOOG", "US.AMZN", "US.TSLA", "US.NFLX"]);
    publishSessionMode(stores, "demo"); // sessionMode starts "pending" (SessionStore's seed) -> this is a tracked entry edge

    await waitFor(() => expect(saved.some((w) => w.panels.some((p) => p.panelId === "watchlist"))).toBe(true));
    const last = saved[saved.length - 1];

    // sorted universe: AMZN, GOOG, MSFT, NFLX, TSLA -> green/red/blue/yellow
    // get the first four; the 5th (TSLA) is the only "remaining" symbol, so
    // the lone pinned info panel (group: null, symbolBearing) gets it.
    expect(last.groups).toMatchObject({ green: "US.AMZN", red: "US.GOOG", blue: "US.MSFT", yellow: "US.NFLX" });
    expect(last.panels.find((p) => p.id === "info-1")?.settings.symbol).toBe("US.TSLA");
    expect(last.panels.filter((p) => p.panelId === "watchlist")).toHaveLength(1);
    expect(onTransitionApplied).toHaveBeenCalled();
  });

  it("pending->demo: waits for the watchlist barrier when it's still empty at the moment mode flips, then proceeds once symbols arrive", async () => {
    const { stores, saved } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());

    publishSessionMode(stores, "demo"); // watchlist is still empty here -> barrier subscribes and waits
    // Give any (incorrect) synchronous entry a chance to have run, then assert it hasn't.
    expect(saved.some((w) => w.panels.some((p) => p.panelId === "watchlist"))).toBe(false);

    publishWatchlist(stores, ["US.MSFT", "US.GOOG", "US.AMZN", "US.TSLA", "US.NFLX"]); // wakes the one-shot subscription
    await waitFor(() => expect(saved.some((w) => w.panels.some((p) => p.panelId === "watchlist"))).toBe(true));
    const last = saved[saved.length - 1];
    expect(last.groups).toMatchObject({ green: "US.AMZN", red: "US.GOOG", blue: "US.MSFT", yellow: "US.NFLX" });
  });

  it("live->demo captures the pre-demo workspace, and demo->live restores it verbatim (dropping the auto-added Watchlist panel)", async () => {
    const onTransitionApplied = vi.fn();
    const { stores, saved } = mount(seed, { onTransitionApplied });
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());

    publishSessionMode(stores, "live"); // pending->live: not a tracked edge, just establishes "live" as the pre-demo mode
    publishWatchlist(stores, ["US.MSFT", "US.GOOG", "US.AMZN", "US.TSLA", "US.NFLX"]);
    publishSessionMode(stores, "demo"); // live->demo: captures a snapshot of the seed workspace above

    await waitFor(() => expect(saved.some((w) => w.panels.some((p) => p.panelId === "watchlist"))).toBe(true));
    // Sanity: entry actually rewrote the pre-demo state (green flips from
    // US.IBM to US.AMZN, info-1 from US.ORCL to US.TSLA) — otherwise the
    // revert assertion below would trivially pass even if revert did nothing.
    const afterEntry = saved[saved.length - 1];
    expect(afterEntry.groups?.green).toBe("US.AMZN");
    expect(afterEntry.panels.find((p) => p.id === "info-1")?.settings.symbol).toBe("US.TSLA");

    publishSessionMode(stores, "live"); // demo->live: revert
    await waitFor(() => expect(saved[saved.length - 1].panels.some((p) => p.panelId === "watchlist")).toBe(false));
    const afterRevert = saved[saved.length - 1];
    expect(afterRevert.groups?.green).toBe("US.IBM");
    expect(afterRevert.panels.find((p) => p.id === "info-1")?.settings.symbol).toBe("US.ORCL");
    expect(afterRevert.panels).toHaveLength(1); // the auto-added Watchlist panel is gone — it wasn't in the snapshot

    expect(onTransitionApplied.mock.calls.length).toBeGreaterThanOrEqual(2); // once for entry, once for revert
  });

  it("demo->demo (e.g. a WS reconnect mid-demo) does not re-run entry, preserving a mid-demo symbol/group change", async () => {
    const { stores, saved, linkGroups } = mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());

    publishWatchlist(stores, ["US.MSFT", "US.GOOG", "US.AMZN", "US.TSLA", "US.NFLX"]);
    publishSessionMode(stores, "demo");
    await waitFor(() => expect(saved.some((w) => w.panels.some((p) => p.panelId === "watchlist"))).toBe(true));

    // Simulate a user mid-demo edit via the existing group-focus path (same
    // one exercised by the "persists a group's focused-symbol change" test
    // above), then a WS reconnect re-delivering the SAME "demo" mode.
    act(() => { linkGroups.focus("green", "US.CUSTOM"); });
    await waitFor(() => expect(saved[saved.length - 1].groups?.green).toBe("US.CUSTOM"));
    const savedCountBeforeReconnect = saved.length;

    publishSessionMode(stores, "demo"); // demo->demo: must be a pure no-op

    // No new save fires solely from the repeated mode push, and the user's
    // edit is still in place (not clobbered by a re-run of planDemoEntry).
    expect(saved.length).toBe(savedCountBeforeReconnect);
    expect(saved[saved.length - 1].groups?.green).toBe("US.CUSTOM");
  });

  it("live(empty)->demo->live: reverting back to an originally-empty workspace re-shows EmptyState instead of crashing", async () => {
    const emptySeed: Workspace = { name: "default", layoutVersion: 8, panels: [], layout: null };
    const { stores, saved } = mount(emptySeed, { workspaceName: "main" });
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    expect(screen.getByText("Empty workspace")).toBeTruthy();

    publishSessionMode(stores, "live");
    publishWatchlist(stores, ["US.MSFT", "US.GOOG", "US.AMZN", "US.TSLA", "US.NFLX"]);
    publishSessionMode(stores, "demo"); // live->demo: snapshot is the empty workspace; entry auto-adds Watchlist

    await waitFor(() => expect(saved.some((w) => w.panels.some((p) => p.panelId === "watchlist"))).toBe(true));
    expect(screen.queryByText("Empty workspace")).toBeNull(); // dockview now mounted with the Watchlist panel

    publishSessionMode(stores, "live"); // demo->live: revert restores the empty snapshot verbatim
    await waitFor(() => expect(saved[saved.length - 1].panels).toHaveLength(0));
    expect(screen.getByText("Empty workspace")).toBeTruthy();
  });
});

describe("AppShell Dockview panel constraints", () => {
  it("creates a seeded tape panel with the shared minimum width", async () => {
    const seed: Workspace = {
      name: "default",
      layoutVersion: 8,
      panels: [{ id: "tape-1", panelId: "tape", group: null, settings: {} }],
      layout: null,
    };
    const addPanel = vi.spyOn(DockviewApi.prototype, "addPanel");
    try {
      mount(seed);
      await waitFor(() => expect(addPanel).toHaveBeenCalled());
      expect(addPanel.mock.calls.map(([options]) => options)).toContainEqual(
        expect.objectContaining({ id: "tape-1", minimumWidth: TAPE_MIN_WIDTH }),
      );
    } finally {
      addPanel.mockRestore();
    }
  });

  it("normalizes a legacy tape entry before restoring Dockview JSON", async () => {
    const seed: Workspace = {
      name: "default",
      layoutVersion: 8,
      panels: [{ id: "tape-1", panelId: "tape", group: null, settings: {} }],
      layout: {
        grid: {
          root: { type: "leaf", size: 400, data: { id: "tape-1", views: ["tape-1"], activeView: "tape-1" } },
          width: 400, height: 300, orientation: "HORIZONTAL",
        },
        panels: { "tape-1": { id: "tape-1", contentComponent: "tape-1", title: "tape" } },
      },
    };
    const fromJSON = vi.spyOn(DockviewApi.prototype, "fromJSON");
    try {
      mount(seed);
      await waitFor(() => expect(fromJSON).toHaveBeenCalled());
      const restored = fromJSON.mock.calls[0][0] as { panels: Record<string, { minimumWidth?: number }> };
      expect(restored.panels["tape-1"].minimumWidth).toBe(TAPE_MIN_WIDTH);
    } finally {
      fromJSON.mockRestore();
    }
  });
});

describe("AppShell hotkey target lifecycle", () => {
  it("seeds the focused restored panel and retargets only a user activation", async () => {
    const channel = new TargetChannel();
    const hasFocus = vi.spyOn(document, "hasFocus").mockReturnValue(true);
    try {
      mount({
        name: "default",
        layoutVersion: 8,
        panels: [{ id: "news-a", panelId: "stock-info", group: "green", settings: { symbol: "US.AAPL" } }],
        layout: null,
      }, { hotkeyTargetChannel: channel });
      await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
      await waitFor(() => expect(channel.messages.some((m) => m.type === "target")).toBe(true));
      const seeded = channel.messages.filter((m): m is Extract<HotkeyTargetMessage, { type: "target" }> => m.type === "target").at(-1);
      expect(seeded?.target.panel).toBe("news-a");

      channel.messages.length = 0;
      fireEvent.click(screen.getByText("+ Add panel"));
      fireEvent.click(screen.getAllByText("Stock Info")[0]);
      await waitFor(() => expect((document.querySelector(".dv-tabs-and-actions-container") as HTMLElement).style.display).not.toBe("none"));
      expect(channel.messages.some((m) => m.type === "target")).toBe(false);
      act(() => clickTab(screen.getByTestId("panel-tab-news-a")));
      await waitFor(() => expect(channel.messages.some((m) => m.type === "target" && m.target.panel === seeded?.target.panel)).toBe(true));
    } finally {
      hasFocus.mockRestore();
    }
  });
});

describe("AppShell layout export (Task 2: ghost-panel fix)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // Regression for the ghost-panel export bug: `getWorkspace()` used to hand
  // SettingsModal the stale React `ws`/`wsRef` snapshot, which onDidLayoutChange
  // never updates — so a panel added since the last sync point landed in
  // `ws.panels` but never in `ws.layout`'s grid, and got exported as a "ghost"
  // (present as config, absent from the grid, invisible on re-import). Adding a
  // panel via the UI and then exporting proves the fix reads the LIVE dockview
  // grid (`apiRef.current.toJSON()`) instead.
  it("includes a panel added after the last sync point, both as config AND in the grid", async () => {
    const seed: Workspace = { name: "default", layoutVersion: 8, panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: {} }], layout: null };
    mount(seed);

    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getAllByText("Symbol")[0]).toBeTruthy());

    // Add a Watchlist panel via the "+ Add panel" popover, same pattern as the
    // onConfigChange describe block above — this is the panel that must not
    // end up ghosted in the export.
    fireEvent.click(screen.getByText("+ Add panel"));
    fireEvent.click(screen.getByText("Watchlist"));
    // Wait for the Watchlist panel's own render marker: proves both that
    // ws.panels includes it (config) AND that dockview's live grid has
    // actually placed it (pendingRef's deferred api.addPanel call flushed).
    await waitFor(() => expect(screen.getByPlaceholderText(/add symbol/i)).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /settings/i }));

    const createObjectURL = vi.fn<(_blob: Blob) => string>(() => "blob:mock");
    vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    fireEvent.click(screen.getByTestId("download-json"));

    const blob = createObjectURL.mock.calls[0][0];
    const text = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsText(blob);
    });
    const parsed = JSON.parse(text);

    const watchlistPanel = (parsed.layout.panels as { id: string; panelId: string }[])
      .find((p) => p.panelId === "watchlist");
    expect(watchlistPanel).toBeTruthy();

    // Internal self-consistency: every id the exported grid actually places
    // must be present among the exported panels' ids (no ghosts).
    const gridIds = collectPanelIds(parsed.layout.layout);
    const panelIds = new Set((parsed.layout.panels as { id: string }[]).map((p) => p.id));
    for (const id of gridIds) expect(panelIds.has(id)).toBe(true);

    // The concrete regression check: the freshly-added watchlist panel's id
    // must itself be in the grid, not just in `panels`.
    expect(gridIds.has(watchlistPanel!.id)).toBe(true);
  });
});
