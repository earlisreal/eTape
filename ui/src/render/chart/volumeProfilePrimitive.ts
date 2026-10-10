import type { IPrimitivePaneRenderer, IPrimitivePaneView, ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, Time } from "lightweight-charts";
import { FONTS, type Palette } from "../palette";
import { formatPrice } from "../format";
import { describeIndicator, type IndicatorInstance, type SeriesDescriptor } from "./indicatorSeries";
import { LINE_DASH } from "./lineStyle";
import type { VolumeProfileProjection } from "./volumeProfileController";

type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];

// The profile is a passive main-series primitive: it owns no price scale,
// autoscale contribution, hit target or time-series data.
export class VolumeProfilePrimitive implements ISeriesPrimitive<Time> {
  private series: ISeriesApi<"Candlestick"> | null = null;
  private update: (() => void) | null = null;
  private instance: IndicatorInstance | null = null;
  private projection: VolumeProfileProjection = { status: "idle", result: null, detail: "" };
  private slots = new Map<string, SeriesDescriptor>();
  private decimals = 4;

  constructor(private palette: Palette) {}

  attached(p: SeriesAttachedParameter<Time>): void { this.series = p.series as ISeriesApi<"Candlestick">; this.update = p.requestUpdate; }
  detached(): void { this.series = null; this.update = null; }
  setInstance(instance: IndicatorInstance | null): void {
    if (this.instance === instance) return;
    this.instance = instance; this.rebuildSlots(); this.update?.();
  }
  setProjection(projection: VolumeProfileProjection, decimals = 4): void { this.projection = projection; this.decimals = decimals; this.update?.(); }
  setPalette(palette: Palette): void { this.palette = palette; this.rebuildSlots(); this.update?.(); }

  paneViews(): readonly IPrimitivePaneView[] {
    return [
      { renderer: () => ({ draw: (target: DrawTarget) => this.drawHistogram(target) }), zOrder: () => "bottom" as const },
      { renderer: () => ({ draw: (target: DrawTarget) => this.drawGuides(target) }), zOrder: () => "top" as const },
    ];
  }

  private rebuildSlots(): void { this.slots = new Map(this.instance ? describeIndicator(this.instance, this.palette).map((slot) => [slot.slot, slot]) : []); }
  private enabled(): boolean { return !!this.series && !!this.instance && !this.instance.hidden && !!this.projection.result; }

  private drawHistogram(target: DrawTarget): void {
    if (!this.enabled()) return;
    const result = this.projection.result!;
    const maxVolume = Math.max(0, ...result.rows.map((row) => row.volume));
    if (maxVolume === 0) return;
    target.useBitmapCoordinateSpace(({ context: ctx, bitmapSize, verticalPixelRatio: vr }) => {
      ctx.save();
      for (const row of result.rows) {
        if (row.volume <= 0) continue;
        const lower = this.series!.priceToCoordinate(row.lower), upper = this.series!.priceToCoordinate(row.upper);
        if (lower === null || upper === null) continue;
        const insideVA = result.val !== undefined && result.vah !== undefined && row.lower >= result.val && row.upper <= result.vah;
        const slot = this.slots.get(insideVA && !this.slots.get("valueArea")?.hidden ? "valueArea" : "hist");
        if (!slot || slot.hidden) continue;
        const top = Math.max(0, Math.min(lower, upper)*vr-(lower === upper ? vr : 0));
        const bottom = Math.min(bitmapSize.height, Math.max(lower, upper)*vr+(lower === upper ? vr : 0));
        if (bottom <= top) continue;
        const width = row.volume/maxVolume*bitmapSize.width*0.2;
        const x = this.instance!.placement === "right" ? bitmapSize.width-width : 0;
        ctx.fillStyle = slot.color; ctx.globalAlpha = insideVA ? 0.42 : 0.22;
        ctx.fillRect(x, top, width, bottom-top);
      }
      ctx.restore();
    });
  }

  private drawGuides(target: DrawTarget): void {
    if (!this.enabled()) return;
    const result = this.projection.result!;
    const groups = new Map<number, { labels: string[]; style: SeriesDescriptor }>();
    for (const slot of ["poc", "vah", "val"] as const) {
      const price = result[slot], style = this.slots.get(slot);
      if (price === undefined || !style || style.hidden) continue;
      const group = groups.get(price);
      if (group) group.labels.push(slot.toUpperCase()); else groups.set(price, { labels: [slot.toUpperCase()], style });
    }
    target.useBitmapCoordinateSpace(({ context: ctx, bitmapSize, horizontalPixelRatio: hr, verticalPixelRatio: vr }) => {
      ctx.save(); ctx.font = `400 ${11*vr}px ${FONTS.mono}`; ctx.textBaseline = "top";
      for (const [price, group] of groups) {
        const coordinate = this.series!.priceToCoordinate(price);
        if (coordinate === null) continue;
        const y = coordinate*vr;
        if (y < 0 || y > bitmapSize.height) continue;
        ctx.globalAlpha = 0.8; ctx.strokeStyle = group.style.color; ctx.lineWidth = group.style.width*vr;
        ctx.setLineDash(LINE_DASH[group.style.lineStyle].map((value) => value*hr));
        ctx.beginPath(); ctx.moveTo(0,y); ctx.lineTo(bitmapSize.width,y); ctx.stroke();
        const text = `${group.labels.join("/")} ${formatPrice(price,this.decimals)}`;
        const width = ctx.measureText(text).width;
        const x = this.instance!.placement === "right" ? Math.max(3*hr, bitmapSize.width-width-3*hr) : 3*hr;
        ctx.fillStyle = group.style.color; ctx.globalAlpha = 1;
        ctx.fillText(text,x,Math.max(0,Math.min(bitmapSize.height-11*vr,y-13*vr)));
      }
      ctx.restore();
    });
  }
}
