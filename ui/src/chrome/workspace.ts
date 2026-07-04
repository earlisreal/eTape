import type { LinkGroup } from "./linkGroups";
import { SEED_WORKSPACES } from "../seeds/workspaces";

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
}

interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }>;
}

// Auto-saves the dockview layout + panel configs to the engine's config store
// (config key `workspace.<name>`), debounced. Loads the saved doc or a seed.
export class WorkspaceStore {
  private pending: Workspace | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly client: CommandClient, private readonly debounceMs = 500) {}

  async load(name: "monitoring" | "trading"): Promise<Workspace> {
    const ack = await this.client.sendCommand("GetConfig", { key: `workspace.${name}` });
    if (ack.status === "accepted" && ack.value) return ack.value as Workspace;
    return structuredClone(SEED_WORKSPACES[name]);
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
    const key = `workspace.${ws.name.toLowerCase()}`;
    await this.client.sendCommand("SetConfig", { key, value: ws });
  }
}
