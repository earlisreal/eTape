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
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(private readonly debounceMs = 500) {}

  // Wire persistence + fire the one-time load. Returns a disposer.
  connect(deps: Deps): () => void {
    this.deps = deps;
    if (!this.loaded) {
      this.loaded = true;
      void deps.commands.sendCommand("GetConfig", { key: KEY })
        .then((ack) => {
          if (ack.status === "accepted" && ack.value && typeof ack.value === "object") {
            const raw = ack.value as Record<string, unknown>;
            const next: Partial<Record<DrawingKind, ToolStyle>> = {};
            for (const [kind, style] of Object.entries(raw)) {
              if (style && typeof style === "object" && isValidDrawingStyle(style as Record<string, unknown>)) {
                next[kind as DrawingKind] = style as ToolStyle;
              }
            }
            this.styles = next;
          }
        })
        .catch(() => { /* load never blocks or crashes a chart */ });
    }
    return () => {
      if (this.timer) { clearTimeout(this.timer); this.timer = null; }
      this.deps = null;
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
    this.scheduleFlush();
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
