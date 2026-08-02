import type { LinkGroup } from "./linkGroups";
import type { VenueID } from "../wire/contract";

export interface PanelConfig {
  id: string;
  panelId: string;
  group: LinkGroup;
  settings: Record<string, unknown>;
}
export interface Workspace {
  name: string;
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
}

export function migrateMoversWorkspace(ws: Workspace): { workspace: Workspace; changed: boolean } {
  const movers = ws.panels.filter((p) => p.panelId === "movers");
  if (!movers.length) return { workspace: ws, changed: false };
  const hasScanner = ws.panels.some((p) => p.panelId === "scanner");
  const convertID = hasScanner ? null : movers[0].id;
  const remove = new Set(movers.map((p) => p.id).filter((id) => id !== convertID));
  const panels = ws.panels.filter((p) => !remove.has(p.id)).map((p) => p.id === convertID ? { ...p, panelId: "scanner", settings: {} } : p);
  const layout = structuredClone(ws.layout) as Record<string, unknown> | null;
  if (layout && typeof layout === "object") {
    const panelMeta = layout.panels;
    if (panelMeta && typeof panelMeta === "object") {
      for (const id of remove) delete (panelMeta as Record<string, unknown>)[id];
      if (convertID && (panelMeta as Record<string, unknown>)[convertID]) {
        const old = (panelMeta as Record<string, Record<string, unknown>>)[convertID];
        (panelMeta as Record<string, unknown>)[convertID] = { ...old, title: "Scanner" };
      }
    }
    const grid = layout.grid as { root?: unknown } | undefined;
    const prune = (node: unknown): unknown => {
      if (!node || typeof node !== "object") return node;
      const n = node as { type?: string; data?: unknown };
      if (n.type === "leaf") {
        const d = n.data as { views?: unknown; activeView?: unknown };
        if (!Array.isArray(d?.views)) return node;
        const views = d.views.filter((v): v is string => typeof v === "string" && !remove.has(v));
        d.views = views;
        if (!views.length) return null;
        if (typeof d.activeView !== "string" || !views.includes(d.activeView)) d.activeView = views[0];
        return node;
      }
      if (n.type === "branch" && Array.isArray(n.data)) {
        const children = n.data.map(prune).filter(Boolean);
        n.data = children;
        if (!children.length) return null;
        if (children.length === 1) return children[0];
      }
      return node;
    };
    if (grid) {
      grid.root = prune(grid.root);
      const leafIDs: string[] = [];
      const gather = (node: unknown): void => {
        if (!node || typeof node !== "object") return;
        const n = node as { type?: string; data?: unknown };
        if (n.type === "leaf") {
          const id = (n.data as { id?: unknown } | null)?.id;
          if (typeof id === "string") leafIDs.push(id);
        } else if (n.type === "branch" && Array.isArray(n.data)) n.data.forEach(gather);
      };
      gather(grid.root);
      const active = layout.activeGroup;
      if (typeof active !== "string" || !leafIDs.includes(active)) layout.activeGroup = leafIDs[0];
    }
  }
  return { workspace: { ...ws, panels, layout }, changed: true };
}

interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }>;
}

// Auto-saves the dockview layout + panel configs to the engine's config store
// (config key `workspace.<name>`), debounced. Loads the saved doc, or a blank
// workspace when none exists (no seed fallback — seeds are opt-in presets, Task 7/10).
export class WorkspaceStore {
  private pending: Workspace | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly client: CommandClient, private readonly debounceMs = 500) {}

  async load(name: string): Promise<Workspace> {
    const key = `workspace.${name}`;
    const ack = await this.client.sendCommand("GetConfig", { key });
    if (ack.status === "accepted" && ack.value) {
      const migrated = migrateMoversWorkspace(ack.value as Workspace);
      if (migrated.changed) await this.client.sendCommand("SetConfig", { key, value: migrated.workspace });
      return migrated.workspace;
    }
    return { name, panels: [], layout: null };
  }

  save(ws: Workspace): void {
    this.pending = ws;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => { void this.writeNow(); }, this.debounceMs);
  }

  async flush(): Promise<void> {
    if (this.timer) { clearTimeout(this.timer); this.timer = null; }
    await this.writeNow();
  }

  private async writeNow(): Promise<void> {
    if (!this.pending) return;
    const ws = this.pending;
    this.pending = null;
    this.timer = null;
    const key = `workspace.${ws.name}`;
    await this.client.sendCommand("SetConfig", { key, value: ws });
  }
}
