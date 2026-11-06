import type { LinkGroup } from "./linkGroups";
import type { VenueID } from "../wire/contract";

export interface PanelConfig {
  id: string;
  panelId: string;
  group: LinkGroup;
  settings: Record<string, unknown>;
}
export interface ScannerSyncConfig {
  enabled: boolean;
  sourceWorkspaceId?: string;
  sourcePanelId?: string;
}
export const WORKSPACE_LAYOUT_VERSION = 8;
export const MONITORING_WORKSPACE_ID = "monitoring";
export const MONITORING_WORKSPACE_NAME = "Monitoring";

export interface Workspace {
  name: string;
  layoutVersion: number;
  panels: PanelConfig[];
  layout: unknown; // dockview serialized layout JSON
  // Per-link-group focused symbol (LinkGroups.focused), persisted so a refresh
  // doesn't lose "which symbol is this group currently following" — LinkGroups
  // itself is rebuilt in-memory (empty) on every page load. Optional: absent in
  // any workspace doc saved before this field existed.
  groups?: Partial<Record<Exclude<LinkGroup, null>, string>>;
  // Per-link-group focused venue (LinkGroups.focusedVenues), persisted beside
  // `groups`. Optional: absent in any workspace doc saved before this field.
  linkVenues?: Partial<Record<Exclude<LinkGroup, null>, VenueID>>;
  scannerSync?: ScannerSyncConfig;
}

type WorkspaceChangeListener = () => void;

export function blankWorkspace(name: string): Workspace {
  return { name, layoutVersion: WORKSPACE_LAYOUT_VERSION, panels: [], layout: null };
}

export function isCurrentWorkspace(value: unknown): value is Workspace {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const workspace = value as Partial<Workspace>;
  return workspace.layoutVersion === WORKSPACE_LAYOUT_VERSION
    && typeof workspace.name === "string"
    && Array.isArray(workspace.panels)
    && "layout" in workspace;
}

export function withoutScreenerPanels(workspace: Workspace): Workspace {
  const retired = new Set(workspace.panels.filter((p) => p?.panelId === "screener").map((p) => p.id));
  if (retired.size === 0) return workspace;
  const panels = workspace.panels.filter((p) => p?.panelId !== "screener");
  const layout = panels.length > 0 ? stripRetiredLayoutPanels(workspace.layout, retired) : null;
  return { ...workspace, panels, layout };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stripRetiredLayoutPanels(raw: unknown, retired: ReadonlySet<string>): unknown {
  if (!isRecord(raw)) return raw;
  const layout = structuredClone(raw);
  const isRetired = (id: unknown) => typeof id === "string" && retired.has(id);
  const removedGroups = new Set<unknown>();
  const survivingGroups: string[] = [];

  const keepGroup = (group: unknown): boolean => {
    if (!isRecord(group) || !Array.isArray(group.views)) return true;
    const views = group.views.filter((id) => !isRetired(id));
    if (views.length < group.views.length) {
      if (views.length === 0) {
        removedGroups.add(group.id);
        return false;
      }
      group.views = views;
      if (isRetired(group.activeView)) group.activeView = views.at(-1);
      if (Array.isArray(group.tabGroups)) {
        group.tabGroups = group.tabGroups.filter((tabs: unknown) => {
          if (!isRecord(tabs) || !Array.isArray(tabs.panelIds)) return true;
          const panelIds = tabs.panelIds.filter((id) => !isRetired(id));
          tabs.panelIds = panelIds;
          return panelIds.length > 0;
        });
      }
    }
    if (views.length > 0 && typeof group.id === "string") survivingGroups.push(group.id);
    return true;
  };

  const keepNode = (node: unknown): boolean => {
    if (!isRecord(node)) return true;
    if (node.type === "leaf") return keepGroup(node.data);
    if (node.type !== "branch" || !Array.isArray(node.data) || node.data.length === 0) return true;
    const children = node.data.filter(keepNode);
    node.data = children;
    return children.length > 0;
  };

  const keepGrid = (grid: unknown): boolean => {
    if (!isRecord(grid)) return true;
    const groupsBefore = removedGroups.size;
    const keep = keepNode(grid.root);
    // A maximized node's saved child indices can shift when a split is removed.
    if (removedGroups.size !== groupsBefore) delete grid.maximizedNode;
    return keep;
  };

  if (isRecord(layout.panels)) {
    for (const id of retired) delete layout.panels[id];
  }
  if (isRecord(layout.grid) && isRecord(layout.grid.root) && !keepGrid(layout.grid)) {
    layout.grid.root = { ...layout.grid.root, type: "branch", data: [] };
  }
  for (const key of ["floatingGroups", "popoutGroups"]) {
    const groups = layout[key];
    if (Array.isArray(groups)) {
      layout[key] = groups.filter((group: unknown) =>
        !isRecord(group) || (group.grid ? keepGrid(group.grid) : keepGroup(group.data)));
    }
  }
  if (isRecord(layout.edgeGroups)) {
    for (const [position, edge] of Object.entries(layout.edgeGroups)) {
      if (isRecord(edge) && !keepGroup(edge.group)) delete layout.edgeGroups[position];
    }
  }
  if (removedGroups.has(layout.activeGroup)) layout.activeGroup = survivingGroups[0];
  return layout;
}

interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }>;
}

// Auto-saves the dockview layout + panel configs to the engine's config store
// (config key `workspace.<name>`), debounced. Loads the saved doc, or a blank
// workspace when none exists (no seed fallback — seeds are opt-in presets, Task 7/10).
export class WorkspaceStore {
  private readonly pending = new Map<string, Workspace>();
  private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();
  private readonly changeListeners = new Map<string, Set<WorkspaceChangeListener>>();
  private readonly changeChannel: BroadcastChannel | null;

  constructor(private readonly client: CommandClient, private readonly debounceMs = 500) {
    this.changeChannel = typeof window !== "undefined" && typeof BroadcastChannel !== "undefined"
      ? new BroadcastChannel("etape.workspace")
      : null;
    this.changeChannel?.addEventListener("message", (event) => {
      const workspaceId = (event.data as { workspaceId?: unknown })?.workspaceId;
      if (typeof workspaceId !== "string") return;
      this.changeListeners.get(workspaceId)?.forEach((listener) => listener());
    });
  }

  async load(name: string, seed?: Workspace): Promise<Workspace> {
    const key = `workspace.${name}`;
    const ack = await this.client.sendCommand("GetConfig", { key });
    if (ack.status === "accepted" && ack.value && isCurrentWorkspace(ack.value)) {
      return withoutScreenerPanels(ack.value);
    }
    if (ack.status === "accepted" && seed && typeof ack.value === "object" && ack.value !== null) {
      const legacy = ack.value as Partial<Workspace>;
      if (Array.isArray(legacy.panels) && "layout" in legacy) {
        return withoutScreenerPanels({ ...legacy, name, layoutVersion: WORKSPACE_LAYOUT_VERSION } as Workspace);
      }
    }
    if (ack.status === "accepted" && ack.value == null) {
      const blank = blankWorkspace(name);
      if (seed) {
        const saved = await this.client.sendCommand("SetConfig", { key, value: seed });
        if (saved.status !== "accepted") this.save(seed);
        return seed;
      }
      return blank;
    }
    const blank = blankWorkspace(name);
    if (ack.status === "accepted" && ack.value) {
      await this.client.sendCommand("SetConfig", { key, value: blank });
    }
    return blank;
  }

  save(ws: Workspace): void {
    this.pending.set(ws.name, ws);
    const timer = this.timers.get(ws.name);
    if (timer) clearTimeout(timer);
    this.timers.set(ws.name, setTimeout(() => {
      this.timers.delete(ws.name);
      void this.writeNow(ws.name);
    }, this.debounceMs));
  }

  watch(name: string, listener: WorkspaceChangeListener): () => void {
    const listeners = this.changeListeners.get(name) ?? new Set<WorkspaceChangeListener>();
    listeners.add(listener);
    this.changeListeners.set(name, listeners);
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) this.changeListeners.delete(name);
    };
  }

  notify(name: string): void {
    this.changeChannel?.postMessage({ workspaceId: name });
  }

  async flush(): Promise<void> {
    for (const timer of this.timers.values()) clearTimeout(timer);
    this.timers.clear();
    while (this.pending.size > 0) await Promise.all([...this.pending.keys()].map((name) => this.writeNow(name)));
  }

  private async writeNow(name: string): Promise<void> {
    const ws = this.pending.get(name);
    if (!ws) return;
    this.pending.delete(name);
    const key = `workspace.${ws.name}`;
    const ack = await this.client.sendCommand("SetConfig", { key, value: ws });
    if (ack.status === "accepted") this.notify(ws.name);
  }
}
