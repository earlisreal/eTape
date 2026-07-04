import type {
  ISeriesApi, ISeriesPrimitive, SeriesAttachedParameter, Time,
  IPrimitivePaneView, IPrimitivePaneRenderer,
} from "lightweight-charts";
import { drawDiamondPath, diamondHalfSize, fillColor, type FillMarker } from "./diamondMarker";
import type { Palette } from "../palette";

// The concrete canvas-rendering-target shape `draw()` receives (CanvasRenderingTarget2D
// from `fancy-canvas`, not re-exported by name from `lightweight-charts` — pulled out
// structurally via the real `IPrimitivePaneRenderer["draw"]` signature instead of
// depending on the transitive package directly).
type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];

// Draws buy/sell diamond fills anchored to (time, price), with a thin dark outline
// (v3.7.1 borderWidth pattern: stroke after fill). Culling is implicit — LWC returns
// null coordinates for off-screen times/prices and we skip them.
export class DiamondFillPrimitive implements ISeriesPrimitive<Time> {
  private markers: FillMarker[] = [];
  private series: ISeriesApi<"Candlestick"> | null = null;
  private chartApi: SeriesAttachedParameter<Time>["chart"] | null = null;
  private readonly size = 16;

  constructor(private palette: Palette) {}
  attached(p: SeriesAttachedParameter<Time>): void { this.series = p.series as ISeriesApi<"Candlestick">; this.chartApi = p.chart; }
  detached(): void { this.series = null; this.chartApi = null; }
  setMarkers(m: FillMarker[]): void { this.markers = m; }
  setPalette(p: Palette): void { this.palette = p; }

  paneViews(): readonly IPrimitivePaneView[] {
    const draw = (target: DrawTarget) => {
      const series = this.series, chartApi = this.chartApi;
      if (!series || !chartApi) return;
      target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, verticalPixelRatio: vr }) => {
        const half = diamondHalfSize(this.size);
        for (const m of this.markers) {
          const x = chartApi.timeScale().timeToCoordinate((Math.floor(m.timeMs / 1000)) as unknown as Time);
          const y = series.priceToCoordinate(m.price);
          if (x === null || y === null) continue; // off-screen → skip (culling)
          const px = x * hr, py = y * vr, ph = half * Math.max(hr, vr);
          ctx.fillStyle = fillColor(m.side, this.palette);
          drawDiamondPath(ctx, px, py, ph);
          ctx.fill();
          ctx.lineWidth = Math.max(1, hr);       // borderWidth pattern
          ctx.strokeStyle = this.palette.fillOutline;
          ctx.stroke();
        }
      });
    };
    return [{ renderer: () => ({ draw }) }];
  }
}
