import { useEffect, useRef, useState } from "react";
import type { DockviewPanelApi } from "dockview";
import { ErrorBoundary } from "./ErrorBoundary";
import { HoverButton } from "./controls/HoverButton";
import { PANELS, type PanelProps } from "./panels/registry";
import { PanelHeaderSlotContext } from "./panels/headerSlot";
import type { PanelConfig } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroup, LinkGroups } from "./linkGroups";
import type { DemandRegistry } from "../wire/DemandRegistry";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";
import { GroupPicker } from "./GroupPicker";
import { bareSymbol } from "./exec/orderStatus";
import { normalizeSymbol } from "./symbol";
import { useToasts } from "./Toast";
import { modalTracker } from "./modalTracker";
import { canStartTypeToLoad, reduceTypeToLoad, PRINTABLE_SYMBOL_CHAR, type TypeToLoadState } from "./typeToLoad";

const swatch = (g: LinkGroup, palette: Palette): string =>
  g === null ? "transparent" : { red: palette.linkRed, green: palette.linkGreen, blue: palette.linkBlue, yellow: palette.linkYellow }[g];

export function PanelFrame(
  { config, stores, scheduler, linkGroups, demandRegistry, commands, onConfigChange, onGroupChange, onClose, api }: {
    config: PanelConfig; stores: Stores; scheduler: Scheduler;
    linkGroups: LinkGroups; demandRegistry: DemandRegistry; commands: PanelProps["commands"];
    onConfigChange: (settings: Record<string, unknown>) => void;
    onGroupChange: (group: LinkGroup) => void;
    onClose: () => void;
    // This panel's own dockview panel API (threaded through from AppShell's
    // per-panel component factory, which dockview supplies as a prop — see
    // IDockviewPanelProps). Used ONLY to read/subscribe to isActive: dockview
    // creates each panel's React content ONCE and keeps it mounted for the
    // panel's whole life (by design — that's what keeps the chart/ladder/tape
    // canvas from remounting on drag/focus), so its underlying `component`
    // factory closure is frozen at creation time and never re-invoked with
    // fresh props on a later AppShell re-render. A plain `active: boolean` prop
    // threaded from an AppShell-level activeId (as sketched in the task plan)
    // would therefore freeze at whatever value was true when the panel was
    // created and never update again — verified empirically against this
    // dockview version. `api` itself (unlike a plain boolean) is a stable
    // object for the panel's whole life, so subscribing to its own
    // onDidActiveChange event from inside the already-mounted component works.
    api: DockviewPanelApi;
  },
): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [showPicker, setShowPicker] = useState(false);
  const [active, setActive] = useState(api.isActive);
  // Portal target for a headerControls panel's own controls (see headerSlot.ts).
  // A ref callback (not useRef) so the state — and therefore the context value
  // PanelHeaderSlotContext.Provider passes down — updates once the slot div mounts.
  const [headerSlot, setHeaderSlot] = useState<HTMLDivElement | null>(null);
  // Local group state, seeded from config.group at mount: config itself is
  // frozen inside the same per-panel factory closure described above, so
  // re-picking a group here needs its own mutable state for the swatch/symbol
  // to update immediately (mirrors TapePanel's local-state + write-through
  // onConfigChange pattern for the same underlying reason).
  const [group, setGroup] = useState<LinkGroup>(config.group);
  const [symbol, setSymbol] = useState<string | undefined>(() => linkGroups.symbolFor(group));
  const { palette } = useTheme();
  const toast = useToasts();

  // Task 13: type-to-load. `tl` drives the header's edit-affordance render;
  // `tlRef` mirrors it for the native keydown listener below so that listener
  // doesn't need to be torn down/rebuilt on every keystroke (it only depends
  // on active/modalOpen/group, which change far less often than `draft`).
  const [tl, setTl] = useState<TypeToLoadState>({ editing: false });
  const tlRef = useRef<TypeToLoadState>(tl);
  useEffect(() => { tlRef.current = tl; }, [tl]);

  // modalTracker is a module-level singleton, not a prop (see modalTracker.ts):
  // AppShell's Settings-modal open/close can't reach an already-mounted
  // PanelFrame as a live prop, same frozen-factory-closure constraint as the
  // `active`-via-`api` pattern just below.
  const [modalOpen, setModalOpen] = useState(modalTracker.isOpen());
  useEffect(() => {
    setModalOpen(modalTracker.isOpen());
    return modalTracker.subscribe(() => setModalOpen(modalTracker.isOpen()));
  }, []);

  useEffect(() => {
    setActive(api.isActive);
    const d = api.onDidActiveChange((e) => setActive(e.isActive));
    return () => d.dispose();
  }, [api]);

  // Task 13 fix (review finding, Critical): cancel any in-progress edit the
  // moment this panel stops being the active dockview panel. Without this,
  // `tl.editing` stayed true after deactivation (it was only ever cleared on
  // Enter/Escape), and the keydown listener's "already editing" branch below
  // doesn't re-check `active` — so a stale, invisible edit on a now-inactive
  // panel kept calling stopPropagation() on every matching keydown anywhere
  // in the document, silently eating real order hotkeys meant for whichever
  // panel actually was active. Returning the same `prev` reference when there
  // is nothing to cancel is a deliberate no-op bail-out (React skips the
  // re-render when a state updater returns the identical object).
  useEffect(() => {
    if (!active) setTl((prev) => (prev.editing ? { editing: false } : prev));
  }, [active]);

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      setSize({ width: Math.floor(r.width), height: Math.floor(r.height) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Keep the header's symbol live as the group's shared focus changes (same
  // subscribe pattern as NewsPanel) — this is what makes "a grouped panel
  // follows its group's symbol" actually true rather than a one-time snapshot.
  useEffect(() => {
    setSymbol(linkGroups.symbolFor(group));
    return linkGroups.subscribe(() => setSymbol(linkGroups.symbolFor(group)));
  }, [linkGroups, group]);

  const def = PANELS[config.panelId];
  const Body = def?.component;
  const props: PanelProps = { config, stores, scheduler, width: size.width, height: size.height,
    linkGroups, commands, onConfigChange, active, onGroupChange, group };

  const pinned = group === null;
  // Effective symbol shown in the header: this group's shared symbol if linked,
  // else the panel's own settings.symbol (pinned panels, or a linked group with
  // no focus published yet), rendered bare (no "US." prefix) like the exec
  // panels. A pinned panel's own settings.symbol is a snapshot from panel
  // creation — full live editing of it is Task 13's type-to-load work.
  const rawSymbol = symbol ?? (config.settings.symbol as string | undefined);
  const effectiveSymbol = rawSymbol ? bareSymbol(rawSymbol) : undefined;

  // On-demand subscription. When this panel declares a demand profile, ask the
  // engine to subscribe the effective (full, prefixed) symbol. ensure is an
  // upsert keyed by this panel's id, so a symbol switch swaps the demand with
  // no explicit release (the old symbol's subs enter the engine's ~5-min
  // hysteresis window). DemandRegistry dedupes an unchanged symbol, so this
  // fires no redundant command when the pinned commit path already ensured.
  useEffect(() => {
    if (!def?.demand || !rawSymbol) return;
    demandRegistry.ensure(config.id, rawSymbol, def.demand).then(
      (ack) => {
        if (ack.status !== "accepted") {
          toast.push({ level: "warn", text: `${bareSymbol(rawSymbol)} — ${ack.reason ?? "unavailable"}` });
        }
      },
      (err) => {
        toast.push({ level: "danger", text: `${bareSymbol(rawSymbol)} failed — ${err instanceof Error ? err.message : "unexpected error"}` });
      },
    );
  }, [def?.demand, rawSymbol, config.id, demandRegistry, toast]);

  // Release exactly once on unmount (not on symbol switch — switches upsert).
  useEffect(() => {
    if (!def?.demand) return;
    return () => demandRegistry.release(config.id);
  }, [def?.demand, config.id, demandRegistry]);

  // Task 13 fix (review finding, Minor but a literal brief requirement):
  // cancel an in-progress edit on focus loss even when the panel remains the
  // active dockview panel — e.g. the user clicks into this panel's own body
  // (a chart/ladder canvas, a button) mid-edit. Type-to-load never focuses a
  // real DOM node itself (the header span is not focusable), so any
  // `focusin` while `tl.editing` is true means focus moved away from the
  // implicit "typing into the header" mode, and the edit should end.
  useEffect(() => {
    if (!def?.symbolBearing) return;
    const onFocusIn = () => {
      setTl((prev) => (prev.editing ? { editing: false } : prev));
    };
    document.addEventListener("focusin", onFocusIn);
    return () => document.removeEventListener("focusin", onFocusIn);
  }, [def?.symbolBearing]);

  // Task 13 fix (review finding, second round: the `focusin` effect above does
  // NOT cover clicking this panel's own body while it's already the active
  // panel). Verified against the installed dockview-core
  // (node_modules/dockview-core/dist/cjs/dockview/components/panel/content.js:37):
  // `contentContainer.element` — an ANCESTOR of this component's own root div —
  // has `tabIndex = -1` and is what dockview `.focus()`s to activate a panel.
  // Nothing this component itself renders (the `hostRef` div, the chart/
  // ladder/tape canvas inside it) sets a tabIndex. So when a user clicks this
  // panel's own canvas while it is already the active panel,
  // `document.activeElement` does not change at all (the click target's
  // nearest focusable ancestor is already the focused element) — no
  // `focusin` event fires, and the effect above never runs. Left unfixed,
  // `tl.editing` (and the document-capture keydown listener that keeps
  // stopPropagation-ing matching keys because of it) survives indefinitely
  // until Enter/Escape, right up to eating a real order hotkey — the same
  // hazard class as the Critical finding this file already fixes for
  // deactivation, just narrower in scope (confined to the one active panel).
  //
  // Fix: a `pointerdown` listener scoped to `hostRef`'s own element (this
  // panel's BODY — the canvas/chart area — not the `ledger-header` div where
  // the edit affordance itself renders, which is a sibling subtree, not a
  // descendant of `hostRef`). This reacts directly to the pointer event
  // landing in the panel body, independent of whether a `focusin` also
  // fires, so it doesn't depend on the DOM's actual focus target changing.
  // Because the header is a separate subtree, a click on the edit affordance
  // itself (`panel-symbol` / `panel-symbol-hint`) never reaches this
  // listener, so it can't fight with the keydown-driven editing flow.
  useEffect(() => {
    if (!def?.symbolBearing) return;
    const el = hostRef.current;
    if (!el) return;
    const onPointerDown = () => {
      setTl((prev) => (prev.editing ? { editing: false } : prev));
    };
    el.addEventListener("pointerdown", onPointerDown);
    return () => el.removeEventListener("pointerdown", onPointerDown);
  }, [def?.symbolBearing]);

  // Task 13: type-to-load keydown capture. Deliberately attached to `document`
  // in the CAPTURE phase, not the frame-root DOM node in the (default) bubble
  // phase as the task brief originally sketched — verified against the
  // installed dockview-core: activating a panel calls
  // `contentContainer.element.focus()` on an ANCESTOR of this component's own
  // root div (dockviewGroupPanelModel.js), so a listener scoped to this node's
  // own subtree would silently miss most real keystrokes (DOM events only
  // bubble from the focused target up through ITS ancestors, never down into
  // a sibling/descendant subtree). `document`+capture instead fires for every
  // keydown regardless of focus target, and — critically for the safety
  // property below — capture-phase listeners on `document` always run before
  // ANY bubble-phase listener anywhere, including `window` (capture: root
  // down to target, target, THEN bubble: target back up to window — the
  // entire capture phase completes before bubbling starts). That ordering is
  // fixed by the DOM event-dispatch algorithm, not by listener-registration
  // order, which matters here because useHotkeys' window-level bubble
  // listener (src/chrome/exec/useHotkeys.ts) does NOT check
  // `event.defaultPrevented` and can be mounted before or after any given
  // panel depending on when it was added to the workspace. Only
  // `stopPropagation()` — called unconditionally below on every key this
  // machine captures — reliably keeps a captured keystroke from ever reaching
  // that window listener, regardless of mount order; `preventDefault()` alone
  // would not be enough (see useHotkeys.ts / OrderSettingsModal.tsx, which
  // document this exact hazard for the same reason).
  useEffect(() => {
    if (!def?.symbolBearing) return;

    const commit = async (draft: string) => {
      const sym = normalizeSymbol(draft);
      try {
        if (group !== null) {
          const r = await linkGroups.focusChecked(group, sym);
          if (!r.ok) toast.push({ level: "danger", text: `${sym} rejected — ${r.reason}` });
          // On reject the group is left completely untouched (LinkGroups.focusChecked's
          // own guarantee) and the header already reverted to the live symbol below —
          // never a half-switched group.
          return;
        }
        if (def?.demand) {
          const ack = await demandRegistry.ensure(config.id, sym, def.demand);
          if (ack.status !== "accepted") {
            toast.push({ level: "danger", text: `${sym} rejected — ${ack.reason ?? "unknown symbol"}` });
            return; // leave the prior symbol untouched — no half-loaded pinned panel
          }
        }
        // Patch, not a full-settings rewrite: `config.settings` here is frozen
        // at panel creation (dockview never re-invokes the factory), so spreading
        // it reverted every setting the panel persisted since mount — a symbol
        // commit used to silently wipe a chart's indicators, timeframe, etc.
        onConfigChange({ symbol: sym });
      } catch (err) {
        // Review finding (Important): `commit` is invoked fire-and-forget
        // (`void commit(...)`) below. A thrown/rejected promise here — e.g. a
        // transport-level failure in `linkGroups.focusChecked`'s underlying
        // `sendCommand`, not just an engine-level "blocked" ack — would
        // otherwise become a silent unhandled rejection: no toast, no
        // feedback, and the user left unsure whether their symbol change
        // went through. Surface it the same way a rejected ack is surfaced.
        toast.push({ level: "danger", text: `${sym} failed — ${err instanceof Error ? err.message : "unexpected error"}` });
      }
    };

    const onKeyDown = (e: KeyboardEvent): void => {
      const prev = tlRef.current;
      const t = document.activeElement;
      const targetIsFormField = !!t && (
        t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || (t as HTMLElement).isContentEditable
      );
      const noMods = !e.ctrlKey && !e.metaKey && !e.altKey;
      const isPrintable = PRINTABLE_SYMBOL_CHAR.test(e.key);
      const ev = { kind: "key" as const, key: e.key, ctrl: e.ctrlKey, meta: e.metaKey, alt: e.altKey };

      if (!prev.editing) {
        if (!canStartTypeToLoad({ active, symbolBearing: true, targetIsFormField, modalOpen })) return;
        if (!(noMods && isPrintable)) return; // not a start key — never touch propagation (order hotkeys, etc. stay live)
        e.preventDefault();
        e.stopPropagation();
        setTl(reduceTypeToLoad(prev, ev));
        return;
      }

      // Already editing this panel's header: only the keys the state machine
      // actually acts on are captured; everything else (Tab, arrows, …) is
      // left alone so it still does whatever it would normally do.
      if (!(noMods && (isPrintable || e.key === "Backspace" || e.key === "Enter" || e.key === "Escape"))) return;
      e.preventDefault();
      e.stopPropagation();
      if (e.key === "Enter") {
        setTl({ editing: false });
        if (prev.draft.length > 0) void commit(prev.draft); // empty draft on Enter == cancel, not a garbage symbol
        return;
      }
      setTl(reduceTypeToLoad(prev, ev));
    };

    document.addEventListener("keydown", onKeyDown, true);
    return () => document.removeEventListener("keydown", onKeyDown, true);
  }, [active, modalOpen, group, def?.symbolBearing, def?.demand, linkGroups, demandRegistry, onConfigChange, toast, config.id, config.settings]);

  const handleGroupPick = (g: LinkGroup) => {
    setGroup(g);
    onGroupChange(g);
  };

  return (
    <div className={active ? "panel-focused" : undefined} style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div className="ledger-header" style={{ position: "relative" }}>
        <HoverButton type="button" aria-label="link group" onClick={() => setShowPicker((v) => !v)}
          style={{ width: 12, height: 12, padding: 0, border: pinned ? `1.5px solid ${palette.textMuted}` : "1px solid transparent",
            borderRadius: 2, background: swatch(group, palette), cursor: "pointer", flex: "0 0 auto" }}
          hoverStyle={{ boxShadow: "inset 0 0 0 2px var(--text-muted)" }} />
        {showPicker && (
          <GroupPicker group={group} onPick={handleGroupPick} onClose={() => setShowPicker(false)} />
        )}
        {def?.symbolBearing && (
          tl.editing ? (
            <span className="mono symedit" data-testid="panel-symbol"
              style={{ fontWeight: 700, color: palette.accent, borderBottom: `2px solid ${palette.accent}` }}>
              {tl.draft}<span aria-hidden="true">▌</span>
            </span>
          ) : (
            <span className="mono" data-testid="panel-symbol" style={{ fontWeight: 700 }}>
              {effectiveSymbol ?? "—"}
            </span>
          )
        )}
        {!def?.headerControls && (
          <span className="serif" style={{ fontWeight: def?.symbolBearing ? 400 : 600, color: def?.symbolBearing ? palette.textMuted : palette.text }}>
            {def?.title ?? config.panelId}
          </span>
        )}
        {tl.editing && (
          <span className="mono" data-testid="panel-symbol-hint" style={{ fontSize: 10, color: palette.textMuted }}>
            ⏎ load · esc keep {effectiveSymbol ?? "—"}
          </span>
        )}
        {def?.headerControls ? (
          <div ref={setHeaderSlot} data-testid="panel-header-slot" style={{ flex: 1, minWidth: 0, display: "flex",
            alignItems: "center", gap: 2, overflow: "hidden", fontFamily: '"IBM Plex Sans", system-ui, sans-serif' }} />
        ) : (
          <span style={{ flex: 1 }} />
        )}
        <HoverButton type="button" aria-label="close panel" onClick={onClose}
          style={{ border: "none", background: "transparent", color: palette.textMuted, cursor: "pointer", fontSize: 13, padding: "0 2px", lineHeight: 1 }}>
          ✕
        </HoverButton>
      </div>
      <div ref={hostRef} data-testid="panel-body" style={{ flex: 1, minHeight: 0 }}>
        <ErrorBoundary label={config.panelId}>
          <PanelHeaderSlotContext.Provider value={def?.headerControls ? headerSlot : undefined}>
            {Body ? <Body {...props} /> : <div style={{ padding: 12, color: palette.textMuted }}>“{config.panelId}” — coming in a later plan</div>}
          </PanelHeaderSlotContext.Provider>
        </ErrorBoundary>
      </div>
    </div>
  );
}
