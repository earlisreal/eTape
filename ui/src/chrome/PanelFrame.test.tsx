// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, act, waitFor } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider } from "./Toast";
import { LinkGroups } from "./linkGroups";
import { modalTracker } from "./modalTracker";
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
function renderFrame(opts: { group?: PanelConfig["group"]; settings?: Record<string, unknown>; onConfigChange?: (s: Record<string, unknown>) => void; onGroupChange?: (g: PanelConfig["group"]) => void; onClose?: () => void; api?: ReturnType<typeof fakePanelApi>; linkGroups?: LinkGroups } = {}) {
  const stores = makeStores();
  const linkGroups = opts.linkGroups ?? new LinkGroups(fakeBus() as never, () => {});
  // `opts.group === undefined` (not `??`): callers pass `null` explicitly to mean
  // "pinned", and `??` treats `null` as absent too, which would silently coerce a
  // pinned-panel test back to the "green" default.
  const config: PanelConfig = { id: "m-news", panelId: "news", group: opts.group === undefined ? "green" : opts.group, settings: opts.settings ?? {} };
  const onConfigChange = opts.onConfigChange ?? vi.fn();
  const onGroupChange = opts.onGroupChange ?? vi.fn();
  const onClose = opts.onClose ?? vi.fn();
  const api = opts.api ?? fakePanelApi(false);
  const { container } = render(
    <ThemeProvider>
      <ToastProvider>
        <PanelFrame config={config} stores={stores} scheduler={{} as never} linkGroups={linkGroups}
          commands={commands} onConfigChange={onConfigChange} onGroupChange={onGroupChange}
          onClose={onClose} api={api as never} />
      </ToastProvider>
    </ThemeProvider>,
  );
  return { container, onConfigChange, onGroupChange, onClose, api, linkGroups };
}

// Fires a real DOM keydown on `document` — the same target PanelFrame's
// type-to-load listener is registered on (document, capture phase; see the
// Task 13 comment in PanelFrame.tsx for why frame-root/window were rejected).
function typeKey(key: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean }> = {}) {
  fireEvent.keyDown(document, { key, ...mods });
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

describe("PanelFrame — type-to-load (Task 13)", () => {
  // modalTracker is a module-level singleton (see modalTracker.ts) so it must
  // not leak state between tests. Wrapped in act(): this runs before RTL's own
  // unmount cleanup, so the still-mounted PanelFrame's subscription callback
  // (setModalOpen) would otherwise fire outside of act().
  afterEach(() => act(() => modalTracker.setOpen(false)));

  it("typing a printable sequence on the active symbol-bearing panel shows the uppercased draft in the edit slot", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v"); typeKey("d"); typeKey("a");
    expect(screen.getByTestId("panel-symbol").textContent).toContain("NVDA");
  });

  it("Enter on a grouped panel commits via linkGroups.focusChecked using the normalized symbol, and the group follows on accept", async () => {
    const linkGroups = new LinkGroups(fakeBus() as never, () => {});
    const spy = vi.spyOn(linkGroups, "focusChecked").mockResolvedValue({ ok: true });
    const api = fakePanelApi(true);
    renderFrame({ api, group: "blue", linkGroups });
    typeKey("n"); typeKey("v"); typeKey("d"); typeKey("a");
    typeKey("Enter");
    await waitFor(() => expect(spy).toHaveBeenCalledWith("blue", "US.NVDA"));
  });

  it("a rejecting focusChecked pushes a toast and leaves the group/header symbol unchanged — never a half-switched group", async () => {
    const linkGroups = new LinkGroups(fakeBus() as never, () => {});
    linkGroups.focus("blue", "US.AAPL"); // pre-existing group focus
    vi.spyOn(linkGroups, "focusChecked").mockResolvedValue({ ok: false, reason: "unknown symbol" });
    const api = fakePanelApi(true);
    renderFrame({ api, group: "blue", linkGroups });
    typeKey("b"); typeKey("o"); typeKey("g"); typeKey("u"); typeKey("s");
    typeKey("Enter");
    await screen.findByText(/rejected — unknown symbol/i);
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL"); // reverted, group untouched
    expect(linkGroups.symbolFor("blue")).toBe("US.AAPL");
  });

  it("Enter on a pinned panel commits via onConfigChange with the normalized symbol", async () => {
    const onConfigChange = vi.fn();
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, onConfigChange, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v"); typeKey("d"); typeKey("a");
    typeKey("Enter");
    await waitFor(() => expect(onConfigChange).toHaveBeenCalledWith({ symbol: "US.NVDA" }));
  });

  it("Escape cancels the edit and restores the previous header symbol", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v");
    expect(screen.getByTestId("panel-symbol").textContent).toContain("NV");
    typeKey("Escape");
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
  });

  it("Backspace trims the draft without exiting edit mode", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v"); typeKey("Backspace");
    expect(screen.getByTestId("panel-symbol").textContent).toContain("N");
    expect(screen.getByTestId("panel-symbol-hint")).toBeTruthy(); // still editing
  });

  it("a keystroke on an inactive panel does not start editing", () => {
    const api = fakePanelApi(false);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n");
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
  });

  it("a keystroke with a modifier held never starts editing (order hotkeys stay live)", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n", { ctrlKey: true });
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
  });

  it("does not start editing when a real form field has focus", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    input.focus();
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n");
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
    document.body.removeChild(input);
  });

  it("does not start editing while a modal is open", () => {
    modalTracker.setOpen(true);
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n");
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
  });

  it("stops propagation to window-level listeners while capturing a key — the useHotkeys hazard", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    const windowSpy = vi.fn();
    window.addEventListener("keydown", windowSpy);
    typeKey("n");
    window.removeEventListener("keydown", windowSpy);
    expect(windowSpy).not.toHaveBeenCalled();
  });

  it("does not swallow keys it doesn't capture (sanity check the stopPropagation above is selective, not blanket)", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    const windowSpy = vi.fn();
    window.addEventListener("keydown", windowSpy);
    typeKey("ArrowUp");
    window.removeEventListener("keydown", windowSpy);
    expect(windowSpy).toHaveBeenCalled();
  });
});
