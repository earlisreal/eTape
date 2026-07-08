import type {
  ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, Time,
  IPrimitivePaneView, IPrimitivePaneRenderer, Logical,
} from "lightweight-charts";
import type { Palette } from "../../palette";
import type { Anchor, Drawing, DrawingKind } from "./model";
import { extendToEdge, timeToLogical } from "./geometry";
import { LINE_DASH } from "../lineStyle";
import { DEFAULT_DRAWING_WIDTH, DEFAULT_LINE_STYLE } from "./model";

// Repo convention: derive the draw target structurally instead of importing
// fancy-canvas's CanvasRenderingTarget2D directly.
type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];

export interface Transient {
  ghost?: { kind: DrawingKind; anchors: Anchor[] };
  measure?: { from: Anchor; to: Anchor };
}

export interface DrawingsPrimitiveHandle {
  setSelection(id: string | null): void;
  setTransient(t: Transient | null): void;
  requestUpdate(): void;
}

type Px = { x: number; y: number };

export class DrawingsPrimitive implements ISeriesPrimitive<Time>, DrawingsPrimitiveHandle {
  private series: ISeriesApi<"Candlestick"> | null = null;
  private chartApi: SeriesAttachedParameter<Time>["chart"] | null = null;
  private requestUpdateFn: (() => void) | null = null;
  private drawings: Drawing[] = [];
  private barsMs: readonly number[] = [];
  private timeframeMs = 60_000;
  private selectionId: string | null = null;
  private transient: Transient | null = null;
  private hideAll = false;

  constructor(private palette: Palette) {}

  attached(p: SeriesAttachedParameter<Time>): void {
    this.series = p.series as ISeriesApi<"Candlestick">;
    this.chartApi = p.chart;
    this.requestUpdateFn = p.requestUpdate;
  }
  detached(): void {
    this.series = null;
    this.chartApi = null;
    this.requestUpdateFn = null;
  }

  requestUpdate(): void { this.requestUpdateFn?.(); }
  setPalette(p: Palette): void { this.palette = p; }
  setDrawings(d: Drawing[]): void { this.drawings = d; }
  setBars(barsMs: readonly number[], timeframeMs: number): void { this.barsMs = barsMs; this.timeframeMs = timeframeMs; }
  setSelection(id: string | null): void { this.selectionId = id; }
  setTransient(t: Transient | null): void { this.transient = t; }
  setHideAll(hidden: boolean): void { this.hideAll = hidden; }

  paneViews(): readonly IPrimitivePaneView[] {
    const draw = (target: DrawTarget) => this.draw(target);
    return [{ renderer: () => ({ draw }), zOrder: () => "top" as const }];
  }

  private xOf(a: Anchor, hr: number): number | null {
    if (!this.chartApi) return null;
    const logical = timeToLogical(a.timeMs, this.barsMs, this.timeframeMs);
    const x = this.chartApi.timeScale().logicalToCoordinate(logical as Logical);
    return x === null ? null : x * hr;
  }
  private yOf(a: Anchor, vr: number): number | null {
    const y = this.series?.priceToCoordinate(a.price) ?? null;
    return y === null ? null : y * vr;
  }
  private pt(a: Anchor, hr: number, vr: number): Px | null {
    const x = this.xOf(a, hr);
    const y = this.yOf(a, vr);
    return x === null || y === null ? null : { x, y };
  }

  private draw(target: DrawTarget): void {
    target.useBitmapCoordinateSpace(({ context: ctx, bitmapSize, horizontalPixelRatio: hr, verticalPixelRatio: vr }) => {
      const width = bitmapSize.width;
      if (!this.hideAll) {
        for (const d of this.drawings) {
          const selected = d.id === this.selectionId;
          ctx.setLineDash(LINE_DASH[d.lineStyle ?? DEFAULT_LINE_STYLE]);
          const color = selected ? this.palette.accent : (d.color ?? this.palette.text);
          const lineWidth = selected ? Math.max(2, d.width ?? DEFAULT_DRAWING_WIDTH) : (d.width ?? DEFAULT_DRAWING_WIDTH);
          this.strokeShape(ctx, d.kind, d.anchors, hr, vr, width, color, lineWidth);
          if (selected) this.handles(ctx, d.anchors, hr, vr);
        }
        ctx.setLineDash([]);
      }
      if (this.transient?.ghost) {
        ctx.setLineDash([4, 3]);
        this.strokeShape(ctx, this.transient.ghost.kind, this.transient.ghost.anchors, hr, vr, width, this.palette.accent, 1);
        ctx.setLineDash([]);
      }
      if (this.transient?.measure) this.measure(ctx, this.transient.measure, hr, vr);
    });
  }

  private strokeShape(ctx: any, kind: DrawingKind, anchors: Anchor[], hr: number, vr: number, width: number, color: string, lineWidth: number): void {
    ctx.strokeStyle = color;
    ctx.lineWidth = lineWidth;
    const p0 = this.pt(anchors[0], hr, vr);
    if (!p0) return;
    if (kind === "hline") { this.line(ctx, 0, p0.y, width, p0.y); return; }
    if (kind === "hray") { this.line(ctx, p0.x, p0.y, width, p0.y); return; }
    const p1 = anchors[1] ? this.pt(anchors[1], hr, vr) : null;
    if (!p1) return;
    if (kind === "trendline") { this.line(ctx, p0.x, p0.y, p1.x, p1.y); return; }
    if (kind === "ray") { const far = extendToEdge(p0, p1, width); this.line(ctx, p0.x, p0.y, far.x, far.y); return; }
    if (kind === "rect") { ctx.strokeRect(Math.min(p0.x, p1.x), Math.min(p0.y, p1.y), Math.abs(p1.x - p0.x), Math.abs(p1.y - p0.y)); }
  }

  private line(ctx: any, x0: number, y0: number, x1: number, y1: number): void {
    ctx.beginPath();
    ctx.moveTo(x0, y0);
    ctx.lineTo(x1, y1);
    ctx.stroke();
  }

  private handles(ctx: any, anchors: Anchor[], hr: number, vr: number): void {
    const r = 3;
    for (const a of anchors) {
      const p = this.pt(a, hr, vr);
      if (!p) continue;
      ctx.fillStyle = this.palette.bg;
      ctx.fillRect(p.x - r, p.y - r, r * 2, r * 2);
      ctx.strokeStyle = this.palette.accent;
      ctx.lineWidth = 1;
      ctx.strokeRect(p.x - r, p.y - r, r * 2, r * 2);
    }
  }

  private measure(ctx: any, m: { from: Anchor; to: Anchor }, hr: number, vr: number): void {
    const p0 = this.pt(m.from, hr, vr);
    const p1 = this.pt(m.to, hr, vr);
    if (!p0 || !p1) return;
    const x = Math.min(p0.x, p1.x);
    const y = Math.min(p0.y, p1.y);
    const w = Math.abs(p1.x - p0.x);
    const h = Math.abs(p1.y - p0.y);
    ctx.fillStyle = this.palette.accent;
    ctx.globalAlpha = 0.12;
    ctx.fillRect(x, y, w, h);
    ctx.globalAlpha = 1;
    ctx.strokeStyle = this.palette.accent;
    ctx.lineWidth = 1;
    ctx.strokeRect(x, y, w, h);
    const dPts = m.to.price - m.from.price;
    const dPct = m.from.price !== 0 ? (dPts / m.from.price) * 100 : 0;
    const bars = Math.round(timeToLogical(m.to.timeMs, this.barsMs, this.timeframeMs)) - Math.round(timeToLogical(m.from.timeMs, this.barsMs, this.timeframeMs));
    const label = `${dPts >= 0 ? "+" : ""}${dPts.toFixed(2)}  ${dPct >= 0 ? "+" : ""}${dPct.toFixed(2)}%  ${Math.abs(bars)} bars`;
    ctx.fillStyle = this.palette.text;
    ctx.font = `${12 * vr}px sans-serif`;
    ctx.textBaseline = "bottom";
    ctx.fillText(label, x, y - 2);
  }
}
