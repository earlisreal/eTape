// Remembers the last color/width/lineStyle used per drawing tool (global, not
// per-symbol) so the next drawing of the same kind starts with the user's own
// defaults instead of the palette fallback. Mirrors DrawingStore's connect/debounced-
// flush shape, but persists a single config key rather than one per symbol.
import type { Drawing, DrawingKind } from "./model";
import { isValidDrawingStyle } from "./model";

export type ToolStyle = Pick<Drawing, "color" | "width" | "lineStyle">;

const KEY = "drawings.toolStyles";

interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }>;
}
interface Deps { commands: CommandClient }

export class DrawingToolStyleStore {
  private styles: Partial<Record<DrawingKind, ToolStyle>> = {};
  private deps: Deps | null = null;
  private loaded = false;
  private ready = false;
  private connected = false;
  private readonly listeners = new Set<() => void>();
  private pendingEdits: Partial<Record<DrawingKind, ToolStyle>> = {};
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly debounceMs = 500) {}

  isReady(): boolean { return this.ready; }
  isConnected(): boolean { return this.connected; }
  subscribe(cb: () => void): () => void { this.listeners.add(cb); return () => this.listeners.delete(cb); }

  // Wire persistence + fire the one-time load. Returns a disposer.
  connect(deps: Deps): () => void {
    this.deps = deps;
    this.connected = true;
    if (!this.loaded) {
      this.loaded = true;
      this.ready = false;
      this.notify();
      void deps.commands.sendCommand("GetConfig", { key: KEY })
        .then((ack) => {
          if (ack.status === "accepted" && ack.value && typeof ack.value === "object" && !Array.isArray(ack.value)) {
            const raw = ack.value as Record<string, unknown>;
            const next: Partial<Record<DrawingKind, ToolStyle>> = {};
            for (const [kind, style] of Object.entries(raw)) {
              if (style && typeof style === "object" && isValidDrawingStyle(style as Record<string, unknown>)) {
                next[kind as DrawingKind] = style as ToolStyle;
              }
            }
            this.styles = this.withPendingEdits(next);
          }
          this.finishHydration();
        })
        .catch(() => { this.finishHydration(); });
    }
    return () => {
      if (this.timer) { clearTimeout(this.timer); this.timer = null; }
      this.deps = null;
      this.connected = false;
    };
  }

  styleFor(kind: DrawingKind): ToolStyle {
    return this.styles[kind] ?? {};
  }

  // Merge only the defined fields of `patch` into kind's remembered style, then
  // schedule a debounced persist. Undefined fields (patch keys not present) leave
  // the previously remembered value alone.
  remember(kind: DrawingKind, patch: Partial<ToolStyle>): void {
    const prev = this.styles[kind] ?? {};
    const next: ToolStyle = { ...prev };
    if (patch.color !== undefined) next.color = patch.color;
    if (patch.width !== undefined) next.width = patch.width;
    if (patch.lineStyle !== undefined) next.lineStyle = patch.lineStyle;
    this.styles = { ...this.styles, [kind]: next };
    if (!this.ready) {
      const pending = this.pendingEdits[kind] ?? {};
      this.pendingEdits = { ...this.pendingEdits, [kind]: {
        ...pending,
        ...(patch.color === undefined ? {} : { color: patch.color }),
        ...(patch.width === undefined ? {} : { width: patch.width }),
        ...(patch.lineStyle === undefined ? {} : { lineStyle: patch.lineStyle }),
      } };
    }
    this.scheduleFlush();
  }

  private withPendingEdits(next: Partial<Record<DrawingKind, ToolStyle>>): Partial<Record<DrawingKind, ToolStyle>> {
    const merged = { ...next };
    for (const [kind, patch] of Object.entries(this.pendingEdits)) {
      if (patch) merged[kind as DrawingKind] = { ...merged[kind as DrawingKind], ...patch };
    }
    this.pendingEdits = {};
    return merged;
  }

  private finishHydration(): void {
    this.pendingEdits = {};
    this.ready = true;
    this.notify();
  }

  private notify(): void {
    for (const listener of this.listeners) listener();
  }

  private scheduleFlush(): void {
    if (this.timer) return;
    this.timer = setTimeout(() => { void this.flush(); }, this.debounceMs);
  }

  async flush(): Promise<void> {
    this.timer = null;
    if (!this.deps) return;
    await this.deps.commands.sendCommand("SetConfig", { key: KEY, value: this.styles });
  }
}
