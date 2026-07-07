import type { Bar } from "../../../gen/wsmsg";
import { anchorCount, type Anchor, type Drawing, type DrawingKind } from "./model";
import type { DrawingStore } from "./store";
import type { DrawingsPrimitiveHandle } from "./primitive";
import { hitTest, snapToLevels, timeToLogical, type Px } from "./geometry";

export type Tool = "select" | "hline" | "hray" | "trendline" | "ray" | "rect" | "measure";

export interface DrawingFacade {
  logicalToCoordinate(logical: number): number | null;
  coordinateToLogical(x: number): number | null;
  coordinateToPrice(y: number): number | null;
  priceToCoordinate(price: number): number | null;
  setPanZoomEnabled(on: boolean): void;
}

export interface InteractionHost {
  addEventListener(type: string, cb: (e: any) => void): void;
  removeEventListener(type: string, cb: (e: any) => void): void;
  getBoundingClientRect(): { left: number; top: number; width: number; height: number };
  focus(): void;
  clientWidth: number;
  tabIndex: number;
  style: { outline: string };
}

export interface DrawingContext {
  symbol(): string;
  bars(): readonly Bar[];
  timeframeMs(): number;
  magnet(): boolean;
}

type PointerLike = { clientX: number; clientY: number };
type KeyLike = { key: string; preventDefault?: () => void };

type Gesture =
  | { kind: "none" }
  | { kind: "placing"; anchor0: Anchor }
  | { kind: "measuring"; from: Anchor }
  | { kind: "handleDrag"; id: string; index: number }
  | { kind: "bodyDrag"; id: string; downLogical: number; downPrice: number; orig: Anchor[] };

const MAGNET_PX = 6;

export class DrawingInteraction {
  private tool: Tool = "select";
  private gesture: Gesture = { kind: "none" };
  private selectionId: string | null = null;
  private readonly newId: () => string;
  private readonly onToolChange: ((t: Tool) => void) | undefined;
  private readonly listeners: [string, (e: any) => void][] = [];

  constructor(
    private readonly host: InteractionHost,
    private readonly facade: DrawingFacade,
    private readonly primitive: DrawingsPrimitiveHandle,
    private readonly store: DrawingStore,
    private readonly ctx: DrawingContext,
    opts?: { newId?: () => string; onToolChange?: (t: Tool) => void },
  ) {
    this.newId = opts?.newId ?? (() => crypto.randomUUID());
    this.onToolChange = opts?.onToolChange;
    host.tabIndex = host.tabIndex >= 0 ? host.tabIndex : 0;
    host.style.outline = "none";
    const on = (t: string, cb: (e: any) => void) => { host.addEventListener(t, cb); this.listeners.push([t, cb]); };
    on("pointerdown", (e) => this.onPointerDown(e));
    on("pointermove", (e) => this.onPointerMove(e));
    on("pointerup", (e) => this.onPointerUp(e));
    on("keydown", (e) => this.onKeyDown(e));
  }

  setTool(tool: Tool): void {
    this.cancelGesture();
    this.tool = tool;
    if (tool !== "select") { this.selectionId = null; this.primitive.setSelection(null); }
    this.applyPanZoomLock();
    this.primitive.requestUpdate();
  }

  onSymbolChanged(): void {
    this.cancelGesture();
    this.selectionId = null;
    this.primitive.setSelection(null);
    // A symbol switch always reverts to select mode and hands pan/zoom back — a tool
    // armed for the old chart shouldn't silently start placing on the new one.
    this.tool = "select";
    this.onToolChange?.("select");
    this.facade.setPanZoomEnabled(true);
    this.primitive.requestUpdate();
  }

  dispose(): void {
    for (const [t, cb] of this.listeners) this.host.removeEventListener(t, cb);
    this.listeners.length = 0;
    this.facade.setPanZoomEnabled(true);
  }

  // --- pan/zoom lock: armed tools lock the whole time; select/measure only during a drag ---
  private applyPanZoomLock(): void {
    const armed = this.tool !== "select" && this.tool !== "measure";
    this.facade.setPanZoomEnabled(!armed);
  }

  private cancelGesture(): void {
    this.gesture = { kind: "none" };
    this.primitive.setTransient(null);
  }

  // --- coordinate helpers ---
  private pos(e: PointerLike): Px {
    const r = this.host.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }
  private barsMs(): number[] {
    return this.ctx.bars().map((b) => Date.parse(b.bucketStart));
  }
  private snap(p: Px): Anchor | null {
    const bars = this.ctx.bars();
    if (bars.length === 0) return null;
    const logical = this.facade.coordinateToLogical(p.x);
    if (logical === null) return null;
    const idx = Math.max(0, Math.min(bars.length - 1, Math.round(logical)));
    const timeMs = Date.parse(bars[idx].bucketStart);
    const raw = this.facade.coordinateToPrice(p.y);
    let price = raw ?? 0;
    if (this.ctx.magnet() && raw !== null) {
      const b = bars[idx];
      const levels = [b.o, b.h, b.l, b.c]
        .map((pr) => ({ price: pr, y: this.facade.priceToCoordinate(pr) }))
        .filter((l): l is { price: number; y: number } => l.y !== null);
      const snapped = snapToLevels(p.y, levels, MAGNET_PX);
      if (snapped !== null) price = snapped;
    }
    return { timeMs, price };
  }
  private project(a: Anchor): Px | null {
    const logical = timeToLogical(a.timeMs, this.barsMs(), this.ctx.timeframeMs());
    const x = this.facade.logicalToCoordinate(logical);
    const y = this.facade.priceToCoordinate(a.price);
    return x === null || y === null ? null : { x, y };
  }

  // --- pointer handlers ---
  private onPointerDown(e: PointerLike): void {
    this.host.focus();
    const p = this.pos(e);
    const anchor = this.snap(p);

    if (this.tool === "measure") {
      if (!anchor) return;
      this.gesture = { kind: "measuring", from: anchor };
      this.facade.setPanZoomEnabled(false);
      this.primitive.setTransient({ measure: { from: anchor, to: anchor } });
      this.primitive.requestUpdate();
      return;
    }

    if (this.tool !== "select") { this.placeAnchor(anchor); return; }

    // select mode: hit-test top-most first
    const drawings = this.store.forSymbol(this.ctx.symbol());
    for (let i = drawings.length - 1; i >= 0; i--) {
      const d = drawings[i];
      const pts = d.anchors.map((a) => this.project(a));
      const hit = hitTest(d.kind, pts, p, this.host.clientWidth);
      if (!hit) continue;
      this.selectionId = d.id;
      this.primitive.setSelection(d.id);
      this.facade.setPanZoomEnabled(false);
      if (hit.type === "handle") {
        this.gesture = { kind: "handleDrag", id: d.id, index: hit.index };
      } else {
        const logical = this.facade.coordinateToLogical(p.x) ?? 0;
        const price = this.facade.coordinateToPrice(p.y) ?? 0;
        this.gesture = { kind: "bodyDrag", id: d.id, downLogical: logical, downPrice: price, orig: d.anchors.map((a) => ({ ...a })) };
      }
      this.primitive.requestUpdate();
      return;
    }
    // empty space → deselect (pan/zoom stays enabled so LWC pans)
    this.selectionId = null;
    this.primitive.setSelection(null);
    this.primitive.requestUpdate();
  }

  private placeAnchor(anchor: Anchor | null): void {
    if (!anchor) return;
    const kind = this.tool as DrawingKind;
    if (this.gesture.kind === "placing") {
      // second click → commit
      this.commit(kind, [this.gesture.anchor0, anchor]);
      return;
    }
    if (anchorCount(kind) === 1) { this.commit(kind, [anchor]); return; }
    // first click of a 2-anchor tool → start placing, show ghost
    this.gesture = { kind: "placing", anchor0: anchor };
    this.primitive.setTransient({ ghost: { kind, anchors: [anchor, anchor] } });
    this.primitive.requestUpdate();
  }

  private commit(kind: DrawingKind, anchors: Anchor[]): void {
    const now = Date.now();
    const d: Drawing = { id: this.newId(), symbol: this.ctx.symbol(), kind, anchors, createdMs: now, updatedMs: now };
    this.store.upsert(d);
    this.cancelGesture();
    // revert to select (TradingView behavior)
    this.tool = "select";
    this.onToolChange?.("select");
    this.applyPanZoomLock();
    this.primitive.requestUpdate();
  }

  private onPointerMove(e: PointerLike): void {
    const p = this.pos(e);
    const g = this.gesture;
    if (g.kind === "placing") {
      const anchor = this.snap(p);
      if (anchor) { this.primitive.setTransient({ ghost: { kind: this.tool as DrawingKind, anchors: [g.anchor0, anchor] } }); this.primitive.requestUpdate(); }
    } else if (g.kind === "measuring") {
      const anchor = this.snap(p);
      if (anchor) { this.primitive.setTransient({ measure: { from: g.from, to: anchor } }); this.primitive.requestUpdate(); }
    } else if (g.kind === "handleDrag") {
      const anchor = this.snap(p);
      const d = this.currentDrawing(g.id);
      if (anchor && d) {
        const anchors = d.anchors.map((a, i) => (i === g.index ? anchor : a));
        this.store.upsert({ ...d, anchors, updatedMs: Date.now() });
        this.primitive.requestUpdate();
      }
    } else if (g.kind === "bodyDrag") {
      const d = this.currentDrawing(g.id);
      const curLogical = this.facade.coordinateToLogical(p.x);
      const curPrice = this.facade.coordinateToPrice(p.y);
      if (d && curLogical !== null && curPrice !== null) {
        const dBars = Math.round(curLogical) - Math.round(g.downLogical);
        const dPrice = curPrice - g.downPrice;
        const bars = this.ctx.bars();
        const barsMs = this.barsMs();
        const anchors = g.orig.map((a) => {
          const idx = Math.max(0, Math.min(bars.length - 1, Math.round(timeToLogical(a.timeMs, barsMs, this.ctx.timeframeMs())) + dBars));
          return { timeMs: bars.length ? Date.parse(bars[idx].bucketStart) : a.timeMs, price: a.price + dPrice };
        });
        this.store.upsert({ ...d, anchors, updatedMs: Date.now() });
        this.primitive.requestUpdate();
      }
    }
  }

  private onPointerUp(_e: PointerLike): void {
    const g = this.gesture;
    if (g.kind === "handleDrag" || g.kind === "bodyDrag") {
      this.gesture = { kind: "none" };
      this.applyPanZoomLock(); // back to select → unlock
      this.primitive.requestUpdate();
    } else if (g.kind === "measuring") {
      // keep the box visible until the next pointerdown or Esc; just end the drag
      this.gesture = { kind: "none" };
      this.facade.setPanZoomEnabled(true);
    }
  }

  private onKeyDown(e: KeyLike): void {
    if (e.key === "Escape") {
      e.preventDefault?.();
      if (this.gesture.kind === "placing") {
        this.cancelGesture();
        this.tool = "select";
        this.onToolChange?.("select");
        this.applyPanZoomLock();
      } else {
        this.cancelGesture(); // clears a lingering measure box or in-progress drag
        this.applyPanZoomLock();
      }
      this.selectionId = null;
      this.primitive.setSelection(null);
      this.primitive.requestUpdate();
      return;
    }
    if ((e.key === "Delete" || e.key === "Backspace") && this.selectionId) {
      e.preventDefault?.();
      this.store.remove(this.selectionId);
      this.selectionId = null;
      this.primitive.setSelection(null);
      this.primitive.requestUpdate();
    }
  }

  private currentDrawing(id: string): Drawing | undefined {
    return this.store.forSymbol(this.ctx.symbol()).find((d) => d.id === id);
  }
}
