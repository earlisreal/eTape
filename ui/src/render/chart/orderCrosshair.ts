import type { IChartApi } from "lightweight-charts";
import type { ChartApiFacade } from "./ChartApiFacade";
import { snapOrderMarkerPrice } from "./orderMarkers";

// Feed the native crosshair a snapped Y without moving its X (including future
// blank space). Public setCrosshairPosition would snap X to an existing candle.
export function installOrderCrosshair(chart: IChartApi, host: HTMLElement,
  facade: Pick<ChartApiFacade, "coordinateToPrice" | "priceToCoordinate" | "priceScaleWidth" | "paneHeights">,
  normalColor: () => string) {
  const normalLabel = chart.options().crosshair.horzLine.labelBackgroundColor;
  const normalFormatter = chart.options().localization.priceFormatter;
  let color: string | null = null, replaying = false;
  let last: MouseEvent | null = null;
  let nativeTarget: Element | null = null;
  const refresh = () => chart.applyOptions({
    crosshair: { vertLine: { color: color ?? normalColor() },
      horzLine: { color: color ?? normalColor(), labelBackgroundColor: color ?? normalLabel } },
    localization: { priceFormatter: color ? (price: number) => {
      const snapped = snapOrderMarkerPrice(price);
      return snapped.toFixed(snapped < 1 ? 4 : 2);
    } : normalFormatter },
  // LWC accepts undefined to restore its default formatter; its optional type
  // does not expose that reset with exactOptionalPropertyTypes enabled.
  } as Parameters<IChartApi["applyOptions"]>[0]);
  const move = (event: MouseEvent) => {
    if (replaying) return;
    last = event;
    if (!(event.target instanceof Element)) return;
    if (event.target.closest(".tv-lightweight-charts")) nativeTarget = event.target;
    const target = event.target === host ? nativeTarget : event.target.closest(".tv-lightweight-charts") ? event.target : null;
    if (!color || !target) return;
    const rect = host.getBoundingClientRect(), x = event.clientX - rect.left, y = event.clientY - rect.top;
    if (x < 0 || x >= rect.width - facade.priceScaleWidth() || y < 0 || y >= facade.paneHeights()[0]) return;
    const raw = facade.coordinateToPrice(y);
    if (raw == null || !Number.isFinite(raw) || raw <= 0) return;
    const price = snapOrderMarkerPrice(raw), snappedY = facade.priceToCoordinate(price);
    if (snappedY == null) return;
    host.dataset.orderCursorPrice = String(price);
    host.dataset.orderCursorY = String(snappedY);
    event.stopImmediatePropagation();
    replaying = true;
    try {
      target.dispatchEvent(new MouseEvent("mousemove", { bubbles: true, clientX: event.clientX,
        clientY: rect.top + snappedY, buttons: event.buttons, ctrlKey: event.ctrlKey,
        altKey: event.altKey, shiftKey: event.shiftKey, metaKey: event.metaKey }));
    } finally { replaying = false; }
  };
  host.addEventListener("mousemove", move, true);
  return {
    set(next: string | null) {
      if (color === next) return;
      color = next;
      if (next) host.dataset.orderCursorColor = next;
      else { delete host.dataset.orderCursorColor; delete host.dataset.orderCursorPrice; delete host.dataset.orderCursorY; }
      refresh();
      if (last && color) move(last);
    },
    refresh,
    dispose() { host.removeEventListener("mousemove", move, true); },
  };
}
