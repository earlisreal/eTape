import type { LinkGroup } from "./linkGroups";
import type { VenueID } from "../wire/contract";
import type { WorkspaceApi, WorkspaceDocumentResult } from "./workspaceApi";

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
  groups?: Partial<Record<Exclude<LinkGroup, null>, string>>;
  linkVenues?: Partial<Record<Exclude<LinkGroup, null>, VenueID>>;
  scannerSync?: ScannerSyncConfig;
}

type WorkspaceChangeListener = () => void;
type CatalogChangeListener = () => void;
type WorkspaceMessage = { kind: string; topic: string; payload: unknown };

interface WorkspaceClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }>;
  workspace?: WorkspaceApi;
  subscribe?: (topic: "workspace", listener: (message: WorkspaceMessage) => void) => () => void;
  onState?: (listener: (state: string) => void) => void;
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

// Auto-saves the Dockview document through WorkspaceService in native mode.
// The generic config commands remain only for the HTTP/browser fallback.
export class WorkspaceStore {
  private readonly pending = new Map<string, Workspace>();
  private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();
  private readonly changeListeners = new Map<string, Set<WorkspaceChangeListener>>();
  private readonly catalogListeners = new Set<CatalogChangeListener>();
  private readonly revisions = new Map<string, number>();
  private catalogRevision = 0;
  private readonly disposeStream: (() => void) | undefined;
  private connected = false;

  constructor(private readonly client: WorkspaceClient, private readonly debounceMs = 500, private readonly api = client.workspace) {
    // Subscribe before the first snapshot fetch so a concurrent mutation cannot
    // land between registration and load.
    this.disposeStream = client.subscribe?.("workspace", (message) => this.onMessage(message));
    client.onState?.((state) => {
      if (state !== "open") return;
      if (this.connected) this.refreshAll();
      this.connected = true;
    });
  }

  async load(name: string, seed?: Workspace): Promise<Workspace> {
    if (this.api) return this.loadCanonical(name, seed);
    const key = `workspace.${name}`;
    const ack = await this.client.sendCommand("GetConfig", { key });
    if (ack.status === "accepted" && ack.value && isCurrentWorkspace(ack.value)) return ack.value;
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
    if (ack.status === "accepted" && ack.value) await this.client.sendCommand("SetConfig", { key, value: blank });
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

  watchCatalog(listener: CatalogChangeListener): () => void {
    this.catalogListeners.add(listener);
    return () => this.catalogListeners.delete(listener);
  }

  // Kept for callers that want to refresh their own local projection. Native
  // peers receive the same hint through the owning Workspace Stream.
  notify(name: string): void { this.notifyWorkspace(name); }

  dispose(): void {
    this.disposeStream?.();
    for (const timer of this.timers.values()) clearTimeout(timer);
    this.timers.clear();
  }

  async flush(): Promise<void> {
    for (const timer of this.timers.values()) clearTimeout(timer);
    this.timers.clear();
    while (this.pending.size > 0) await Promise.all([...this.pending.keys()].map((name) => this.writeNow(name)));
    if (this.api) {
      const result = await this.api.flush();
      if (result.status !== "accepted") throw new Error(result.reason ?? "Could not durably flush workspace state.");
    }
  }

  private async loadCanonical(name: string, seed?: Workspace): Promise<Workspace> {
    const result = await this.api!.load(name);
    this.recordDocument(result);
    if (result.status === "accepted" && isCurrentWorkspace(result.document)) return result.document;
    if (result.status === "blocked" && seed && this.isMissing(result)) {
      const saved = await this.api!.save({ workspaceId: name, document: seed, expectedRevision: result.revision });
      this.recordDocument(saved);
      if (saved.status === "accepted") return seed;
      return this.loadCanonical(name);
    }
    if (result.status === "accepted" && result.document && seed) {
      const legacy = result.document as Partial<Workspace>;
      if (Array.isArray(legacy.panels) && "layout" in legacy) return { ...legacy, name, layoutVersion: WORKSPACE_LAYOUT_VERSION } as Workspace;
    }
    return blankWorkspace(name);
  }

  private async writeNow(name: string): Promise<void> {
    const ws = this.pending.get(name);
    if (!ws) return;
    this.pending.delete(name);
    if (!this.api) {
      const ack = await this.client.sendCommand("SetConfig", { key: `workspace.${ws.name}`, value: ws });
      if (ack.status === "accepted") this.notifyWorkspace(ws.name);
      return;
    }
    const result = await this.api.save({ workspaceId: ws.name, document: ws, expectedRevision: this.revisions.get(name) ?? 0 });
    this.recordDocument(result);
    this.notifyWorkspace(ws.name);
  }

  private onMessage(message: WorkspaceMessage): void {
    if (message.topic !== "workspace" || !message.payload || typeof message.payload !== "object") return;
    const payload = message.payload as { workspaceId?: unknown; kind?: unknown; revision?: unknown };
    const workspaceId = typeof payload.workspaceId === "string" ? payload.workspaceId : "";
    const revision = typeof payload.revision === "number" && Number.isSafeInteger(payload.revision) ? payload.revision : 0;
    if (revision <= 0) return;
    const current = workspaceId ? (this.revisions.get(workspaceId) ?? 0) : this.catalogRevision;
    if (revision <= current) return;
    const gap = current > 0 && revision > current + 1;
    if (workspaceId) this.revisions.set(workspaceId, revision);
    else this.catalogRevision = revision;
    if (workspaceId) {
      if (payload.kind === "document" || gap) this.changeListeners.get(workspaceId)?.forEach((listener) => listener());
    } else if (payload.kind === "catalog" || gap) {
      this.catalogListeners.forEach((listener) => listener());
    }
  }

  private recordDocument(result: WorkspaceDocumentResult): void {
    if (result.revision > (this.revisions.get(result.workspaceId) ?? 0)) this.revisions.set(result.workspaceId, result.revision);
  }

  private isMissing(result: WorkspaceDocumentResult): boolean {
    return result.reason === "workspace document is missing" || result.revision === 0;
  }

  private notifyWorkspace(name: string): void {
    this.changeListeners.get(name)?.forEach((listener) => listener());
  }

  private refreshAll(): void {
    this.catalogListeners.forEach((listener) => listener());
    this.changeListeners.forEach((listeners) => listeners.forEach((listener) => listener()));
  }
}
