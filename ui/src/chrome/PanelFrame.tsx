import { useEffect, useRef, useState } from "react";
import type { DockviewPanelApi } from "dockview";
import { ErrorBoundary } from "./ErrorBoundary";
import { PANELS, type PanelProps } from "./panels/registry";
import type { PanelConfig } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroup, LinkGroups } from "./linkGroups";
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
  { config, stores, scheduler, linkGroups, commands, onConfigChange, onGroupChange, onClose, api }: {
    config: PanelConfig; stores: Stores; scheduler: Scheduler;
    linkGroups: LinkGroups; commands: PanelProps["commands"];
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
    linkGroups, commands, onConfigChange, active, onGroupChange };

  const pinned = group === null;
  // Effective symbol shown in the header: this group's shared symbol if linked,
  // else the panel's own settings.symbol (pinned panels, or a linked group with
  // no focus published yet), rendered bare (no "US." prefix) like the exec
  // panels. A pinned panel's own settings.symbol is a snapshot from panel
  // creation — full live editing of it is Task 13's type-to-load work.
  const rawSymbol = symbol ?? (config.settings.symbol as string | undefined);
  const effectiveSymbol = rawSymbol ? bareSymbol(rawSymbol) : undefined;

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
      if (group !== null) {
        const r = await linkGroups.focusChecked(group, sym);
        if (!r.ok) toast.push({ level: "danger", text: `${sym} rejected — ${r.reason}` });
        // On reject the group is left completely untouched (LinkGroups.focusChecked's
        // own guarantee) and the header already reverted to the live symbol below —
        // never a half-switched group.
        return;
      }
      onConfigChange({ ...config.settings, symbol: sym });
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
  }, [active, modalOpen, group, def?.symbolBearing, linkGroups, onConfigChange, toast, config.settings]);

  const handleGroupPick = (g: LinkGroup) => {
    setGroup(g);
    onGroupChange(g);
  };

  return (
    <div className={active ? "panel-focused" : undefined} style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div className="ledger-header" style={{ position: "relative" }}>
        <button type="button" aria-label="link group" onClick={() => setShowPicker((v) => !v)}
          style={{ width: 12, height: 12, padding: 0, border: pinned ? `1.5px solid ${palette.textMuted}` : "1px solid transparent",
            borderRadius: 2, background: swatch(group, palette), cursor: "pointer", flex: "0 0 auto" }} />
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
        <span className="serif" style={{ fontWeight: def?.symbolBearing ? 400 : 600, color: def?.symbolBearing ? palette.textMuted : palette.text }}>
          {def?.title ?? config.panelId}
        </span>
        {tl.editing && (
          <span className="mono" data-testid="panel-symbol-hint" style={{ fontSize: 10, color: palette.textMuted }}>
            ⏎ load · esc keep {effectiveSymbol ?? "—"}
          </span>
        )}
        <span style={{ flex: 1 }} />
        <button type="button" aria-label="close panel" onClick={onClose}
          style={{ border: "none", background: "transparent", color: palette.textMuted, cursor: "pointer", fontSize: 13, padding: "0 2px", lineHeight: 1 }}>
          ✕
        </button>
      </div>
      <div ref={hostRef} style={{ flex: 1, minHeight: 0 }}>
        <ErrorBoundary label={config.panelId}>
          {Body ? <Body {...props} /> : <div style={{ padding: 12, color: palette.textMuted }}>“{config.panelId}” — coming in a later plan</div>}
        </ErrorBoundary>
      </div>
    </div>
  );
}
