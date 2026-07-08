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
    if (ack.status === "accepted" && ack.value) return ack.value as Workspace;
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
