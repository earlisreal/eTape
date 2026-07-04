import type {
  IPanePrimitive, PaneAttachedParameter, Time,
  IPanePrimitivePaneView, IPrimitivePaneRenderer,
} from "lightweight-charts";
import type { Band, Session } from "./sessions";
import type { Palette } from "../palette";

type DrawTarget = Parameters<IPrimitivePaneRenderer["draw"]>[0];

// Fills vertical session bands behind the bars (pre/post/closed tinted; rth clear).
export class SessionShadingPrimitive implements IPanePrimitive<Time> {
  private bands: Band[] = [];
  private chartApi: PaneAttachedParameter<Time>["chart"] | null = null;
  constructor(private palette: Palette) {}
  attached(p: PaneAttachedParameter<Time>): void { this.chartApi = p.chart; }
  detached(): void { this.chartApi = null; }
  setBands(b: Band[]): void { this.bands = b; }
  setPalette(p: Palette): void { this.palette = p; }

  private color(s: Session): string {
    return s === "pre" ? this.palette.sessionPre
      : s === "post" ? this.palette.sessionPost
      : s === "closed" ? this.palette.sessionClosed
      : this.palette.sessionRth;
  }

  paneViews(): readonly IPanePrimitivePaneView[] {
    const draw = (target: DrawTarget) => {
      const chartApi = this.chartApi;
      if (!chartApi) return;
      const ts = chartApi.timeScale();
      target.useBitmapCoordinateSpace(({ context: ctx, horizontalPixelRatio: hr, bitmapSize }) => {
        for (const b of this.bands) {
          const x0 = ts.timeToCoordinate((Math.floor(b.startMs / 1000)) as unknown as Time);
          const x1 = ts.timeToCoordinate((Math.floor(b.endMs / 1000)) as unknown as Time);
          if (x0 === null || x1 === null) continue;
          ctx.fillStyle = this.color(b.session);
          ctx.fillRect(x0 * hr, 0, (x1 - x0) * hr, bitmapSize.height);
        }
      });
    };
    // zOrder 'bottom' → behind the series.
    return [{ renderer: () => ({ draw }), zOrder: () => "bottom" as const }];
  }
}
