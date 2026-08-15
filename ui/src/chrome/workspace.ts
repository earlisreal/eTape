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
