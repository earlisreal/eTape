import type { IChartApi } from "lightweight-charts";
import type { ChartApiFacade } from "./ChartApiFacade";
import { snapOrderMarkerPrice } from "./orderMarkers";

// Preserve pointer X (including future blank space) when replaying captured
// risk input. Public setCrosshairPosition would snap X to an existing candle.
export function installOrderCrosshair(chart: IChartApi, host: HTMLElement,
  facade: Pick<ChartApiFacade, "coordinateToPrice" | "priceToCoordinate" | "priceScaleWidth" | "paneHeights">,
  normalColor: () => string) {
  const normalLabel = chart.options().crosshair.horzLine.labelBackgroundColor;
  const normalFormatter = chart.options().localization.priceFormatter;
  let color: string | null = null, replaying = false, preview = false;
  let horizontalVisible = chart.options().crosshair.horzLine.visible !== false;
  let last: MouseEvent | null = null;
  let nativeTarget: Element | null = null;
  const refresh = () => {
    const nativeColor = preview ? null : color;
    chart.applyOptions({
      crosshair: { vertLine: { color: nativeColor ?? normalColor() },
        horzLine: { visible: horizontalVisible && !preview, labelVisible: horizontalVisible && !preview,
          color: nativeColor ?? normalColor(), labelBackgroundColor: nativeColor ?? normalLabel } },
      localization: { priceFormatter: color && !preview ? (price: number) => {
        const snapped = snapOrderMarkerPrice(price);
        return snapped.toFixed(snapped < 1 ? 4 : 2);
      } : normalFormatter },
      // LWC accepts undefined to restore its default formatter; its optional type
      // does not expose that reset with exactOptionalPropertyTypes enabled.
    } as Parameters<IChartApi["applyOptions"]>[0]);
  };
  const move = (event: MouseEvent, forwardPreview = false) => {
    if (replaying) return;
    last = event;
    const element = event.target instanceof Element ? event.target : null;
    if (element?.closest(".tv-lightweight-charts")) nativeTarget = element;
    const target = element?.closest(".tv-lightweight-charts") ? element : preview || event.target === host ? nativeTarget : null;
    if (!color || !target || (preview && !forwardPreview)) return;
    const rect = host.getBoundingClientRect(), x = event.clientX - rect.left, y = event.clientY - rect.top;
    if (x < 0 || x >= rect.width - facade.priceScaleWidth() || y < 0 || y >= facade.paneHeights()[0]) return;
    const raw = facade.coordinateToPrice(y);
    if (raw == null || !Number.isFinite(raw) || raw <= 0) return;
    const price = snapOrderMarkerPrice(raw), snappedY = preview ? y : facade.priceToCoordinate(price);
    if (snappedY == null) return;
    if (!preview) {
      host.dataset.orderCursorPrice = String(price);
      host.dataset.orderCursorY = String(snappedY);
    }
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
    set(next: string | null, pointer?: MouseEvent) {
      const nextPreview = !!next && (host.dataset.orderEntryMode === "risk" || host.dataset.orderEntryMode === "gesture");
      const changed = color !== next || preview !== nextPreview;
      if (!changed && (!pointer || pointer === last)) return;
      color = next;
      preview = nextPreview;
      if (next) host.dataset.orderCursorColor = next;
      else { delete host.dataset.orderCursorColor; delete host.dataset.orderCursorPrice; delete host.dataset.orderCursorY; }
      if (changed) refresh();
      const point = pointer ?? last;
      if (point && color) move(point, true);
    },
    setHorizontalVisible(visible: boolean) {
      if (horizontalVisible === visible) return;
      horizontalVisible = visible;
      const show = visible && !preview;
      chart.applyOptions({ crosshair: { horzLine: { visible: show, labelVisible: show } } });
    },
    refresh,
    dispose() { host.removeEventListener("mousemove", move, true); },
  };
}
