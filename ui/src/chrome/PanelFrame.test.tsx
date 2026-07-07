// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { LinkGroups } from "./linkGroups";
import { makeStores } from "../data/registry";
import { PanelFrame } from "./PanelFrame";
import type { PanelConfig } from "./workspace";
import type { AckMsg } from "../wire/contract";

// jsdom has no ResizeObserver; PanelFrame's host-size wiring only needs observe/disconnect.
class MockResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
vi.stubGlobal("ResizeObserver", MockResizeObserver);

function fakeBus() {
  const subs = new Set<(m: unknown) => void>();
  return { post: (m: unknown) => subs.forEach((cb) => cb(m)), onMessage: (cb: (m: unknown) => void) => { subs.add(cb); return () => subs.delete(cb); }, close: () => {} };
}

const commands = {
  sendCommand: async (): Promise<AckMsg> => ({ kind: "ack", corrId: "c", status: "accepted" }),
  sendQuery: async (): Promise<unknown> => [],
};

// Minimal stand-in for dockview's per-panel DockviewPanelApi: dockview creates each
// panel's React content ONCE (at panel-creation time) and keeps it mounted for the
// panel's whole life — the surrounding factory closure AppShell hands dockview is
// never re-invoked with fresh props on a later AppShell re-render (verified against
// this dockview version), so PanelFrame can't just take a plain `active: boolean`
// prop and expect it to update. Instead it reads liveness off this stable, per-panel
// api object's onDidActiveChange event — this fake reproduces just that surface.
function fakePanelApi(initial = false) {
  const listeners = new Set<(e: { isActive: boolean }) => void>();
  let isActive = initial;
  return {
    get isActive() { return isActive; },
    onDidActiveChange(cb: (e: { isActive: boolean }) => void) {
      listeners.add(cb);
      return { dispose: () => listeners.delete(cb) };
    },
    setActive(v: boolean) { isActive = v; listeners.forEach((cb) => cb({ isActive: v })); },
  };
}

// "news" is symbol-bearing but renders no <canvas> (unlike chart/ladder/tape), so this
// test stays in vitest's default (threads) pool per vitest.config.ts's poolMatchGlobs
// comment about node-canvas being unsafe to load into more than one worker.
function renderFrame(opts: { group?: PanelConfig["group"]; onConfigChange?: (s: Record<string, unknown>) => void; onGroupChange?: (g: PanelConfig["group"]) => void; onClose?: () => void; api?: ReturnType<typeof fakePanelApi> } = {}) {
  const stores = makeStores();
  const linkGroups = new LinkGroups(fakeBus() as never, () => {});
  const config: PanelConfig = { id: "m-news", panelId: "news", group: opts.group ?? "green", settings: {} };
  const onConfigChange = opts.onConfigChange ?? vi.fn();
  const onGroupChange = opts.onGroupChange ?? vi.fn();
  const onClose = opts.onClose ?? vi.fn();
  const api = opts.api ?? fakePanelApi(false);
  const { container } = render(
    <ThemeProvider>
      <PanelFrame config={config} stores={stores} scheduler={{} as never} linkGroups={linkGroups}
        commands={commands} onConfigChange={onConfigChange} onGroupChange={onGroupChange}
        onClose={onClose} api={api as never} />
    </ThemeProvider>,
  );
  return { container, onConfigChange, onGroupChange, onClose, api };
}

describe("PanelFrame", () => {
  it("renders the ledger header with the panel title and a symbol slot for symbol-bearing panels", () => {
    renderFrame();
    expect(screen.getByText("News")).toBeTruthy();
    expect(screen.getByTestId("panel-symbol")).toBeTruthy();
  });

  it("opens the group picker from the swatch button and reports a pick via onGroupChange", () => {
    const { onGroupChange } = renderFrame({ group: "blue" });
    fireEvent.click(screen.getByLabelText("link group"));
    expect(screen.getByText(/red group/i)).toBeTruthy();
    fireEvent.click(screen.getByText(/green group/i));
    expect(onGroupChange).toHaveBeenCalledWith("green");
  });

  it("applies panel-focused only when the panel's own dockview api reports isActive, and stays live across activation changes without remounting", () => {
    const api = fakePanelApi(false);
    const { container } = renderFrame({ api });
    expect(container.querySelector(".panel-focused")).toBeNull();

    act(() => api.setActive(true));
    expect(container.querySelector(".panel-focused")).not.toBeNull();

    act(() => api.setActive(false));
    expect(container.querySelector(".panel-focused")).toBeNull();
  });

  it("starts focused when the panel's api already reports isActive at mount", () => {
    const { container } = renderFrame({ api: fakePanelApi(true) });
    expect(container.querySelector(".panel-focused")).not.toBeNull();
  });

  it("wires the close button to onClose", () => {
    const { onClose } = renderFrame();
    fireEvent.click(screen.getByLabelText("close panel"));
    expect(onClose).toHaveBeenCalled();
  });
});
