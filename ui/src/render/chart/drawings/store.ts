import { PaintStore } from "../../../data/store";
import type { Drawing } from "./model";
import { validateDrawings } from "./model";

export interface DrawingMsg {
  op: "upsert" | "remove" | "clear";
  symbol: string;
  drawing?: Drawing; // op "upsert"
  id?: string;       // op "remove"
}

export interface DrawingBus {
  post(msg: DrawingMsg): void;
  onMessage(cb: (msg: DrawingMsg) => void): () => void;
  close(): void;
}

export class BroadcastChannelDrawingBus implements DrawingBus {
  private ch = new BroadcastChannel("etape.drawings");
  post(msg: DrawingMsg): void { this.ch.postMessage(msg); }
  onMessage(cb: (msg: DrawingMsg) => void): () => void {
    const handler = (e: MessageEvent) => cb(e.data as DrawingMsg);
    this.ch.addEventListener("message", handler);
    return () => this.ch.removeEventListener("message", handler);
  }
  close(): void { this.ch.close(); }
}

// Minimal command surface (mirrors WorkspaceStore's local CommandClient).
interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }>;
}

interface Deps { commands: CommandClient; bus: DrawingBus; onError: (reason: string) => void }

const keyFor = (symbol: string) => `drawings.${symbol}`;

// Symbol-keyed store of chart drawings, shared across every panel in a window
// (one instance from makeStores()). Extends PaintStore so N chart panels each
// track their own last-seen revision via getRev() without starving each other.
//
// Primary index is byId (so remove(id) is O(1) and can find the owning symbol);
// bySymbol tracks id membership + insertion order per symbol.
//
// Cross-window sync + persistence mirrors linkGroups.ts's echo-guard: local
// mutations (upsert/remove/clearSymbol) publish on the bus and schedule a
// debounced KV write; mutations arriving via the bus (applyRemote) or via load
// (ensureLoaded) apply through the same internal primitives but never
// re-publish or re-persist (single-writer per symbol per window).
export class DrawingStore extends PaintStore {
  private readonly byId = new Map<string, Drawing>();
  private readonly bySymbol = new Map<string, Set<string>>();

  private deps: Deps | null = null;
  private readonly loaded = new Set<string>();
  private readonly dirtySymbols = new Set<string>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private offBus: (() => void) | null = null;

  constructor(private readonly debounceMs = 500) {
    super();
  }

  forSymbol(symbol: string): Drawing[] {
    const ids = this.bySymbol.get(symbol);
    if (!ids) return [];
    const out: Drawing[] = [];
    for (const id of ids) {
      const d = this.byId.get(id);
      if (d) out.push(d);
    }
    return out;
  }

  // Wire cross-window sync + persistence + error surfacing. Returns a disposer.
  connect(deps: Deps): () => void {
    this.deps = deps;
    this.offBus = deps.bus.onMessage((m) => this.applyRemote(m));
    return () => {
      this.offBus?.();
      this.offBus = null;
      if (this.timer) { clearTimeout(this.timer); this.timer = null; }
      this.deps = null;
    };
  }

  // Fire GetConfig once per symbol per session. Absent/malformed → empty.
  ensureLoaded(symbol: string): void {
    if (this.loaded.has(symbol) || !this.deps) return;
    this.loaded.add(symbol);
    const commands = this.deps.commands;
    void commands.sendCommand("GetConfig", { key: keyFor(symbol) })
      .then((ack) => {
        if (ack.status === "accepted") {
          for (const d of validateDrawings(ack.value)) this.setLocal(d); // load path: no publish/persist
        }
      })
      .catch(() => { /* load never blocks or crashes a chart */ });
  }

  async flush(): Promise<void> {
    this.timer = null;
    const deps = this.deps;
    if (!deps) { this.dirtySymbols.clear(); return; }
    const symbols = [...this.dirtySymbols];
    this.dirtySymbols.clear();
    for (const symbol of symbols) {
      const ack = await deps.commands.sendCommand("SetConfig", { key: keyFor(symbol), value: this.forSymbol(symbol) });
      if (ack.status !== "accepted") {
        deps.onError(ack.reason ?? `Failed to save drawings for ${symbol}`);
        this.dirtySymbols.add(symbol); // retry on the next flush
      }
    }
  }

  private scheduleFlush(symbol: string): void {
    this.dirtySymbols.add(symbol);
    if (this.timer) return;
    this.timer = setTimeout(() => { void this.flush(); }, this.debounceMs);
  }

  private applyRemote(m: DrawingMsg): void {
    if (m.op === "upsert" && m.drawing) this.setLocal(m.drawing);
    else if (m.op === "remove" && m.id) { if (this.deleteLocal(m.id)) this.markDirty(); }
    else if (m.op === "clear") { this.clearLocal(m.symbol); this.markDirty(); }
  }

  upsert(d: Drawing): void {
    this.setLocal(d);
    if (this.deps) {
      this.deps.bus.post({ op: "upsert", symbol: d.symbol, drawing: d });
      this.scheduleFlush(d.symbol);
    }
  }

  remove(id: string): void {
    const d = this.byId.get(id);
    if (!d) return;
    this.deleteLocal(id);
    this.markDirty();
    if (this.deps) {
      this.deps.bus.post({ op: "remove", symbol: d.symbol, id });
      this.scheduleFlush(d.symbol);
    }
  }

  clearSymbol(symbol: string): void {
    this.clearLocal(symbol);
    this.markDirty();
    if (this.deps) {
      this.deps.bus.post({ op: "clear", symbol });
      this.scheduleFlush(symbol);
    }
  }

  // --- internal mutation primitives (Task 4 reuses these for remote apply) ---

  protected setLocal(d: Drawing): void {
    this.byId.set(d.id, d);
    let ids = this.bySymbol.get(d.symbol);
    if (!ids) {
      ids = new Set<string>();
      this.bySymbol.set(d.symbol, ids);
    }
    ids.add(d.id);
    this.markDirty();
  }

  protected deleteLocal(id: string): boolean {
    const d = this.byId.get(id);
    if (!d) return false;
    this.byId.delete(id);
    this.bySymbol.get(d.symbol)?.delete(id);
    return true;
  }

  protected clearLocal(symbol: string): void {
    const ids = this.bySymbol.get(symbol);
    if (!ids) return;
    for (const id of ids) this.byId.delete(id);
    this.bySymbol.delete(symbol);
  }
}
