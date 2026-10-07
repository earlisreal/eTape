import { useEffect, useRef, type CSSProperties, type MutableRefObject } from "react";
import type { AckMsg, StopLimitRoutePreview, SubmitOrderArgs } from "../../../wire/contract";
import type { Stores } from "../../../data/registry";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import type { ChartBinding, OrderConfig, PlaceOrderTemplate } from "../../exec/actionTemplate";
import { chartBindingForModifiers, chartConditionalRouteKey, chartConditionalTemplate, chartConditionalOrderWillTrigger, resolveChartConditionalOrder, conditionalRouteLabel, type ChartConditionalTemplate } from "../../exec/resolveChartConditionalOrder";
import { snapOrderMarkerPrice } from "../../../render/chart/orderMarkers";
import type { LinkGroup, LinkGroups } from "../../linkGroups";
import type { Tool } from "../../../render/chart/drawings/interaction";
import type { TvChrome } from "../../../render/chart/tvTheme";

interface Props {
  chrome: TvChrome;
  hostRef: MutableRefObject<HTMLDivElement | null>;
  facadeRef: MutableRefObject<ChartApiFacade | null>;
  stores: Stores;
  linkGroups: LinkGroups;
  group: LinkGroup;
  symbol: string;
  config: OrderConfig;
  configLoaded: boolean;
  activeTool: Tool;
  chooserOpenRef: MutableRefObject<boolean>;
  sendCommand(name: string, args: unknown): Promise<AckMsg>;
  sendQuery(name: string, args: unknown): Promise<unknown>;
}

interface PreviewSnapshot {
  template: ChartConditionalTemplate;
  binding: ChartBinding;
  args: SubmitOrderArgs;
  stopPrice: number;
  limitPrice: number;
  detail: string;
  warning?: string;
  invalid?: string;
  route?: StopLimitRoutePreview;
  needsAck?: boolean;
}
interface Gesture { pointerId: number; x: number; y: number; event: PointerEvent; snapshot: PreviewSnapshot }
interface RouteCache { at: number; route?: StopLimitRoutePreview; pending: Promise<StopLimitRoutePreview | null> | null }

function exactBinding(event: PointerEvent): ChartBinding | undefined {
  return chartBindingForModifiers(event);
}

function inEditableOrUi(target: EventTarget | null): boolean {
  const el = target as Element | null;
  return !!el?.closest?.("[data-drawing-ui], [data-order-markers], input, textarea, select, button, [contenteditable='true']");
}

function timeET(ms: number): string {
  return new Intl.DateTimeFormat("en-US", { timeZone: "America/New_York", hour: "numeric", minute: "2-digit", timeZoneName: "short" }).format(ms);
}

function deadlineCountdown(deadlineMs: number): string {
  const remaining = deadlineMs - Date.now();
  if (remaining <= 0) return " · deadline passed";
  return remaining <= 60_000 ? ` · ${Math.ceil(remaining / 1000)}s remaining` : "";
}

function settingsAckInstruction(venue: string, type: PlaceOrderTemplate["type"]): string {
	return `Live engine-held ${type === "LIMIT_IF_TOUCHED" ? "LIT" : "Stop-Limit"} for ${venue} is not enabled. Open Settings → Orders & hotkeys → Review / enable live accounts.`;
}

export function ChartConditionalOrderEntry(props: Props): JSX.Element {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const statusRef = useRef<HTMLDivElement | null>(null);
  const latest = useRef(props); latest.current = props;
  const pointRef = useRef<{ x: number; y: number; event: PointerEvent } | null>(null);
  const hoverRef = useRef<{ x: number; y: number; event: PointerEvent } | null>(null);
  const gestureRef = useRef<Gesture | null>(null);
  const routeCache = useRef(new Map<string, RouteCache>());
  const consumedBindings = useRef(new Set<ChartBinding>());
  const submittedRef = useRef(false);
  // Keep the overlay imperative: native pointer movement can be >200 Hz and must
  // not rerender ChartPanel or carry quote ticks through React state.
  const announce = (message: string, visible = false) => {
    const el = rootRef.current?.querySelector<HTMLElement>("[data-entry-announcement]");
    if (el) el.textContent = message;
    if (statusRef.current) { statusRef.current.textContent = visible ? message : ""; statusRef.current.style.display = visible ? "block" : "none"; }
  };
  const hide = () => {
    const root = rootRef.current;
    if (root) { root.style.opacity = "0"; root.style.visibility = "hidden"; }
    const host = latest.current.hostRef.current;
    if (host?.dataset.orderEntryMode === "gesture") {
      delete host.dataset.orderEntryMode; delete host.dataset.orderCursorPrice;
      host.title = "";
      latest.current.facadeRef.current?.setOrderCrosshair?.(null);
    }
  };
  const renderSnapshot = (snapshot: PreviewSnapshot, y: number) => {
    const root = rootRef.current;
    const facade = latest.current.facadeRef.current;
    if (!root || !facade) return;
    const detail = root.querySelector<HTMLElement>("[data-entry-detail]");
    const line = root.querySelector<HTMLElement>("[data-entry-line]");
    const label = root.querySelector<HTMLElement>("[data-entry-preview-price]");
    if (line) { line.style.top = `${y}px`; line.style.right = `${facade.priceScaleWidth()}px`; }
    if (label) {
      label.textContent = snapshot.stopPrice.toFixed(snapshot.stopPrice < 1 ? 4 : 2);
      label.style.top = `${Math.max(10, Math.min(y, (facade.paneHeights()[0] ?? 0) - 10))}px`;
      label.style.maxWidth = `${facade.priceScaleWidth()}px`;
    }
    const color = snapshot.template.side === "BUY" || snapshot.template.side === "COVER" ? latest.current.chrome.up : latest.current.chrome.down;
    const instruction = snapshot.needsAck ? settingsAckInstruction(snapshot.args.venue, snapshot.template.type) : "";
    const warning = snapshot.invalid ?? (instruction || snapshot.warning || "");
    root.style.opacity = "1";
    root.style.visibility = "visible";
    root.style.setProperty("--entry-color", color);
    const host = latest.current.hostRef.current;
    if (host) {
      host.dataset.orderEntryMode = "gesture";
      host.dataset.orderCursorPrice = String(snapshot.stopPrice);
      host.dataset.orderCursorY = String(y);
      host.title = `${snapshot.detail}${warning ? `\n${warning}` : ""}`;
      facade.setOrderCrosshair?.(color, pointRef.current?.event);
    }
    root.dataset.price = String(snapshot.stopPrice);
    root.dataset.y = String(y);
    if (detail) {
      detail.textContent = warning;
      detail.style.display = warning ? "block" : "none";
    }
  };

  const activeTemplate = (event: PointerEvent) => {
    const binding = exactBinding(event);
    if (!binding || consumedBindings.current.has(binding)) return null;
    const config = latest.current.config;
    if (!latest.current.configLoaded || latest.current.activeTool !== "select" || latest.current.chooserOpenRef.current
      || latest.current.hostRef.current?.dataset.riskEntryActive || latest.current.hostRef.current?.dataset.orderEntryMode === "order-drag") return null;
    const template = chartConditionalTemplate(config.templates.filter((t): t is PlaceOrderTemplate => t.kind === "place"), binding);
    return template ? { binding, template } : null;
  };

  const requestRoute = (template: ChartConditionalTemplate, symbol: string): Promise<StopLimitRoutePreview | null> => {
    const deferred = template.side === "SELL" && template.sizing.mode === "PositionFraction";
	const key = chartConditionalRouteKey(template.tif, template.session ?? "AUTO", symbol, deferred, template.type);
    const old = routeCache.current.get(key);
    if (old?.route && Date.now() - old.at < 250) return Promise.resolve(old.route);
    if (old?.pending) return old.pending;
    const entry: RouteCache = { at: Date.now(), pending: null };
    entry.pending = latest.current.sendQuery("QueryStopLimitRoute", {
		tif: template.tif, session: template.session ?? "AUTO", symbol, deferredPositionSizing: deferred,
		...(template.type === "LIMIT_IF_TOUCHED" ? { type: template.type } : {}),
    })
      .then((raw) => {
        const route = raw as StopLimitRoutePreview;
        if (route?.route !== "NATIVE" && route?.route !== "ENGINE_HELD" && route?.route !== "UNSUPPORTED") return null;
        entry.route = route; entry.at = Date.now(); return route;
      })
      .catch(() => null)
      .finally(() => { entry.pending = null; });
    routeCache.current.set(key, entry);
    return entry.pending;
  };

  const buildSnapshot = (event: PointerEvent, resolved: { binding: ChartBinding; template: ChartConditionalTemplate }, route?: StopLimitRoutePreview, clickedPrice?: number, routePending = false): PreviewSnapshot | null => {
    const host = latest.current.hostRef.current;
    const facade = latest.current.facadeRef.current;
    if (!host || !facade) return null;
    const rect = host.getBoundingClientRect();
    const x = event.clientX - rect.left, y = event.clientY - rect.top;
    const paneHeight = facade.paneHeights()[0] ?? 0;
    if (x < 0 || y < 0 || x >= rect.width - facade.priceScaleWidth() || y >= paneHeight) return null;
    const price = clickedPrice ?? facade.coordinateToPrice(y);
    if (price == null || !Number.isFinite(price) || price <= 0) return null;
    const stopPrice = snapOrderMarkerPrice(price);
    const stores = latest.current.stores;
    const venue = latest.current.group === null ? "" : latest.current.linkGroups.venueFor(latest.current.group) ?? "";
    const status = stores.exec.status();
    const venueStatus = status?.venues.find((v) => v.venue === venue);
    const account = stores.exec.accounts().find((a) => a.venue === venue);
    const positionQty = stores.exec.positions().filter((p) => p.symbol === latest.current.symbol && p.venue === venue).reduce((sum, p) => sum + p.qty, 0);
    const quote = stores.quote.get(latest.current.symbol);
    const resolvedPlace = resolveChartConditionalOrder(resolved.template, {
      venue, symbol: latest.current.symbol, ...(quote ? { quote } : {}), buyingPower: account?.buyingPower ?? 0, availableCash: account?.availableCash ?? 0,
      positionQty, nowMs: Date.now(), extHoursMarketBufferPct: latest.current.config.extHoursMarketBufferPct ?? 1,
    }, stopPrice);
	const args: SubmitOrderArgs = { ...resolvedPlace.args, ...(route ? { routeExpected: route.route,
		...(resolved.template.type === "LIMIT_IF_TOUCHED" ? { routeDeadlineMs: route.deadlineMs } : {}) } : {}) };
    const routeText = route ? conditionalRouteLabel(route) : "Checking engine route…";
    const env = venueStatus?.broker?.toUpperCase() === "SIM" ? "SIM" : venueStatus?.env?.toUpperCase() || "UNKNOWN";
    const deadline = route?.deadlineMs ? ` · deadline ${timeET(route.deadlineMs)}${deadlineCountdown(route.deadlineMs)}` : "";
	const triggerNow = chartConditionalOrderWillTrigger(resolved.template.side, stopPrice, route, resolved.template.type);
    const noPositionAtTrigger = triggerNow && args.deferredPositionPct !== undefined && venueStatus?.positionDataReady && positionQty <= 0;
    const warning = triggerNow ? noPositionAtTrigger ? "WILL TRIGGER NOW — NO OPEN POSITION" : "WILL TRIGGER NOW" : "";
    const size = args.deferredPositionPct === undefined ? `${args.qty.toLocaleString("en-US")} shares` : `${args.deferredPositionPct}% position · shares determined on trigger`;
	const litCustody = route?.route === "ENGINE_HELD" && resolved.template.type === "LIMIT_IF_TOUCHED"
		? " · Held by eTape — no broker order before activation; trigger source: primary moomoo OpenD Last-Eligible Prints; engine or feed loss pauses evaluation."
		: "";
	const detail = `${size}\n${resolved.template.side} ${resolved.template.type === "LIMIT_IF_TOUCHED" ? "LIT" : "STOP-LIMIT"} · limit ${args.limitPrice.toFixed(args.limitPrice < 1 ? 4 : 2)}\n${args.tif}/${route?.effectiveSession ?? args.session} · ${routeText}${deadline}${litCustody}\n${venue || "no venue"} · ${env}`;
    const invalid = latest.current.group === null ? "Pin a Link Group to enable chart orders."
      : !venue ? "Choose an execution venue for this Link Group."
      : !latest.current.symbol ? "Choose a symbol."
      : !status?.masterArmed ? "Trading is locked. Arm the engine first."
      : !venueStatus?.connected ? "Execution venue is disconnected."
      : venueStatus.reconcilePending ? "Execution venue is reconciling."
      : args.deferredPositionPct !== undefined && venueStatus.flattenPending ? "Venue flatten is awaiting reconciliation."
      : !route && !routePending ? "Engine route preview unavailable."
		: route?.route === "UNSUPPORTED" ? route.reason || `This ${resolved.template.type === "LIMIT_IF_TOUCHED" ? "LIT" : "stop-limit"} session is unsupported.`
      : args.deferredPositionPct !== undefined && !venueStatus.positionDataReady ? "Position cache is reconciling; percentage stop-sell is unavailable."
		: route?.route === "ENGINE_HELD" && route.deadlineMs > 0 && route.deadlineMs <= Date.now() ? "Engine-held order deadline passed."
      : resolvedPlace.errors[0];
    return { template: resolved.template, binding: resolved.binding, args, stopPrice, limitPrice: args.limitPrice, detail, warning,
      ...(invalid ? { invalid } : {}), ...(route ? { route } : {}),
		...(route?.route === "ENGINE_HELD" && venueStatus?.env?.toLowerCase() === "live" &&
			(resolved.template.type === "LIMIT_IF_TOUCHED" ? !venueStatus.heldLimitIfTouchedAcknowledged : !venueStatus.heldStopLimitAcknowledged) ? { needsAck: true } : {}) };
  };

  const updateFromPoint = async () => {
    const point = pointRef.current;
    if (!point) return;
    const resolved = activeTemplate(point.event);
    if (!resolved || !document.hasFocus() || inEditableOrUi(point.event.target)) { hide(); return; }
    const pendingSnapshot = buildSnapshot(point.event, resolved, undefined, undefined, true);
    const pendingY = pendingSnapshot && latest.current.facadeRef.current?.priceToCoordinate(pendingSnapshot.stopPrice);
    if (!pendingSnapshot || pendingY == null) { hide(); return; }
    renderSnapshot(pendingSnapshot, pendingY);
    const route = await requestRoute(resolved.template, latest.current.symbol);
    if (pointRef.current !== point || !activeTemplate(point.event)) return;
    const snapshot = buildSnapshot(point.event, resolved, route ?? undefined);
    const host = latest.current.hostRef.current;
    const facade = latest.current.facadeRef.current;
    if (!snapshot || !host || !facade) { hide(); return; }
    const y = facade.priceToCoordinate(snapshot.stopPrice);
    if (y == null || !Number.isFinite(y)) { hide(); return; }
    renderSnapshot(snapshot, y);
    if (gestureRef.current && Math.hypot(point.event.clientX - gestureRef.current.x, point.event.clientY - gestureRef.current.y) >= 4) {
      gestureRef.current = null;
      announce("Chart order canceled — pointer moved. Click without dragging to place.");
      hide();
    } else if (gestureRef.current) {
      gestureRef.current.snapshot = snapshot;
    }
  };

  useEffect(() => {
    const host = latest.current.hostRef.current;
    if (!host) return;
    let frame = 0;
    const schedule = (event: PointerEvent) => {
      pointRef.current = { x: event.clientX, y: event.clientY, event };
      hoverRef.current = pointRef.current;
      if (frame) return;
      frame = requestAnimationFrame(() => { frame = 0; void updateFromPoint(); });
    };
    const down = (event: PointerEvent) => {
      if (event.button !== 0 || gestureRef.current || inEditableOrUi(event.target) || !document.hasFocus()) return;
      const resolved = activeTemplate(event);
      if (!resolved) return;
      const deferred = resolved.template.side === "SELL" && resolved.template.sizing.mode === "PositionFraction";
      const routeEntry = routeCache.current.get(chartConditionalRouteKey(resolved.template.tif, resolved.template.session ?? "AUTO", latest.current.symbol, deferred, resolved.template.type));
      const route = routeEntry && Date.now() - routeEntry.at < 250 ? routeEntry.route : undefined;
      const snapshot = buildSnapshot(event, resolved, route, undefined, true);
      if (!snapshot || snapshot.invalid) return;
      if (snapshot.needsAck) {
        consumedBindings.current.add(resolved.binding);
        event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation();
		announce(settingsAckInstruction(snapshot.args.venue, resolved.template.type));
        return;
      }
      const hostNow = latest.current.hostRef.current;
      if (!hostNow) return;
      gestureRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, event, snapshot };
      consumedBindings.current.add(resolved.binding);
      submittedRef.current = false;
      try { hostNow.setPointerCapture(event.pointerId); } catch { /* window listeners still complete the gesture */ }
      event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation();
    };
    const up = async (event: PointerEvent) => {
      const gesture = gestureRef.current;
      if (!gesture || gesture.pointerId !== event.pointerId || submittedRef.current) return;
      event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation();
      if (Math.hypot(event.clientX - gesture.x, event.clientY - gesture.y) >= 4) { gestureRef.current = null; hide(); return; }
      submittedRef.current = true;
      // A cold or expired preview must refresh the route, not discard the click.
      const route = await requestRoute(gesture.snapshot.template, gesture.snapshot.args.symbol);
      if (gestureRef.current !== gesture) return;
      gestureRef.current = null;
      const snapshot = buildSnapshot(gesture.event, gesture.snapshot, route ?? undefined, gesture.snapshot.stopPrice);
      if (!snapshot || !document.hasFocus()) { hide(); return; }
      if (snapshot.invalid || snapshot.needsAck) {
        renderSnapshot(snapshot, latest.current.facadeRef.current?.priceToCoordinate(snapshot.stopPrice) ?? gesture.y);
        announce(snapshot.invalid ?? settingsAckInstruction(snapshot.args.venue, snapshot.template.type));
        return;
      }
      announce(`Submitting ${snapshot.detail}`);
      void latest.current.sendCommand("SubmitOrder", snapshot.args).then((ack) => {
        if (ack.ambiguous) announce("Order outcome unknown. Verify Open Orders before retrying.", true);
        else if (ack.status === "accepted") announce(`Engine accepted order ${ack.orderId ?? ""}; waiting for order state.`);
        else announce(`Order blocked: ${ack.reason ?? "unknown reason"}.`, true);
      }).catch(() => announce("Order outcome unknown. Verify Open Orders before retrying.", true));
      pointRef.current = null;
      hide();
    };
    const cancel = (message: string, event?: Event) => {
      if (!gestureRef.current && host.dataset.orderEntryMode !== "gesture") return;
      const binding = pointRef.current && exactBinding(pointRef.current.event);
      if (binding) consumedBindings.current.add(binding);
      gestureRef.current = null;
      pointRef.current = null;
      if (event) { event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation(); }
      hide(); announce(message);
    };
    const cancelOnRight = (event: PointerEvent) => { if (event.button === 2) cancel("Chart order canceled.", event); };
    const cancelOnContext = (event: MouseEvent) => { if (gestureRef.current) cancel("Chart order canceled.", event); };
    const refreshModifiers = (event: KeyboardEvent) => {
      const point = hoverRef.current;
      if (!point) return;
      const next = new PointerEvent("pointermove", { clientX: point.x, clientY: point.y,
        ctrlKey: event.ctrlKey, altKey: event.altKey, shiftKey: event.shiftKey });
      Object.defineProperty(next, "target", { value: point.event.target });
      schedule(next);
    };
    const cancelOnKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") cancel("Chart order canceled.", event);
      else if (["Shift", "Control", "Alt"].includes(event.key)) refreshModifiers(event);
    };
    const onKeyUp = (event: KeyboardEvent) => {
      if (!event.ctrlKey && !event.altKey && !event.shiftKey) { consumedBindings.current.clear(); hide(); }
      refreshModifiers(event);
    };
    const onLeave = () => { hoverRef.current = null; if (!gestureRef.current) { pointRef.current = null; hide(); } else cancel("Chart order canceled — pointer left the chart."); };
    const onBlur = () => cancel("Chart order canceled — window lost focus.");
    const unsubscribeQuote = latest.current.stores.quote.subscribe(() => {
      if (pointRef.current) schedule(pointRef.current.event);
    });
    const deadlineTimer = window.setInterval(() => {
      if (pointRef.current) schedule(pointRef.current.event);
    }, 1000);
    host.addEventListener("pointermove", schedule, true);
    host.addEventListener("pointerdown", down, true);
    host.addEventListener("pointerleave", onLeave);
    window.addEventListener("pointerup", up, true);
    window.addEventListener("pointercancel", onLeave, true);
    window.addEventListener("pointerdown", cancelOnRight, true);
    window.addEventListener("contextmenu", cancelOnContext, true);
    window.addEventListener("keydown", cancelOnKey, true);
    window.addEventListener("keyup", onKeyUp, true);
    window.addEventListener("blur", onBlur);
    return () => {
      host.removeEventListener("pointermove", schedule, true); host.removeEventListener("pointerdown", down, true);
      host.removeEventListener("pointerleave", onLeave); window.removeEventListener("pointerup", up, true);
      window.removeEventListener("pointercancel", onLeave, true); window.removeEventListener("pointerdown", cancelOnRight, true);
      window.removeEventListener("contextmenu", cancelOnContext, true); window.removeEventListener("keydown", cancelOnKey, true);
      window.removeEventListener("keyup", onKeyUp, true); window.removeEventListener("blur", onBlur);
      unsubscribeQuote(); window.clearInterval(deadlineTimer);
      if (frame) cancelAnimationFrame(frame);
    };
  });

  useEffect(() => {
    hide();
    gestureRef.current = null;
    pointRef.current = null;
    hoverRef.current = null;
    routeCache.current.clear();
    announce("");
    return () => { gestureRef.current = null; pointRef.current = null; };
  }, [props.group, props.symbol, props.linkGroups.venueFor(props.group), props.config.templates, props.activeTool]);

  return <><div ref={rootRef} data-testid="chart-order-entry-preview" style={{ position: "absolute", inset: 0, opacity: 0, visibility:"hidden",
    zIndex: 9, pointerEvents: "none", overflow: "hidden", "--entry-color": "#34c6dc" } as CSSProperties}>
    <div data-entry-line style={{ position: "absolute", left: 0, borderTop: "2px dashed var(--entry-color)" }} />
    <div data-entry-preview-price aria-hidden="true" style={{ position: "absolute", right: 0, transform: "translateY(-50%)", height: 20,
      boxSizing: "border-box", padding: "0 4px", background: "#0c1017", color: "var(--entry-color)",
      font: "600 10px/20px ui-monospace,monospace", whiteSpace: "nowrap" }} />
    <div data-entry-detail="true" role="status" style={{ position: "absolute", left: 10, bottom:35, maxWidth: "75%", padding: "3px 5px",
      background: "rgba(12,16,23,.94)",color:"#ff9d72", font: "600 10px ui-monospace,monospace",whiteSpace:"pre-wrap" }} />
    <span data-entry-announcement="true" aria-live="polite" style={{ position: "absolute", width: 1, height: 1, overflow: "hidden", clipPath: "inset(50%)" }} />
  </div><div ref={statusRef} data-entry-status role="status" style={{display:"none",position:"absolute",left:10,bottom:35,zIndex:9,maxWidth:"75%",padding:"3px 5px",background:"#0c1017",color:"#ff9d72",fontSize:11,pointerEvents:"none"}} /></>;
}
