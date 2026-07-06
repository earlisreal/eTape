import { useEffect, useState } from "react";
import { DockviewReact, type DockviewReadyEvent } from "dockview";
import "dockview/dist/styles/dockview.css";
import { PanelFrame } from "./PanelFrame";
import type { Workspace } from "./workspace";
import { WorkspaceStore } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroups } from "./linkGroups";
import type { PanelProps } from "./panels/registry";
import { WorkspaceHeader } from "./WorkspaceHeader";
import { useTheme } from "./ThemeProvider";
import { useHotkeys } from "./exec/useHotkeys";
import { useSoundWiring } from "../sound/useSoundWiring";

interface Props {
  workspaceName: "monitoring" | "trading";
  stores: Stores;
  scheduler: Scheduler;
  workspaceStore: WorkspaceStore;
  linkGroups: LinkGroups;
  commands: PanelProps["commands"];
}

export function AppShell({ workspaceName, stores, scheduler, workspaceStore, linkGroups, commands }: Props): JSX.Element {
  const [ws, setWs] = useState<Workspace | null>(null);
  const { mode } = useTheme();
  useEffect(() => { void workspaceStore.load(workspaceName).then(setWs); }, [workspaceName, workspaceStore]);
  // Mounted once, globally — must run unconditionally, before the loading-state
  // early return below, per the Rules of Hooks.
  useHotkeys({ stores, commands, linkGroups });
  useSoundWiring(stores);
  if (!ws) return <div style={{ padding: 12 }}>loading workspace…</div>;

  // A stable per-panel onConfigChange updates ws.panels[i].settings then saves.
  const onConfigChange = (panelId: string, settings: Record<string, unknown>) => {
    const next = { ...ws, panels: ws.panels.map((p) => (p.id === panelId ? { ...p, settings } : p)) };
    setWs(next);                 // keep local state authoritative for subsequent edits
    workspaceStore.save(next);   // debounced persist (config key workspace.<name>)
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
    event.api.onDidLayoutChange(() => {
      workspaceStore.save({ ...ws, layout: event.api.toJSON() });
    });
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <WorkspaceHeader workspaceName={workspaceName} linkGroups={linkGroups} />
      <div style={{ flex: 1, minHeight: 0 }}>
        <DockviewReact components={components} onReady={onReady}
          className={mode === "light" ? "dockview-theme-light" : "dockview-theme-dark"} />
      </div>
    </div>
  );
}
