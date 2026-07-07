import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { DockviewReact, type DockviewApi, type DockviewReadyEvent } from "dockview";
import "dockview/dist/styles/dockview.css";
import { PanelFrame } from "./PanelFrame";
import type { PanelConfig, Workspace } from "./workspace";
import { WorkspaceStore } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroups } from "./linkGroups";
import { PANELS, type PanelProps } from "./panels/registry";
import { PRESETS, applyPreset } from "./presets";
import { TopBar } from "./TopBar";
import { EmptyState } from "./EmptyState";
import { Catalog } from "./Catalog";
import { useTheme } from "./ThemeProvider";
import { useToasts } from "./Toast";
import { useOrderCommands } from "./exec/useOrderCommands";
import { useHotkeys } from "./exec/useHotkeys";
import { useSoundWiring } from "../sound/useSoundWiring";
import { nextWindowName } from "./windows";

interface Props {
  workspaceName: string;
  stores: Stores;
  scheduler: Scheduler;
  workspaceStore: WorkspaceStore;
  linkGroups: LinkGroups;
  commands: PanelProps["commands"];
}

export function AppShell({ workspaceName, stores, scheduler, workspaceStore, linkGroups, commands }: Props): JSX.Element {
  const [ws, setWs] = useState<Workspace | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const { mode } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  // DockviewApi is only available once dockview mounts (i.e. once the workspace
  // has at least one panel — see the empty-state switch below); null otherwise.
  const apiRef = useRef<DockviewApi | null>(null);
  // Dockview mutations that need a NEW panel id already present in dockview's
  // `components` map (api.addPanel, applyPreset's api.fromJSON) must not run
  // synchronously in the same tick as the setWs() that adds the id — dockview's
  // React binding only refreshes its internal `createComponent` closure in a
  // child useEffect that fires after commit, so an immediate call resolves
  // against the STALE map and throws ("Only React.memo/ForwardRef/functional
  // components are accepted"). Queue the mutation here; the flush effect below
  // runs after every ws-driven re-render, which (children-before-parent effect
  // ordering) is always after DockviewReact's own components-sync effect.
  const pendingRef = useRef<Array<(api: DockviewApi) => void>>([]);
  // Lets handlers registered once (onDidRemovePanel, below) read the latest
  // workspace without capturing a stale closure over `ws`.
  const wsRef = useRef<Workspace | null>(null);
  wsRef.current = ws;
  useEffect(() => { void workspaceStore.load(workspaceName).then(setWs); }, [workspaceName, workspaceStore]);
  // Mounted once, globally — must run unconditionally, before the loading-state
  // early return below, per the Rules of Hooks.
  useHotkeys({ stores, commands, linkGroups });
  useSoundWiring(stores);
  // Same exec/armed pattern as AccountBarPanel: subscribe to the exec store so
  // the top bar's arm chip re-renders on masterArmed flips from any source
  // (this bar, the account panel, or the engine).
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const armed = stores.exec.status()?.masterArmed ?? false;
  // Flush any dockview mutations queued by addPanel/applyPresetToWorkspace once
  // dockview's components map has caught up with the latest `ws`.
  useEffect(() => {
    const api = apiRef.current;
    if (!api || pendingRef.current.length === 0) return;
    const actions = pendingRef.current;
    pendingRef.current = [];
    actions.forEach((fn) => fn(api));
  }, [ws]);
  // The dockview instance is unmounted (see the empty-state switch below) once
  // the last panel is removed; drop the now-disposed api reference so nothing
  // tries to call into it afterward.
  useEffect(() => {
    if (!ws || ws.panels.length === 0) apiRef.current = null;
  }, [ws]);
  if (!ws) return <div style={{ padding: 12 }}>loading workspace…</div>;

  // A stable per-panel onConfigChange updates ws.panels[i].settings then saves.
  const onConfigChange = (panelId: string, settings: Record<string, unknown>) => {
    const next = { ...ws, panels: ws.panels.map((p) => (p.id === panelId ? { ...p, settings } : p)) };
    setWs(next);                 // keep local state authoritative for subsequent edits
    workspaceStore.save(next);   // debounced persist (config key workspace.<name>)
  };

  // Allocate a fresh panel id and default settings per panel type, add it to
  // the workspace doc, then (if dockview is already mounted) queue the actual
  // dockview.addPanel call — see the pendingRef comment above for why this
  // can't run synchronously. If the workspace was empty, dockview isn't
  // mounted yet: it mounts fresh on the next render and its onReady seeds the
  // grid directly from the now-updated ws.panels, so no queued action is needed.
  const addPanel = (panelId: string) => {
    const def = PANELS[panelId];
    if (!def) return;
    const id = `${panelId}-${crypto.randomUUID().slice(0, 8)}`;
    const settings: Record<string, unknown> = panelId === "chart" ? { symbol: "US.AAPL", timeframe: "1m" } : {};
    const config: PanelConfig = { id, panelId, group: null, settings };
    const next = { ...ws, panels: [...ws.panels, config] };
    setWs(next);
    workspaceStore.save(next);
    if (apiRef.current) {
      pendingRef.current.push((api) => {
        if (!api.getPanel(id)) api.addPanel({ id, component: id, title: def.title });
      });
    }
    setAddOpen(false);
  };

  // Drop a panel from the workspace doc and close it in dockview (if open).
  // Reads/writes via wsRef so this stays correct whether called from a
  // freshly-rendered handler or from the once-registered onDidRemovePanel
  // listener below (which keeps ws.panels in sync when the user closes a
  // dockview tab directly).
  const removePanel = (id: string) => {
    const current = wsRef.current;
    if (!current || !current.panels.some((p) => p.id === id)) return; // already synced
    const next = { ...current, panels: current.panels.filter((p) => p.id !== id) };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
    apiRef.current?.getPanel(id)?.api.close();
  };

  // Replace the workspace with a preset's panels + layout. Confirms first if
  // the workspace isn't already empty. Same pendingRef deferral as addPanel:
  // if dockview is already mounted, applyPreset's api.fromJSON needs the new
  // panel ids present in the components map first.
  const applyPresetToWorkspace = (presetId: string) => {
    const preset = PRESETS.find((p) => p.id === presetId);
    if (!preset) return;
    const current = wsRef.current ?? ws;
    if (current.panels.length > 0 && !window.confirm("Replace the current layout with this preset?")) return;
    const { panels, layout } = preset.build();
    const next = { ...current, panels, layout };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
    if (apiRef.current) {
      pendingRef.current.push((api) => applyPreset(api, preset));
    }
    setAddOpen(false);
  };

  // "Connection" in the latency readout: focus the existing connection-status
  // panel if the workspace already has one, otherwise add it.
  const onOpenConnection = () => {
    const current = wsRef.current ?? ws;
    const existing = current.panels.find((p) => p.panelId === "connection-status");
    if (existing) apiRef.current?.getPanel(existing.id)?.focus();
    else addPanel("connection-status");
  };

  // Best-effort window tracking in localStorage so repeated "New window"
  // clicks fill the lowest free window-N gap rather than colliding.
  const onNewWindow = () => {
    let known: string[] = [];
    try {
      known = JSON.parse(localStorage.getItem("etape.windows") ?? "[]") as string[];
    } catch {
      known = [];
    }
    const names = Array.from(new Set([workspaceName, ...known]));
    const name = nextWindowName(names);
    try {
      localStorage.setItem("etape.windows", JSON.stringify([...names, name]));
    } catch {
      // best-effort only — a full/blocked localStorage shouldn't stop the new window
    }
    const url = `?workspace=${name}`;
    const w = window.open(url, "_blank");
    if (!w) toast.push({ level: "warn", text: `Popup blocked — open ${url} manually.`, sticky: true });
  };

  // Stable React keys: panels are keyed by config.id so dockview drag/resize
  // never remounts them (canvas keeps its context).
  const components = Object.fromEntries(
    ws.panels.map((p) => [
      p.id,
      () => <PanelFrame config={p} stores={stores} scheduler={scheduler}
        linkGroups={linkGroups} commands={commands}
        onConfigChange={(settings) => onConfigChange(p.id, settings)} />,
    ]),
  );

  const onReady = (event: DockviewReadyEvent) => {
    apiRef.current = event.api;
    // Restore a previously saved dockview layout if present; otherwise seed the grid
    // from the panel list (first run — the seed's `layout` is a placeholder string).
    let restored = false;
    const layout = ws.layout as { grid?: unknown } | null;
    try {
      if (layout && typeof layout.grid === "object" && layout.grid !== null) {
        event.api.fromJSON(layout as Parameters<typeof event.api.fromJSON>[0]);
        restored = true;
      }
    } catch {
      restored = false;
    }
    if (!restored) {
      ws.panels.forEach((p, i) => {
        event.api.addPanel({
          id: p.id, component: p.id, title: p.panelId,
          ...(i === 0 ? {} : { position: { direction: i % 2 ? "right" : "below" } as const }),
        });
      });
    }
    // Keep ws.panels in sync when the user closes a dockview tab directly
    // (previously only the layout was re-saved on removal, leaving the closed
    // panel's config as a zombie entry in the workspace doc).
    event.api.onDidRemovePanel((panel) => removePanel(panel.id));
    event.api.onDidLayoutChange(() => {
      // Read via wsRef, not the `ws` this closure was created with: addPanel /
      // removePanel / applyPresetToWorkspace can change ws.panels after this
      // mount-time closure was captured, and onDidLayoutChange fires on every
      // drag/resize thereafter — saving the stale `ws` here would silently
      // drop any panel added/removed since mount from the persisted doc.
      const current = wsRef.current ?? ws;
      workspaceStore.save({ ...current, layout: event.api.toJSON() });
    });
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div style={{ position: "relative" }}>
        <TopBar workspaceName={workspaceName} health={stores.health} armed={armed}
          onArmToggle={() => (armed ? oc.disarm() : oc.arm())}
          onAddPanel={() => setAddOpen((v) => !v)}
          onNewWindow={onNewWindow}
          onOpenSettings={() => {}} // TODO(T11): open the settings surface
          onOpenConnection={onOpenConnection}
        />
        {addOpen && (
          <div className="popover" style={{ top: 40, right: 160, width: 580, maxHeight: "70vh", overflow: "auto" }}>
            <Catalog onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} />
          </div>
        )}
      </div>
      <div style={{ flex: 1, minHeight: 0 }}>
        {ws.panels.length === 0 ? (
          <EmptyState onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} />
        ) : (
          <DockviewReact components={components} onReady={onReady}
            className={mode === "light" ? "dockview-theme-light" : "dockview-theme-dark"} />
        )}
      </div>
    </div>
  );
}
