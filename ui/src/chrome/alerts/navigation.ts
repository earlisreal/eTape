import type { LinkGroup } from "../linkGroups";
import type { PanelConfig } from "../workspace";

const PREFIX = "etape.alert-panels.";
type Color = Exclude<LinkGroup, null>;
interface Panel { id: string; group: LinkGroup; used: number }
interface Snapshot { workspace: string; panels: Panel[]; ready: boolean }
export interface AlertDestination { group: Color | null; unavailable: boolean }

function color(value: unknown): Color | null {
  return value === "red" || value === "green" || value === "blue" || value === "yellow" ? value : null;
}

// Storage supplies current panel colors; Web Locks exclude closed/crashed owners.
export class AlertNavigation {
  private state: Snapshot;
  private storage?: Storage;
  private locks?: Pick<LockManager, "request" | "query">;
  private available = false;
  private run = 0;
  private ready: Promise<void> = Promise.resolve();
  private readonly subscribers = new Set<() => void>();

  constructor(
    private readonly owner: string,
    workspace: string,
    private readonly environment?: { storage: Storage; locks: Pick<LockManager, "request" | "query"> },
  ) {
    this.state = { workspace, panels: [], ready: false };
  }

  start(): () => void {
    const run = ++this.run;
    let stopped = false;
    let release: (() => void) | undefined;
    const changed = (event: StorageEvent) => {
      if (event.key === null || event.key.startsWith(PREFIX)) this.notify();
    };
    const stop = () => {
      if (stopped) return;
      stopped = true;
      this.available = false;
      window.removeEventListener("storage", changed);
      window.removeEventListener("pagehide", stop);
      release?.();
      this.notify();
    };
    try {
      this.storage = this.environment?.storage ?? localStorage;
      this.locks = this.environment?.locks ?? navigator.locks;
      if (!this.locks) return stop;
      this.available = true;
      this.write();
      this.ready = new Promise<void>((resolve) => {
        void this.locks!.request(PREFIX + this.owner, async () => {
          if (stopped) { resolve(); return; }
          const held = new Promise<void>((done) => { release = done; });
          resolve();
          this.notify();
          await held;
        }).catch(() => {
          if (!stopped) { this.available = false; this.notify(); }
          resolve();
        }).finally(() => {
          // Notify peers after releasing the lock so a closing owner cannot look unresolved.
          if (stopped && this.run === run) {
            try { this.storage?.removeItem(PREFIX + this.owner); } catch { /* Owner liveness still excludes stale data. */ }
          }
        });
      });
      window.addEventListener("storage", changed);
      window.addEventListener("pagehide", stop);
    } catch {
      this.available = false;
    }
    return stop;
  }

  sync(panels: readonly PanelConfig[] | null): void {
    const previous = new Map(this.state.panels.map((panel) => [panel.id, panel.used]));
    const next: Snapshot = { workspace: this.state.workspace, ready: panels !== null,
      panels: (panels ?? []).filter((panel) => panel.panelId === "alerts")
        .map((panel) => ({ id: panel.id, group: panel.group, used: previous.get(panel.id) ?? 0 })) };
    if (JSON.stringify(next) === JSON.stringify(this.state)) return;
    this.state = next;
    this.write();
  }

  use(panelId: string): void {
    const panel = this.state.panels.find((entry) => entry.id === panelId);
    if (!panel?.group) return;
    try {
      // A shared logical clock orders user interactions without wall-clock jumps.
      const key = PREFIX + "clock";
      const revision = Number(this.storage?.getItem(key) ?? 0);
      const used = Math.max(Number.isSafeInteger(revision) ? revision : 0, panel.used) + 1;
      this.storage?.setItem(key, String(used));
      panel.used = used;
      this.write();
    } catch {
      this.available = false;
      this.notify();
    }
  }

  subscribe(callback: () => void): () => void {
    this.subscribers.add(callback);
    return () => this.subscribers.delete(callback);
  }

  async destination(sourceGroup?: string): Promise<AlertDestination> {
    const unavailable: AlertDestination = { group: null, unavailable: true };
    try {
      await this.ready;
      if (!this.available || !this.locks || !this.storage) return unavailable;
      const held = (await this.locks.query()).held ?? [];
      if (!this.available || !held.some((lock) => lock.name === PREFIX + this.owner)) return unavailable;
      const candidates: Array<{ group: Color; used: number; key: string }> = [];
      for (const lock of held) {
        if (!lock.name?.startsWith(PREFIX)) continue;
        const snapshot: Snapshot | null = JSON.parse(this.storage.getItem(lock.name) ?? "null");
        if (!snapshot?.ready || typeof snapshot.workspace !== "string" || !Array.isArray(snapshot.panels)) return unavailable;
        for (const panel of snapshot.panels) {
          if (typeof panel.id !== "string" || !Number.isSafeInteger(panel.used) || panel.used < 0
            || (panel.group !== null && !color(panel.group))) return unavailable;
          if (panel.group) candidates.push({ group: panel.group, used: panel.used,
            key: `${snapshot.workspace}\0${panel.id}\0${lock.name}` });
        }
      }
      candidates.sort((a, b) => b.used - a.used || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
      return { group: candidates[0]?.group ?? color(sourceGroup), unavailable: false };
    } catch {
      return unavailable;
    }
  }

  private write(): void {
    if (!this.available) return;
    try { this.storage?.setItem(PREFIX + this.owner, JSON.stringify(this.state)); }
    catch { this.available = false; }
    this.notify();
  }

  private notify(): void { this.subscribers.forEach((callback) => callback()); }
}
