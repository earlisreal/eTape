// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, fireEvent, act } from "@testing-library/react";
import { AppShell } from "./AppShell";
import { WorkspaceStore, type Workspace } from "./workspace";
import { makeStores } from "../data/registry";
import { LinkGroups, BroadcastChannelBus } from "./linkGroups";
import { DemandRegistry } from "../wire/DemandRegistry";
import { Scheduler } from "../render/Scheduler";
import { browserRaf } from "../render/surface";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider } from "./Toast";
import { OrderConfigProvider } from "./exec/useOrderConfig";

// dockview's DockviewComponent constructor watches its container via a real
// ResizeObserver on mount, which jsdom doesn't implement.
class FakeResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = FakeResizeObserver;

// jsdom has no PointerEvent constructor; dockview's tab-activation handler
// listens for "pointerdown" and reads `.button` off the event. A plain
// MouseEvent carries the same `.button`/`.shiftKey` fields dockview reads and
// dispatches under an arbitrary type string, so this stands in for a real
// pointerdown click on a dockview tab.
function clickTab(el: Element): void {
  el.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, cancelable: true, button: 0 }));
}

function mount(seed: Workspace) {
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
      <AppShell workspaceName="default" stores={stores} scheduler={scheduler} workspaceStore={workspaceStore}
        linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands} />
    </OrderConfigProvider></ToastProvider></ThemeProvider>,
  );
  return { saved, workspaceStore };
}

describe("AppShell onConfigChange", () => {
  // Regression test for the final-review Finding 1 fix: PanelFrame's per-panel
  // component factory is captured ONCE by dockview at panel-creation time, so a
  // handler baked into that factory (onConfigChange) closes over whatever `ws`
  // existed at THAT panel's creation render — not the current one. A panel added
  // to the workspace AFTER an earlier panel was created must survive a later
  // onConfigChange call fired from that earlier panel's own (stale) closure.
  it("does not drop a later-added panel when an earlier panel's onConfigChange fires", async () => {
    const seed: Workspace = { name: "default", panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: {} }], layout: null };
    const { saved } = mount(seed);

    // Wait for the initial (pre-existing) panel's content to actually mount inside
    // dockview's portal target before doing anything else.
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getByText("Symbol")).toBeTruthy());

    // Add a second panel via the "+ Add panel" popover — this changes `ws` in
    // AppShell's React state AFTER the open-orders PanelFrame factory (and the
    // onConfigChange closure baked into it) was already created.
    fireEvent.click(screen.getByText("+ Add panel"));
    fireEvent.click(screen.getByText("News"));

    // The News panel landed as a second tab in the same dockview group and is now
    // the active one — switch back to the open-orders tab (dockview only mounts
    // the active tab's content) before touching its sort header. dockview's tab
    // activates on `pointerdown`, not `click`.
    act(() => clickTab(screen.getByText("open-orders")));
    await waitFor(() => expect(screen.getByText("Symbol")).toBeTruthy());

    // Trigger the pre-existing open-orders panel's onConfigChange path (sort-by
    // symbol persists via onConfigChange — see OpenOrdersPanel/AccountPanel).
    fireEvent.click(screen.getByText("Symbol"));

    await waitFor(() => expect(saved.length).toBeGreaterThan(0));
    const last = saved[saved.length - 1];
    const panelIds = last.panels.map((p) => p.panelId);
    // Both the original open-orders panel AND the just-added News panel must
    // survive the save — the bug silently dropped the latter.
    expect(panelIds).toContain("open-orders");
    expect(panelIds).toContain("news");
    expect(last.panels).toHaveLength(2);
  });
});

describe("AppShell single-panel tab visibility", () => {
  // A lone panel's own ledger-header already shows its title, so dockview's own tab
  // strip above it is redundant chrome — hidden until a second panel joins the group.
  it("hides the dockview tab strip for a single-panel group and shows it once a second panel joins", async () => {
    const seed: Workspace = { name: "default", panels: [{ id: "orders-1", panelId: "open-orders", group: null, settings: {} }], layout: null };
    mount(seed);
    await waitFor(() => expect(screen.queryByText(/loading workspace/i)).toBeNull());
    await waitFor(() => expect(screen.getByText("Symbol")).toBeTruthy());

    const tabStrip = () => document.querySelector(".dv-tabs-and-actions-container") as HTMLElement;
    expect(tabStrip().style.display).toBe("none");

    fireEvent.click(screen.getByText("+ Add panel"));
    fireEvent.click(screen.getByText("News"));
    await waitFor(() => expect(tabStrip().style.display).not.toBe("none"));
  });
});
