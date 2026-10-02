import { useEffect, useRef, type CSSProperties, type MutableRefObject } from "react";
import type { AckMsg, StopLimitRoutePreview, SubmitOrderArgs } from "../../../wire/contract";
import type { Stores } from "../../../data/registry";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import type { ChartBinding, OrderConfig, PlaceOrderTemplate } from "../../exec/actionTemplate";
import { chartBindingForModifiers, chartConditionalRouteKey, chartConditionalTemplate, chartConditionalOrderWillTrigger, resolveChartConditionalOrder, conditionalRouteLabel, type ChartConditionalTemplate } from "../../exec/resolveChartConditionalOrder";
import { snapOrderMarkerPrice } from "../../../render/chart/orderMarkers";
import type { LinkGroup, LinkGroups } from "../../linkGroups";
import type { Tool } from "../../../render/chart/drawings/interaction";

interface Props {
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
  invalid?: string;
  route?: StopLimitRoutePreview;
  needsAck?: boolean;
}
interface Gesture { pointerId: number; x: number; y: number; snapshot: PreviewSnapshot }
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
  const latest = useRef(props); latest.current = props;
  const pointRef = useRef<{ x: number; y: number; event: PointerEvent } | null>(null);
  const gestureRef = useRef<Gesture | null>(null);
  const routeCache = useRef(new Map<string, RouteCache>());
  const consumedBindings = useRef(new Set<ChartBinding>());
  const submittedRef = useRef(false);
  // Keep the overlay imperative: native pointer movement can be >200 Hz and must
  // not rerender ChartPanel or carry quote ticks through React state.
  const announce = (message: string) => {
    const el = rootRef.current?.querySelector<HTMLElement>("[data-entry-announcement]");
    if (el) el.textContent = message;
  };
  const hide = () => {
    const root = rootRef.current;
    if (root) { root.style.opacity = "0"; root.style.pointerEvents = "none"; }
  };
  const renderSnapshot = (snapshot: PreviewSnapshot, y: number) => {
    const root = rootRef.current;
    const facade = latest.current.facadeRef.current;
    if (!root || !facade) return;
    const line = root.querySelector<HTMLElement>("[data-entry-line]");
    const chip = root.querySelector<HTMLElement>("[data-entry-chip]");
    const detail = root.querySelector<HTMLElement>("[data-entry-detail]");
    const color = snapshot.invalid || snapshot.needsAck ? "#ff6877" : "#34c6dc";
    root.style.opacity = "1";
    root.style.setProperty("--entry-color", color);
    root.style.setProperty("--entry-y", `${Math.round(y)}px`);
    if (line) { line.style.top = `${Math.round(y)}px`; line.style.right = `${facade.priceScaleWidth()}px`; }
    if (chip) { chip.textContent = `${snapshot.stopPrice.toFixed(snapshot.stopPrice < 1 ? 4 : 2)}${snapshot.invalid || snapshot.needsAck ? " !" : ""}`; chip.style.borderColor = color; chip.style.color = color; }
    if (detail) {
      detail.style.borderColor = color; detail.style.color = color;
      const instruction = snapshot.needsAck ? settingsAckInstruction(snapshot.args.venue, snapshot.template.type) : "";
      detail.textContent = snapshot.invalid ?? (instruction ? `${snapshot.detail}\n${instruction}` : snapshot.detail);
      detail.title = instruction ? `${snapshot.detail}\n${instruction}` : snapshot.detail;
      detail.style.whiteSpace = instruction ? "pre-wrap" : "nowrap";
      detail.style.textOverflow = instruction ? "clip" : "ellipsis";
      detail.style.overflow = instruction ? "visible" : "hidden";
    }
  };

  const activeTemplate = (event: PointerEvent) => {
    const binding = exactBinding(event);
    if (!binding || consumedBindings.current.has(binding)) return null;
    const config = latest.current.config;
    if (!latest.current.configLoaded || latest.current.activeTool !== "select" || latest.current.chooserOpenRef.current) return null;
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

  const buildSnapshot = (event: PointerEvent, resolved: { binding: ChartBinding; template: ChartConditionalTemplate }, route?: StopLimitRoutePreview): PreviewSnapshot | null => {
    const host = latest.current.hostRef.current;
    const facade = latest.current.facadeRef.current;
    if (!host || !facade) return null;
    const rect = host.getBoundingClientRect();
    const x = event.clientX - rect.left, y = event.clientY - rect.top;
    const paneHeight = facade.paneHeights()[0] ?? 0;
    if (x < 0 || y < 0 || x >= rect.width - facade.priceScaleWidth() || y >= paneHeight) return null;
    const price = facade.coordinateToPrice(y);
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
    const trigger = triggerNow ? noPositionAtTrigger ? " · WILL TRIGGER NOW — NO OPEN POSITION" : " · WILL TRIGGER NOW" : "";
    const size = args.deferredPositionPct === undefined ? args.qty : `${args.deferredPositionPct}% position`;
	const litCustody = route?.route === "ENGINE_HELD" && resolved.template.type === "LIMIT_IF_TOUCHED"
		? " · Held by eTape — no broker order before activation; trigger source: primary moomoo OpenD Last-Eligible Prints; engine or feed loss pauses evaluation."
		: "";
	const detail = `${resolved.template.side} ${size} ${latest.current.symbol} · ${resolved.template.type === "LIMIT_IF_TOUCHED" ? "LIT trigger" : "stop"} ${stopPrice.toFixed(stopPrice < 1 ? 4 : 2)} → limit ${args.limitPrice.toFixed(args.limitPrice < 1 ? 4 : 2)} · ${args.tif}/${route?.effectiveSession ?? args.session} · ${routeText}${trigger}${deadline}${litCustody} · ${venue || "no venue"} · ${env}`;
    const invalid = latest.current.group === null ? "Pin a Link Group to enable chart orders."
      : !venue ? "Choose an execution venue for this Link Group."
      : !latest.current.symbol ? "Choose a symbol."
      : !status?.masterArmed ? "Trading is locked. Arm the engine first."
      : !venueStatus?.connected ? "Execution venue is disconnected."
      : venueStatus.reconcilePending ? "Execution venue is reconciling."
      : args.deferredPositionPct !== undefined && venueStatus.flattenPending ? "Venue flatten is awaiting reconciliation."
      : !route ? "Engine route preview unavailable."
		: route.route === "UNSUPPORTED" ? route.reason || `This ${resolved.template.type === "LIMIT_IF_TOUCHED" ? "LIT" : "stop-limit"} session is unsupported.`
      : args.deferredPositionPct !== undefined && !venueStatus.positionDataReady ? "Position cache is reconciling; percentage stop-sell is unavailable."
		: route.route === "ENGINE_HELD" && route.deadlineMs > 0 && route.deadlineMs <= Date.now() ? "Engine-held order deadline passed."
      : resolvedPlace.errors[0];
    return { template: resolved.template, binding: resolved.binding, args, stopPrice, limitPrice: args.limitPrice, detail,
      ...(invalid ? { invalid } : {}), ...(route ? { route } : {}),
		...(route?.route === "ENGINE_HELD" && venueStatus?.env?.toLowerCase() === "live" &&
			(resolved.template.type === "LIMIT_IF_TOUCHED" ? !venueStatus.heldLimitIfTouchedAcknowledged : !venueStatus.heldStopLimitAcknowledged) ? { needsAck: true } : {}) };
  };

  const updateFromPoint = async () => {
    const point = pointRef.current;
    if (!point) return;
    const resolved = activeTemplate(point.event);
    if (!resolved || !document.hasFocus() || inEditableOrUi(point.event.target)) { hide(); return; }
    const route = await requestRoute(resolved.template, latest.current.symbol);
    if (pointRef.current !== point) return;
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
      if (frame) return;
      frame = requestAnimationFrame(() => { frame = 0; void updateFromPoint(); });
    };
    const down = (event: PointerEvent) => {
      if (event.button !== 0 || gestureRef.current || inEditableOrUi(event.target) || !document.hasFocus()) return;
      const resolved = activeTemplate(event);
      if (!resolved) return;
      const deferred = resolved.template.side === "SELL" && resolved.template.sizing.mode === "PositionFraction";
      const routeEntry = routeCache.current.get(chartConditionalRouteKey(resolved.template.tif, resolved.template.session ?? "AUTO", latest.current.symbol, deferred, resolved.template.type));
      const route = routeEntry?.route;
      if (!route || Date.now() - routeEntry.at >= 250) { schedule(event); return; }
      const snapshot = buildSnapshot(event, resolved, route);
      if (!snapshot || snapshot.invalid) return;
      if (snapshot.needsAck) {
        consumedBindings.current.add(resolved.binding);
        event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation();
		announce(settingsAckInstruction(snapshot.args.venue, resolved.template.type));
        return;
      }
      const hostNow = latest.current.hostRef.current;
      if (!hostNow) return;
      gestureRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, snapshot };
      consumedBindings.current.add(resolved.binding);
      submittedRef.current = false;
      try { hostNow.setPointerCapture(event.pointerId); } catch { /* window listeners still complete the gesture */ }
      event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation();
    };
    const up = (event: PointerEvent) => {
      const gesture = gestureRef.current;
      if (!gesture || gesture.pointerId !== event.pointerId) return;
      gestureRef.current = null;
      event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation();
      if (Math.hypot(event.clientX - gesture.x, event.clientY - gesture.y) >= 4 || submittedRef.current) { hide(); return; }
      submittedRef.current = true;
      const snapshot = gesture.snapshot;
      announce(`Submitting ${snapshot.detail}`);
      void latest.current.sendCommand("SubmitOrder", snapshot.args).then((ack) => {
        if (ack.ambiguous) announce("Order outcome unknown. Verify Open Orders before retrying.");
        else if (ack.status === "accepted") announce(`Engine accepted order ${ack.orderId ?? ""}; waiting for order state.`);
        else announce(`Order blocked: ${ack.reason ?? "unknown reason"}.`);
      }).catch(() => announce("Order outcome unknown. Verify Open Orders before retrying."));
      hide();
    };
    const cancel = (message: string, event?: Event) => {
      if (!gestureRef.current) return;
      gestureRef.current = null;
      if (event) { event.preventDefault(); event.stopPropagation(); event.stopImmediatePropagation(); }
      hide(); announce(message);
    };
    const cancelOnRight = (event: PointerEvent) => { if (event.button === 2) cancel("Chart order canceled.", event); };
    const cancelOnContext = (event: MouseEvent) => { if (gestureRef.current) cancel("Chart order canceled.", event); };
    const cancelOnKey = (event: KeyboardEvent) => { if (event.key === "Escape") cancel("Chart order canceled.", event); };
    const onKeyUp = (event: KeyboardEvent) => { if (!event.ctrlKey && !event.altKey && !event.shiftKey) consumedBindings.current.clear(); };
    const onLeave = () => { if (!gestureRef.current) { pointRef.current = null; hide(); } else cancel("Chart order canceled — pointer left the chart."); };
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
    routeCache.current.clear();
  }, [props.group, props.symbol, props.linkGroups.venueFor(props.group), props.config.templates]);

  return <div ref={rootRef} data-testid="chart-order-entry-preview" style={{ position: "absolute", inset: 0, opacity: 0,
    zIndex: 9, pointerEvents: "none", overflow: "hidden", "--entry-color": "#34c6dc" } as CSSProperties}>
    <div data-entry-line="true" style={{ position: "absolute", left: 0, right: 0, height: 2,
      background: "repeating-linear-gradient(90deg,var(--entry-color) 0 8px,transparent 8px 13px)" }} />
    <div data-entry-chip="true" style={{ position: "absolute", right: 0, top: "var(--entry-y)", transform: "translateY(-50%)",
      padding: "2px 5px", border: "1px solid #34c6dc", borderRadius: 3, background: "#0c1017", font: "600 10px ui-monospace,monospace" }} />
    <div data-entry-detail="true" style={{ position: "absolute", left: 10, top: 10, maxWidth: "75%", padding: "5px 7px", border: "1px solid #34c6dc",
      borderRadius: 3, background: "rgba(12,16,23,.94)", font: "600 10px ui-monospace,monospace", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }} />
    <span data-entry-announcement="true" aria-live="polite" style={{ position: "absolute", width: 1, height: 1, overflow: "hidden", clipPath: "inset(50%)" }} />
  </div>;
}
