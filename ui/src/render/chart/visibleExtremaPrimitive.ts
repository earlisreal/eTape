import type {
  IPrimitivePaneRenderer, IPrimitivePaneView, ISeriesApi, ISeriesPrimitive, Logical,
  SeriesAttachedParameter, Time,
} from "lightweight-charts";
import type { Palette } from "../palette";
import { FONTS } from "../palette";
import type { VisibleExtremaAnchor, VisibleExtremaProjection } from "./ChartController";
import { formatPrice } from "../format";

type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];
type Point = { x: number; y: number };
type Placement = { point: Point; text: string; x: number; top: number; width: number; height: number; above: boolean };

const FONT_SIZE = 11;
const HORIZONTAL_GAP = 6;
const VERTICAL_GAP = 5;
const EDGE_INSET = 3;
const LABEL_SEPARATION = 3;

export class VisibleExtremaPrimitive implements ISeriesPrimitive<Time> {
  private series: ISeriesApi<"Candlestick"> | null = null;
  private chartApi: SeriesAttachedParameter<Time>["chart"] | null = null;
  private requestUpdateFn: (() => void) | null = null;
  private projection: VisibleExtremaProjection = { high: null, low: null };
  private decimals = 3;
  private visible = true;

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

  setProjection(projection: VisibleExtremaProjection, decimals: number): void {
    if (sameProjection(this.projection, projection) && this.decimals === decimals) return;
    this.projection = projection;
    this.decimals = decimals;
    this.requestUpdate();
  }

  setVisible(visible: boolean): void {
    if (this.visible === visible) return;
    this.visible = visible;
    this.requestUpdate();
  }

  setPalette(palette: Palette): void {
    this.palette = palette;
    this.requestUpdate();
  }

  requestUpdate(): void { this.requestUpdateFn?.(); }

  paneViews(): readonly IPrimitivePaneView[] {
    const draw = (target: DrawTarget) => this.draw(target);
    return [{ renderer: () => ({ draw }), zOrder: () => "top" as const }];
  }

  private point(anchor: VisibleExtremaAnchor): Point | null {
    if (!this.chartApi || !this.series) return null;
    const x = this.chartApi.timeScale().logicalToCoordinate(anchor.logical as Logical);
    const y = this.series.priceToCoordinate(anchor.price);
    return x === null || y === null ? null : { x, y };
  }

  private draw(target: DrawTarget): void {
    if (!this.visible) return;
    const { high, low } = this.projection;
    if (!high && !low) return;
    const highPoint = high ? this.point(high) : null;
    const lowPoint = low ? this.point(low) : null;
    target.useBitmapCoordinateSpace(({ context: ctx, bitmapSize, horizontalPixelRatio: hr, verticalPixelRatio: vr }) => {
      if (high && low && high.price === low.price) {
        if (highPoint || lowPoint) {
          const anchor = highPoint && lowPoint && high.logical >= low.logical ? highPoint : lowPoint ?? highPoint;
          if (anchor) {
            const placement = this.place(ctx, { x: anchor.x * hr, y: anchor.y * vr }, `H/L ${formatPrice(high.price, this.decimals)}`,
              true, bitmapSize.width, bitmapSize.height, hr, vr);
            this.drawCallout(ctx, placement);
          }
        }
        return;
      }

      ctx.font = this.font(vr);
      let highPlacement = high && highPoint
        ? this.place(ctx, { x: highPoint.x * hr, y: highPoint.y * vr }, formatPrice(high.price, this.decimals), true, bitmapSize.width, bitmapSize.height, hr, vr)
        : null;
      let lowPlacement = low && lowPoint
        ? this.place(ctx, { x: lowPoint.x * hr, y: lowPoint.y * vr }, formatPrice(low.price, this.decimals), false, bitmapSize.width, bitmapSize.height, hr, vr)
        : null;
      if (highPlacement && lowPlacement) {
        const gap = LABEL_SEPARATION * vr;
        if (highPlacement.top + highPlacement.height + gap > lowPlacement.top) {
          const inset = EDGE_INSET * vr;
          highPlacement = {
            ...highPlacement,
            top: this.clamp(
              highPlacement.top - (highPlacement.top + highPlacement.height + gap - lowPlacement.top),
              inset, Math.max(inset, bitmapSize.height - inset - highPlacement.height),
            ),
          };
          if (highPlacement.top + highPlacement.height + gap > lowPlacement.top) {
            lowPlacement = {
              ...lowPlacement,
              top: this.clamp(
                highPlacement.top + highPlacement.height + gap,
                inset, Math.max(inset, bitmapSize.height - inset - lowPlacement.height),
              ),
            };
          }
        }
      }
      if (highPlacement) this.drawCallout(ctx, highPlacement);
      if (lowPlacement) this.drawCallout(ctx, lowPlacement);
    });
  }

  private place(ctx: CanvasRenderingContext2D, point: Point, text: string, above: boolean,
    width: number, height: number, hr: number, vr: number): Placement {
    ctx.font = this.font(vr);
    const textWidth = ctx.measureText(text).width;
    const textHeight = FONT_SIZE * vr;
    const xInset = EDGE_INSET * hr;
    const right = point.x + HORIZONTAL_GAP * hr;
    const left = point.x - HORIZONTAL_GAP * hr - textWidth;
    const maxX = Math.max(xInset, width - xInset - textWidth);
    const x = right + textWidth <= width - xInset ? right : this.clamp(left, xInset, maxX);
    const yInset = EDGE_INSET * vr;
    const maxTop = Math.max(yInset, height - yInset - textHeight);
    const top = above
      ? this.clamp(point.y - (VERTICAL_GAP * vr + textHeight), yInset, maxTop)
      : this.clamp(point.y + VERTICAL_GAP * vr, yInset, maxTop);
    return { point, text, x, top, width: textWidth, height: textHeight, above };
  }

  private drawCallout(ctx: CanvasRenderingContext2D, placement: Placement): void {
    const labelEdge = placement.x >= placement.point.x ? placement.x : placement.x + placement.width;
    const leaderY = placement.above ? placement.top + placement.height : placement.top;
    ctx.strokeStyle = this.palette.textMuted;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(placement.point.x, placement.point.y);
    ctx.lineTo(labelEdge, leaderY);
    ctx.stroke();
    ctx.fillStyle = this.palette.text;
    ctx.textBaseline = "top";
    ctx.fillText(placement.text, placement.x, placement.top);
  }

  private font(vr: number): string { return `400 ${FONT_SIZE * vr}px ${FONTS.mono}`; }
  private clamp(value: number, min: number, max: number): number { return Math.min(Math.max(value, min), max); }
}

function sameProjection(a: VisibleExtremaProjection, b: VisibleExtremaProjection): boolean {
  return sameAnchor(a.high, b.high) && sameAnchor(a.low, b.low);
}

function sameAnchor(a: VisibleExtremaAnchor | null, b: VisibleExtremaAnchor | null): boolean {
  return a === b || (a !== null && b !== null && a.logical === b.logical && a.price === b.price);
}
