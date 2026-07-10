// @vitest-environment jsdom
import { useContext } from "react";
import { createPortal } from "react-dom";
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, within, fireEvent, act, waitFor } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { ToastProvider } from "./Toast";
import { LinkGroups } from "./linkGroups";
import { modalTracker } from "./modalTracker";
import { makeStores } from "../data/registry";
import { PanelFrame } from "./PanelFrame";
import { PANELS } from "./panels/registry";
import { PanelHeaderSlotContext, PanelHeaderActionsSlotContext } from "./panels/headerSlot";
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
function renderFrame(opts: { panelId?: string; group?: PanelConfig["group"]; settings?: Record<string, unknown>; onConfigChange?: (s: Record<string, unknown>) => void; onGroupChange?: (g: PanelConfig["group"]) => void; onClose?: () => void; api?: ReturnType<typeof fakePanelApi>; linkGroups?: LinkGroups; demandRegistry?: import("../wire/DemandRegistry").DemandRegistry } = {}) {
  const stores = makeStores();
  const linkGroups = opts.linkGroups ?? new LinkGroups(fakeBus() as never, () => {});
  // inside renderFrame(opts): default a no-op registry so existing tests keep passing.
  const demandRegistry = (opts.demandRegistry ?? {
    ensure: () => Promise.resolve({ kind: "ack", corrId: "", status: "accepted" }),
    release: () => {},
  }) as unknown as import("../wire/DemandRegistry").DemandRegistry;
  const panelId = opts.panelId ?? "news";
  // `opts.group === undefined` (not `??`): callers pass `null` explicitly to mean
  // "pinned", and `??` treats `null` as absent too, which would silently coerce a
  // pinned-panel test back to the "green" default.
  const config: PanelConfig = { id: `m-${panelId}`, panelId, group: opts.group === undefined ? "green" : opts.group, settings: opts.settings ?? {} };
  const onConfigChange = opts.onConfigChange ?? vi.fn();
  const onGroupChange = opts.onGroupChange ?? vi.fn();
  const onClose = opts.onClose ?? vi.fn();
  const api = opts.api ?? fakePanelApi(false);
  const { container, unmount } = render(
    <ThemeProvider>
      <ToastProvider>
        <PanelFrame config={config} stores={stores} scheduler={{} as never} linkGroups={linkGroups}
          demandRegistry={demandRegistry} commands={commands} onConfigChange={onConfigChange} onGroupChange={onGroupChange}
          onClose={onClose} api={api as never} />
      </ToastProvider>
    </ThemeProvider>,
  );
  return { container, unmount, onConfigChange, onGroupChange, onClose, api, linkGroups, demandRegistry };
}

// Fires a real DOM keydown on `document` — the same target PanelFrame's
// type-to-load listener is registered on (document, capture phase; see the
// Task 13 comment in PanelFrame.tsx for why frame-root/window were rejected).
function typeKey(key: string, mods: Partial<{ ctrlKey: boolean; shiftKey: boolean; metaKey: boolean; altKey: boolean }> = {}) {
  fireEvent.keyDown(document, { key, ...mods });
}

describe("PanelFrame", () => {
  it("renders the ledger header with the panel title and a symbol slot for symbol-bearing panels", () => {
    renderFrame();
    expect(screen.getByText("Stock Info")).toBeTruthy();
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

  // Task 4: close button uses HoverButton's default overlay (var(--surface)/var(--text)).
  it("hovering the close button applies the default surface/text overlay", () => {
    renderFrame();
    const btn = screen.getByLabelText("close panel") as HTMLButtonElement;
    expect(btn.style.background).toBe("transparent");
    fireEvent.mouseEnter(btn);
    expect(btn.style.background).toBe("var(--surface)");
    expect(btn.style.color).toBe("var(--text)");
    fireEvent.mouseLeave(btn);
    expect(btn.style.background).toBe("transparent");
  });

  // Task 4: the swatch's background IS the group color, so hover must use a ring
  // (not a background swap), and the existing border (pinned vs. grouped) must
  // survive hover untouched.
  it("hovering the link-group swatch shows a ring, not a background swap, and preserves its border", () => {
    renderFrame({ group: "red" }); // grouped (not pinned) — real swatch color, "1px solid transparent" border
    const btn = screen.getByLabelText("link group") as HTMLButtonElement;
    const bgBefore = btn.style.background;
    const borderBefore = btn.style.border;
    expect(bgBefore).not.toBe("transparent"); // real group color, not the pinned/no-group case
    expect(borderBefore).toBe("1px solid transparent");

    fireEvent.mouseEnter(btn);
    expect(btn.style.background).toBe(bgBefore);
    expect(btn.style.border).toBe(borderBefore);
    expect(btn.style.boxShadow).toBe("inset 0 0 0 2px var(--text-muted)");

    fireEvent.mouseLeave(btn);
    expect(btn.style.boxShadow).toBe("");
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

  // Regression: Shift+<symbol char> (e.g. Shift+1, Shift+Z) used to be captured
  // as symbol search because the modifier gate only checked ctrl/meta/alt. It
  // must be treated the same as any other modifier combo — a hotkey attempt,
  // not typing.
  it("Shift+<symbol char> never starts editing (Shift is a hotkey modifier, not typing)", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("Z", { shiftKey: true });
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

  // Regression: this is the property the Shift+digit hotkey fix depends on —
  // a modifier combo must reach useHotkeys' window bubble-phase listener, not
  // be stopPropagation()'d by type-to-load the way a captured plain key is.
  it("does not stop propagation for a Shift+<symbol char> combo, so it can still reach a window-level hotkey listener", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    const windowSpy = vi.fn();
    window.addEventListener("keydown", windowSpy);
    typeKey("Z", { shiftKey: true });
    window.removeEventListener("keydown", windowSpy);
    expect(windowSpy).toHaveBeenCalled();
  });

  // Review finding (Critical): `tl.editing` used to survive panel deactivation
  // (only Enter/Escape ever cleared it), and the keydown listener's "already
  // editing" branch never re-checked `active` — so a stale edit on a
  // now-inactive panel kept calling stopPropagation() on every matching
  // keydown anywhere in the document, silently eating real order hotkeys
  // meant for whichever panel actually was active. This exercises the real
  // DOM propagation chain (a `window` bubble-phase listener, the same phase
  // useHotkeys listens in), not just the reducer's internal state.
  it("deactivating the panel mid-edit cancels the edit and releases keydown capture (regression: stale editing state must not swallow hotkeys)", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v");
    expect(screen.getByTestId("panel-symbol").textContent).toContain("NV");

    act(() => api.setActive(false));
    // The header should already show the reverted symbol, not the stale
    // in-progress draft, the instant the panel goes inactive.
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");

    const windowSpy = vi.fn();
    window.addEventListener("keydown", windowSpy);
    typeKey("d"); // matches the "already editing" key shape this bug would have re-captured
    window.removeEventListener("keydown", windowSpy);
    expect(windowSpy).toHaveBeenCalled(); // NOT stopped — the stale-editing branch must not fire
  });

  // Review finding (Minor, but a literal brief requirement: "Cancel (Esc /
  // blur / focus loss)"). Type-to-load never focuses a real DOM node itself,
  // so any focus landing elsewhere while the panel is still active — e.g. the
  // user clicks into the panel's own body — means the implicit "typing into
  // the header" mode should end.
  it("focus moving elsewhere while the panel stays active cancels the in-progress edit", () => {
    const api = fakePanelApi(true);
    renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v");
    expect(screen.getByTestId("panel-symbol").textContent).toContain("NV");

    const other = document.createElement("button");
    document.body.appendChild(other);
    act(() => other.focus());
    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
    document.body.removeChild(other);
  });

  // Review finding (second round, Critical-class): the `focusin`-based cancel
  // above ONLY fires if document.activeElement actually changes. Verified
  // against the installed dockview-core that nothing in this component's own
  // tree (host div, canvas) sets a tabIndex, so clicking this panel's own
  // body while it's already the active dockview panel moves NO focus at all
  // (dockview already `.focus()`d an ANCESTOR — contentContainer.element,
  // tabIndex=-1 — to activate the panel) and fires no `focusin` event. This
  // test reproduces exactly that: it asserts document.activeElement is
  // unchanged by the click (proving it is NOT relying on a focusin), then
  // asserts the edit was cancelled anyway via a real window-bubble-listener
  // capture check (the same technique the deactivation regression test above
  // uses). Note the follow-up keydown uses "Backspace", not a printable
  // character: since the panel stays ACTIVE (unlike the deactivation test),
  // a printable keystroke legitimately starts a brand-new edit and WOULD be
  // captured — that's correct behavior, not the bug. "Backspace" is only
  // ever acted on by the "already editing" branch, so it proves that branch
  // is not still running against the cancelled edit.
  it("clicking the panel's own body while it stays active cancels the in-progress edit, even though no focusin fires (regression: pointerdown-scoped cancel, not focus-dependent)", () => {
    const api = fakePanelApi(true);
    const { container } = renderFrame({ api, group: null, settings: { symbol: "US.AAPL" } });
    typeKey("n"); typeKey("v");
    expect(screen.getByTestId("panel-symbol").textContent).toContain("NV");

    const before = document.activeElement;
    const body = container.querySelector('[data-testid="panel-body"]') as HTMLElement;
    expect(body).toBeTruthy();
    fireEvent.pointerDown(body);

    // Proves this test actually reproduces the "no focusin fires" scenario
    // the bug depends on, not some other DOM interaction.
    expect(document.activeElement).toBe(before);

    expect(screen.getByTestId("panel-symbol").textContent).toBe("AAPL");
    expect(screen.queryByTestId("panel-symbol-hint")).toBeNull();

    const windowSpy = vi.fn();
    window.addEventListener("keydown", windowSpy);
    typeKey("Backspace"); // only meaningful in the "already editing" branch — must not still be captured
    window.removeEventListener("keydown", windowSpy);
    expect(windowSpy).toHaveBeenCalled(); // NOT stopped — the stale-editing branch must not fire
  });

  // Review finding (Important, addendum): `commit`'s try/catch around a
  // rejecting/throwing `focusChecked` promise (a transport-level failure,
  // distinct from an already-handled `{ ok: false }` "blocked" ack) had no
  // regression test — verified by inspection only. This exercises the real
  // reject path end-to-end and confirms it surfaces a toast rather than
  // becoming a silent unhandled rejection.
  it("a rejecting/throwing focusChecked promise (transport failure) also surfaces a toast, not a silent unhandled rejection", async () => {
    const linkGroups = new LinkGroups(fakeBus() as never, () => {});
    vi.spyOn(linkGroups, "focusChecked").mockRejectedValue(new Error("network down"));
    const api = fakePanelApi(true);
    renderFrame({ api, group: "blue", linkGroups });
    typeKey("n"); typeKey("v"); typeKey("d"); typeKey("a");
    typeKey("Enter");
    await screen.findByText(/failed — network down/i);
  });
});

describe("PanelFrame — DemandRegistry wiring (Task 10)", () => {
  it("ensures the effective symbol on mount for a demand panel", async () => {
    const calls: { m: string; args: any[] }[] = [];
    const reg = {
      ensure: (...a: any[]) => { calls.push({ m: "ensure", args: a }); return Promise.resolve({ kind: "ack", corrId: "", status: "accepted" }); },
      release: (...a: any[]) => { calls.push({ m: "release", args: a }); },
    } as unknown as import("../wire/DemandRegistry").DemandRegistry;
    renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" }, demandRegistry: reg });
    await waitFor(() => expect(calls.some((c) => c.m === "ensure")).toBe(true));
    expect(calls.find((c) => c.m === "ensure")!.args).toEqual(["m-chart", "US.AAPL", "watch"]);
  });

  it("releases on unmount", () => {
    const calls: { m: string; args: any[] }[] = [];
    const reg = {
      ensure: () => Promise.resolve({ kind: "ack", corrId: "", status: "accepted" }),
      release: (...a: any[]) => { calls.push({ m: "release", args: a }); },
    } as unknown as import("../wire/DemandRegistry").DemandRegistry;
    const { unmount } = renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" }, demandRegistry: reg });
    unmount();
    expect(calls.some((c) => c.m === "release" && c.args[0] === "m-chart")).toBe(true);
  });

  it("pinned commit reverts on a blocked ensure ack", async () => {
    const ensure = vi.fn()
      .mockResolvedValueOnce({ kind: "ack", corrId: "", status: "accepted" })  // mount ensure (US.AAPL)
      .mockResolvedValueOnce({ kind: "ack", corrId: "", status: "blocked", reason: "unknown symbol US.ZZZZ" });
    const reg = { ensure, release: () => {} } as unknown as import("../wire/DemandRegistry").DemandRegistry;
    const onConfigChange = vi.fn();
    // api must start active: type-to-load's canStartTypeToLoad() gates on it,
    // matching every other typeKey-driven test in this file.
    renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" }, demandRegistry: reg, onConfigChange, api: fakePanelApi(true) });
    typeKey("z"); typeKey("z"); typeKey("z"); typeKey("z"); typeKey("Enter");
    await waitFor(() => expect(ensure).toHaveBeenCalledWith("m-chart", "US.ZZZZ", "watch"));
    expect(onConfigChange).not.toHaveBeenCalledWith(expect.objectContaining({ symbol: "US.ZZZZ" }));
  });
});

// A minimal stand-in for ChartPanel that reads PanelHeaderSlotContext directly — the
// real ChartPanel needs a working scheduler + a mocked lightweight-charts to mount at
// all (see ChartPanel.test.tsx's own setup), neither of which this file provides;
// `renderFrame`'s `scheduler: {} as never` stub makes the real component throw and get
// swallowed by ErrorBoundary, which would silently discard whatever it rendered
// (including a portaled header) before any assertion ran. This probe isolates exactly
// the contract PanelFrame promises a headerControls panel: what PanelHeaderSlotContext
// resolves to, and (once it's the live slot element) that content portaled into it
// actually lands inside PanelFrame's OWN ledger header, not inside the panel body.
function HeaderSlotProbe(): JSX.Element | null {
  const slot = useContext(PanelHeaderSlotContext);
  if (slot === undefined) return <div data-testid="probe">inline-fallback</div>;
  if (slot === null) return null; // provider present, slot div not yet mounted
  return createPortal(<div data-testid="probe">portaled</div>, slot);
}

describe("PanelFrame — headerControls slot (chart panel)", () => {
  it("suppresses its own title and portals a headerControls panel's controls into the ledger header, not the panel body", () => {
    const realChartDef = PANELS["chart"];
    PANELS["chart"] = { ...realChartDef, component: HeaderSlotProbe };
    try {
      const { container } = renderFrame({ panelId: "chart", group: null, settings: { symbol: "US.AAPL" } });
      // The symbol already identifies a headerControls panel — no separate title text.
      expect(within(container).queryByText("Chart")).toBeNull();
      const probe = within(container).getByTestId("probe");
      expect(probe.textContent).toBe("portaled");
      const header = container.querySelector(".ledger-header");
      const body = container.querySelector('[data-testid="panel-body"]');
      expect(header?.contains(probe)).toBe(true);
      expect(body?.contains(probe)).toBe(false);
    } finally {
      PANELS["chart"] = realChartDef; // PANELS is a module-level singleton — restore for every later test/file
    }
  });

  it("falls back to rendering inline when no PanelHeaderSlotContext provider is above it", () => {
    // Mirrors how ChartPanel.test.tsx renders ChartPanel directly (no PanelFrame) —
    // reproduced here via the real context default instead of a second render helper.
    const { container } = render(<PanelHeaderSlotContext.Provider value={undefined}><HeaderSlotProbe /></PanelHeaderSlotContext.Provider>);
    expect(within(container).getByTestId("probe").textContent).toBe("inline-fallback");
  });
});

// Same probe technique as HeaderSlotProbe above, for the narrower headerActions
// slot (a single icon button beside the close button — currently the tape panel's
// settings gear). Substituting the real component avoids needing a working
// scheduler/canvas to mount TapePanel, same rationale as the chart probe above.
function ActionsSlotProbe(): JSX.Element | null {
  const slot = useContext(PanelHeaderActionsSlotContext);
  if (slot === undefined) return <div data-testid="actions-probe">inline-fallback</div>;
  if (slot === null) return null; // provider present, slot div not yet mounted
  return createPortal(<div data-testid="actions-probe">portaled</div>, slot);
}

describe("PanelFrame — headerActions slot (tape panel)", () => {
  it("portals a headerActions panel's action into the ledger header, immediately before the close button, without suppressing the title", () => {
    const realTapeDef = PANELS["tape"];
    PANELS["tape"] = { ...realTapeDef, component: ActionsSlotProbe };
    try {
      const { container } = renderFrame({ panelId: "tape", group: null, settings: { symbol: "US.AAPL" } });
      expect(within(container).getByText("Time & Sales")).toBeTruthy(); // title still shown, unlike headerControls
      const probe = within(container).getByTestId("actions-probe");
      expect(probe.textContent).toBe("portaled");
      const header = container.querySelector(".ledger-header");
      const body = container.querySelector('[data-testid="panel-body"]');
      expect(header?.contains(probe)).toBe(true);
      expect(body?.contains(probe)).toBe(false);
      // Immediately before the close button, not just somewhere in the header.
      const closeBtn = screen.getByLabelText("close panel");
      const actionsSlotEl = container.querySelector('[data-testid="panel-header-actions"]');
      expect(actionsSlotEl?.nextElementSibling).toBe(closeBtn);
    } finally {
      PANELS["tape"] = realTapeDef; // PANELS is a module-level singleton — restore for every later test/file
    }
  });

  it("falls back to rendering inline when no PanelHeaderActionsSlotContext provider is above it", () => {
    const { container } = render(<PanelHeaderActionsSlotContext.Provider value={undefined}><ActionsSlotProbe /></PanelHeaderActionsSlotContext.Provider>);
    expect(within(container).getByTestId("actions-probe").textContent).toBe("inline-fallback");
  });

  it("does not render the actions slot for a panel without headerActions", () => {
    renderFrame({ panelId: "news" });
    expect(screen.queryByTestId("panel-header-actions")).toBeNull();
  });
});
