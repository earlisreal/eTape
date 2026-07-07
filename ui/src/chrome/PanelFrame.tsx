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
          <span className="mono" data-testid="panel-symbol" style={{ fontWeight: 700 }}>
            {effectiveSymbol ?? "—"}
          </span>
        )}
        <span className="serif" style={{ fontWeight: def?.symbolBearing ? 400 : 600, color: def?.symbolBearing ? palette.textMuted : palette.text }}>
          {def?.title ?? config.panelId}
        </span>
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
