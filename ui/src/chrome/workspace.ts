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

  async load(name: string, seed?: Workspace): Promise<Workspace> {
    const key = `workspace.${name}`;
    const ack = await this.client.sendCommand("GetConfig", { key });
    if (ack.status === "accepted" && ack.value && isCurrentWorkspace(ack.value)) {
      return ack.value;
    }
    if (ack.status === "accepted" && seed && typeof ack.value === "object" && ack.value !== null) {
      const legacy = ack.value as Partial<Workspace>;
      if (Array.isArray(legacy.panels) && "layout" in legacy) {
        return { ...legacy, name, layoutVersion: WORKSPACE_LAYOUT_VERSION } as Workspace;
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
