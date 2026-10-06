import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MutableRefObject, type PointerEvent as ReactPointerEvent } from "react";
import type { AckMsg, Order, ReplaceOrderArgs } from "../../../wire/contract";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { chartOrderMarkers, nearestPriceLines, orderActionPending, orderMarkerChipYs, snapOrderMarkerPrice, type ChartOrderMarker } from "../../../render/chart/orderMarkers";
import type { OrderConfig, PlaceOrderTemplate } from "../../exec/actionTemplate";
import { chartBindingForModifiers, chartConditionalTemplate } from "../../exec/resolveChartConditionalOrder";
import type { Tool } from "../../../render/chart/drawings/interaction";
import {riskEntrySize} from "../../exec/riskEntry";
import {resolveLimitCushionPrice} from "../../exec/priceSource";
import type { TvChrome } from "../../../render/chart/tvTheme";

type Pending = { kind: "replace" | "cancel"; price?: number; stop: boolean; confirmedPrice?: number; outcome: "requested" | "unknown" };
type Drag = { id: string; pointerId: number; marker: ChartOrderMarker; startX: number; startY: number; startPrice: number; price: number; moved: boolean; entryPhase:string; previewQty:number };

interface Props {
  chrome: TvChrome;
  availableCash?: number;
  buyingPower?: number;
  orders: Iterable<Order>;
  venue: string;
  symbol: string;
  pinned: boolean;
  sendCommand(name: string, args: unknown): Promise<AckMsg>;
  hostRef: MutableRefObject<HTMLDivElement | null>;
  facadeRef: MutableRefObject<ChartApiFacade | null>;
  rightAxisWidth: number;
  layoutRef: MutableRefObject<() => void>;
  chooserOpenRef: MutableRefObject<boolean>;
  config?: OrderConfig;
  activeTool?: Tool;
}

interface Group { key: string; price: number; kind: ChartOrderMarker["kind"]; markers: ChartOrderMarker[]; pending?: Pending }

function priceText(price: number): string { return price < 1 ? price.toFixed(4) : price.toFixed(2); }
function orderTitle(marker: ChartOrderMarker): string {
  const type = marker.order.type === "LIMIT_IF_TOUCHED" ? "LIT" : marker.kind === "limit" ? "LIMIT" : "STOP-LIMIT";
  return `${marker.order.side} ${marker.order.leavesQty.toLocaleString("en-US")} ${type}${marker.order.riskEntry ? ` · Risk Entry ${marker.phase}` : marker.order.riskEntryId ? ` · Linked Protection ${marker.phase}` : ""}`;
}

export function ChartOrderMarkers(props: Props): JSX.Element {
  const orders=useMemo(()=>Array.from(props.orders),[props.orders]);
  const markers = useMemo(() => chartOrderMarkers(orders, props.venue, props.symbol, props.pinned),
    [orders, props.venue, props.symbol, props.pinned]);
  const [pending, setPending] = useState<Map<string, Pending>>(() => new Map());
  const groups = useMemo(() => {
    const map = new Map<string, Group>();
    for (const marker of markers) {
      const action = orderActionPending(marker) ?? pending.get(marker.order.id);
      const waiting = action?.kind === "replace" && action.price !== marker.price ? action : undefined;
      const price = waiting ? waiting.price! : marker.price;
      const key = `${marker.order.side}:${marker.kind}:${price.toFixed(4)}${waiting ? `:pending:${marker.order.id}` : ""}`;
      const group = map.get(key) ?? { key, kind: marker.kind, price, markers: [], ...(waiting ? { pending: waiting } : {}) };
      group.markers.push(waiting ? { ...marker, price, draggable: false } : marker); map.set(key, group);
    }
    return [...map.values()];
  }, [markers, pending]);
  const latest = useRef(props);
  latest.current = {...props,orders};
  const drag = useRef<Drag | null>(null);
  const [chooser, setChooser] = useState<string | null>(null);
  const [lineChooser, setLineChooser] = useState<string[] | null>(null);
  const chooserLatest = useRef(false); chooserLatest.current = chooser !== null || lineChooser !== null;
  props.chooserOpenRef.current = chooserLatest.current;
  const groupsLatest = useRef(groups); groupsLatest.current = groups;
  const [announcement, setAnnouncement] = useState("");

  const riskProposal=(marker:ChartOrderMarker,price:number):{detail:string;qty:number;plannedQty?:number;aboveBudget?:boolean;limit?:number}=>{
    const all=Array.from(latest.current.orders);
    const entry=marker.order.riskEntry ? marker.order : all.find(o=>o.id===marker.order.riskEntryId);
    const risk=entry?.riskEntry,stop=risk && all.find(o=>o.id===risk.stopId);
    if(!entry || !risk || !stop)return {detail:"",qty:0};
    let buy=entry.limitPrice,sell=stop.limitPrice;
    const trigger=marker.kind!=="limit";
    const cushion=marker.order.side === "BUY"?risk.buyCushion:risk.sellCushion;
    const limit=trigger?resolveLimitCushionPrice(marker.order.side,price,cushion.value,cushion.unit === "%"?"%":"$"):price;
    if(marker.order.side === "BUY")buy=limit;else sell=limit;
    const locked=!!entry.held?.childClientId;
    const sized=riskEntrySize({kind:"risk",id:entry.id,label:"Risk",mode:risk.mode === "CashPct" ? "CashPct" : risk.mode === "BuyingPowerPct" ? "BuyingPowerPct" : "Dollar",value:risk.budget,buyCushion:risk.buyCushion,sellCushion:risk.sellCushion},
      marker.order.id===entry.id && trigger?price:entry.stopPrice,marker.order.id!==entry.id && trigger?price:stop.stopPrice,latest.current.availableCash ?? 0,latest.current.buyingPower ?? 0,risk.budget);
    const qty=locked?entry.qty:sized.qty;
    const aboveBudget=qty*Math.max(0,buy-sell)>risk.budget+0.005;
    return {qty,plannedQty:entry.qty,limit,aboveBudget,detail:`${qty} shares${aboveBudget?" · ABOVE ORIGINAL BUDGET":""}`};
  };
  const sideText = (marker: ChartOrderMarker) => marker.order.side === "BUY" || marker.order.side === "COVER" ? "B" : "S";
  const tooltip = (marker: ChartOrderMarker, price = marker.price, proposed = false) => {
    const risk = riskProposal(marker, price);
    const quantity = marker.order.deferredPositionPct !== undefined && marker.kind !== "limit"
      ? `${marker.order.deferredPositionPct}% position · shares determined on trigger`
      : (proposed && risk.detail) || (marker.order.riskEntryId && !marker.order.leavesQty)
        ? `${(proposed ? risk.qty : risk.plannedQty ?? 0).toLocaleString("en-US")} planned shares`
        : `${marker.order.leavesQty.toLocaleString("en-US")} shares`;
    const limit = risk.limit ?? (marker.kind === "limit" ? price : marker.order.limitPrice);
    const type = marker.kind === "limit" ? "LIMIT" : marker.kind === "limit-if-touched" ? "LIT" : "STOP-LIMIT";
    return `${quantity}\n${marker.order.side} ${type} · limit ${priceText(limit)}\n${marker.order.held && marker.kind !== "limit" ? "Held by eTape · " : ""}${marker.order.riskEntry ? "Risk Entry · " : marker.order.riskEntryId ? "Linked Protection · " : ""}${marker.phase || marker.order.status}`;
  };

  const layout = () => {
    const host = latest.current.hostRef.current;
    const facade = latest.current.facadeRef.current;
    if (!host || !facade) return;
    const paneHeight = facade.paneHeights?.()[0] ?? host.getBoundingClientRect().height;
    const rows = [...host.querySelectorAll<HTMLElement>("[data-order-group]")].filter(el => {
      const y = facade.priceToCoordinate(Number(el.dataset.proposedPrice ?? el.dataset.price));
      const visible = y != null && Number.isFinite(y) && y >= 0 && y <= paneHeight;
      el.style.display = visible ? "block" : "none";
      return visible;
    });
    const ys = rows.map(el => facade.priceToCoordinate(Number(el.dataset.price)) ?? -10000);
    const chipYs = orderMarkerChipYs(ys, paneHeight);
    for (const [index, el] of rows.entries()) {
      if (drag.current && el.dataset.orderIds?.split(",").includes(drag.current.id)) continue;
      const price = Number(el.dataset.price);
      const y = facade.priceToCoordinate(price);
      if (y != null && Number.isFinite(y)) el.style.top = `${Math.round(y)}px`;
      const chip = el.querySelector<HTMLElement>("[data-order-chip]");
      if (chip && y != null) chip.style.top = `${Math.round(chipYs[index] - y - 10)}px`;
      const confirmedLine = el.querySelector<HTMLElement>("[data-order-confirmed-line]");
      if (confirmedLine) {
        const confirmedY = facade.priceToCoordinate(Number(el.dataset.confirmedPrice));
        confirmedLine.style.top = confirmedY != null && Number.isFinite(confirmedY) && y != null ? `${Math.round(confirmedY - y)}px` : "-10000px";
      }
    }
  };

  useEffect(() => {
    props.layoutRef.current = layout;
    layout();
    return () => { if (props.layoutRef.current === layout) props.layoutRef.current = () => {}; };
  });

  useEffect(() => {
    setPending((current) => {
      const next = new Map(current);
      for (const [id, action] of next) {
        const marker = markers.find((m) => m.order.id === id);
        if (!marker) { next.delete(id); continue; }
        if (action.kind === "replace" && action.price === (action.stop ? marker.order.stopPrice : marker.order.limitPrice)) next.delete(id);
        if (action.kind === "cancel" && marker.order.status !== "SUBMITTED" && marker.order.status !== "ACCEPTED" && marker.order.status !== "PARTIALLY_FILLED") next.delete(id);
      }
      return next.size === current.size ? current : next;
    });
    requestAnimationFrame(layout);
  }, [markers]);

  const paintProposal = (id: string, price: number, highlight: boolean) => {
    const host = latest.current.hostRef.current;
    const facade = latest.current.facadeRef.current;
    const row = [...(host?.querySelectorAll<HTMLElement>("[data-order-group]") ?? [])]
      .find((el) => el.dataset.orderIds?.split(",").includes(id));
    if (!row || !facade) return;
    const y = facade.priceToCoordinate(price);
    if (y != null && Number.isFinite(y)) row.style.top = `${Math.round(y)}px`;
    const confirmedLine = row.querySelector<HTMLElement>("[data-order-confirmed-line]");
    const confirmedPrice = drag.current?.startPrice ?? Number(row.dataset.price);
    const confirmedY = facade.priceToCoordinate(confirmedPrice);
    if (confirmedLine && confirmedY != null && Number.isFinite(confirmedY)) {
      confirmedLine.style.display = "block"; confirmedLine.style.top = `${Math.round(confirmedY - (y ?? confirmedY))}px`;
    }
    row.dataset.proposedPrice = String(price);
    const color = highlight ? "#fff29a" : "transparent";
    const chip = row.querySelector<HTMLElement>("[data-order-price]");
    if (chip && drag.current) {
      chip.textContent = `${sideText(drag.current.marker)} ${priceText(price)}`;
      chip.title = tooltip(drag.current.marker, price, highlight);
      chip.style.borderColor = color;
      const proposal = riskProposal(drag.current.marker, price);
      drag.current.previewQty = proposal.qty;
      const status = row.querySelector<HTMLElement>("[data-order-status]");
      if (status) { status.textContent = proposal.aboveBudget ? "ABOVE ORIGINAL BUDGET" : status.dataset.status ?? ""; status.style.display = status.textContent ? "block" : "none"; }
    }
  };

  const cancelDrag = () => {
    const current = drag.current;
    if (!current) return;
    paintProposal(current.id, current.startPrice, false);
    const host = latest.current.hostRef.current;
    const row = [...(host?.querySelectorAll<HTMLElement>("[data-order-group]") ?? [])]
      .find((el) => el.dataset.orderIds?.split(",").includes(current.id));
    if (row) { delete row.dataset.proposedPrice; row.querySelector<HTMLElement>("[data-order-price]")?.style.removeProperty("border-color");
      const confirmed = row.querySelector<HTMLElement>("[data-order-confirmed-line]"); if (confirmed) confirmed.style.display = "none"; }
    drag.current = null;
    const hostMode = latest.current.hostRef.current;
    if (hostMode?.dataset.orderEntryMode === "order-drag") { delete hostMode.dataset.orderEntryMode; hostMode.style.cursor = ""; hostMode.title = ""; latest.current.facadeRef.current?.setOrderCrosshair?.(null); latest.current.facadeRef.current?.setPanZoomEnabled?.(!latest.current.activeTool || latest.current.activeTool === "select"); }
    setAnnouncement("Price change canceled.");
  };

  const sendReplace = async (marker: ChartOrderMarker, price: number, observedEntryPhase?:string,approvedQty?:number) => {
    const stop = marker.kind !== "limit";
    const entry=marker.order.riskEntry ? marker.order : Array.from(latest.current.orders).find(o=>o.id===marker.order.riskEntryId);
    const proposal=riskProposal(marker,price);
    const waitingRisk=!!entry?.riskEntry && (observedEntryPhase ?? (entry.held?.childClientId?"ACTIVATED":"WAITING"))==="WAITING";
    if(waitingRisk && !(approvedQty ?? proposal.qty)){setAnnouncement("Risk preview unavailable or zero shares; no modification sent.");return;}
    const args: ReplaceOrderArgs = {
      venue: marker.order.venue, orderId: marker.order.id,
      qty: waitingRisk ? approvedQty ?? proposal.qty : 0, // waiting pairs cap the approved preview; activated orders preserve quantity.
      limitPrice: stop ? marker.order.limitPrice : price,
      stopPrice: stop ? price : marker.order.stopPrice,
      ...((marker.order.riskEntry || marker.order.riskEntryId) ? {expectedHeldPhase:marker.phase,expectedRiskEntryPhase:observedEntryPhase ?? (entry?.held?.childClientId?"ACTIVATED":"WAITING")} : {}),
    };
    setPending((current) => new Map(current).set(marker.order.id, { kind: "replace", price, confirmedPrice: marker.price, stop, outcome: "requested" }));
    setAnnouncement(`Modify requested at ${priceText(price)}.${proposal.detail ? ` ${proposal.detail}`:""}`);
    try {
      const ack = await latest.current.sendCommand("ReplaceOrder", args);
      if (ack.ambiguous) {
        setPending((current) => new Map(current).set(marker.order.id, { kind: "replace", price, confirmedPrice: marker.price, stop, outcome: "unknown" }));
        setAnnouncement("Modify outcome unknown. Verify the order before another change.");
      } else if (ack.status !== "accepted") {
        setPending((current) => { const next = new Map(current); next.delete(marker.order.id); return next; });
        setAnnouncement(`Modify rejected: ${ack.reason ?? "unknown reason"}.`);
      }
    } catch {
      setPending((current) => new Map(current).set(marker.order.id, { kind: "replace", price, confirmedPrice: marker.price, stop, outcome: "unknown" }));
      setAnnouncement("Modify outcome unknown. Verify the order before another change.");
    }
  };

  const startDrag = (event: ReactPointerEvent<HTMLElement> | PointerEvent, marker: ChartOrderMarker) => {
    if (!marker.draggable || event.button !== 0) return;
    event.preventDefault(); event.stopPropagation();
    drag.current = { id: marker.order.id, pointerId: event.pointerId, marker, startX: event.clientX, startY: event.clientY,
      startPrice: marker.price, price: marker.price, moved: false,
      entryPhase:(marker.order.riskEntry ? marker.order : Array.from(latest.current.orders).find(o=>o.id===marker.order.riskEntryId))?.held?.childClientId?"ACTIVATED":"WAITING",previewQty:riskProposal(marker,marker.price).qty };
    const row = [...(latest.current.hostRef.current?.querySelectorAll<HTMLElement>("[data-order-group]") ?? [])]
      .find((el) => el.dataset.orderIds?.split(",").includes(marker.order.id));
    const confirmedLine = row?.querySelector<HTMLElement>("[data-order-confirmed-line]");
    const confirmedY = latest.current.facadeRef.current?.priceToCoordinate(marker.price);
    if (confirmedLine && confirmedY != null && Number.isFinite(confirmedY)) {
      confirmedLine.style.display = "block"; confirmedLine.style.top = "0px";
    }
    const host = latest.current.hostRef.current;
    if (host) { host.dataset.orderEntryMode = "order-drag"; host.style.cursor = "ns-resize"; host.title = tooltip(marker); }
    latest.current.facadeRef.current?.setOrderCrosshair?.(sideText(marker) === "B" ? latest.current.chrome.up : latest.current.chrome.down);
    latest.current.facadeRef.current?.setPanZoomEnabled?.(false);
    try { (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId); } catch { /* window listeners cover browsers without capture */ }
  };

  useEffect(() => {
    const closeChooser = () => { setChooser(null); setLineChooser(null); };
    const lineHits = (event: PointerEvent) => {
      const p = latest.current, host = p.hostRef.current, facade = p.facadeRef.current;
      if (!host || !facade || (p.activeTool && p.activeTool !== "select") || host.dataset.riskEntryActive || chooserLatest.current
        || host.dataset.orderEntryMode === "gesture" || (event.target as Element)?.closest?.("[data-drawing-ui],button,input,select,textarea,[contenteditable='true']")) return [];
      const binding = chartBindingForModifiers(event);
      if (binding && p.config && chartConditionalTemplate(p.config.templates.filter((t): t is PlaceOrderTemplate => t.kind === "place"), binding)) return [];
      const rect = host.getBoundingClientRect(), x = event.clientX - rect.left, y = event.clientY - rect.top;
      const height = facade.paneHeights?.()[0] ?? rect.height;
      if (x < 0 || x >= rect.width - p.rightAxisWidth || y < 0 || y >= height) return [];
      return nearestPriceLines(groupsLatest.current.flatMap(group => group.markers), y, price => facade.priceToCoordinate(price));
    };
    const onLineDown = (event: PointerEvent) => {
      if (event.button !== 0 || drag.current || !document.hasFocus()) return;
      const hits = lineHits(event);
      if (!hits.length) return;
      event.preventDefault(); event.stopImmediatePropagation();
      if (hits.length > 1) { setLineChooser(hits.map(marker => marker.order.id)); return; }
      startDrag(event, hits[0]);
    };
    const onLineHover = (event: PointerEvent) => {
      if (drag.current) return;
      const host = latest.current.hostRef.current;
      if (!host) return;
      const hits = lineHits(event);
      if (host.dataset.orderEntryMode && host.dataset.orderEntryMode !== "order-hover") return;
      if (hits.length) {
        host.dataset.orderEntryMode = "order-hover";
        host.style.cursor = hits.some(marker => marker.draggable) ? "ns-resize" : "";
        host.title = hits.map(marker => tooltip(marker)).join("\n\n");
      } else if (host.dataset.orderEntryMode === "order-hover") {
        delete host.dataset.orderEntryMode; host.style.cursor = ""; host.title = "";
      }
    };
    const leave = () => {
      const host = latest.current.hostRef.current;
      if (host?.dataset.orderEntryMode === "order-hover") { delete host.dataset.orderEntryMode; host.style.cursor = ""; host.title = ""; }
    };
    const onMove = (event: PointerEvent) => {
      const current = drag.current;
      if (!current || event.pointerId !== current.pointerId) return;
      const distance = Math.hypot(event.clientX - current.startX, event.clientY - current.startY);
      if (distance < 3) return;
      const host = latest.current.hostRef.current;
      const rect = host?.getBoundingClientRect();
      const facade = latest.current.facadeRef.current;
      const startY = facade?.priceToCoordinate(current.startPrice);
      const raw = rect && facade?.coordinateToPrice((startY ?? current.startY - rect.top) + event.clientY - current.startY);
      if (raw == null || !Number.isFinite(raw) || raw <= 0) return;
      current.moved = true; current.price = snapOrderMarkerPrice(raw);
      paintProposal(current.id, current.price, true);
      if (host) host.title = tooltip(current.marker, current.price, true);
    };
    const onUp = (event: PointerEvent) => {
      const current = drag.current;
      if (!current || event.pointerId !== current.pointerId) return;
      drag.current = null;
      const host = latest.current.hostRef.current;
      if (host?.dataset.orderEntryMode === "order-drag") { delete host.dataset.orderEntryMode; host.style.cursor = ""; host.title = ""; latest.current.facadeRef.current?.setOrderCrosshair?.(null); latest.current.facadeRef.current?.setPanZoomEnabled?.(!latest.current.activeTool || latest.current.activeTool === "select"); }
      const row = [...(host?.querySelectorAll<HTMLElement>("[data-order-group]") ?? [])]
        .find((el) => el.dataset.orderIds?.split(",").includes(current.id));
      if (row) { delete row.dataset.proposedPrice; row.querySelector<HTMLElement>("[data-order-price]")?.style.removeProperty("border-color");
        if (!current.moved || current.price === current.startPrice) { const confirmed = row.querySelector<HTMLElement>("[data-order-confirmed-line]"); if (confirmed) confirmed.style.display = "none"; } }
      if (current.moved && current.price !== current.startPrice) void sendReplace(current.marker, current.price,current.entryPhase,current.previewQty);
      else paintProposal(current.id, current.startPrice, false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (drag.current) { event.preventDefault(); cancelDrag(); }
      else if (chooserLatest.current) { event.preventDefault(); closeChooser(); }
    };
    const onRightDown = (event: PointerEvent) => { if (event.button === 2 && drag.current) { event.preventDefault(); cancelDrag(); } };
    const onContext = (event: MouseEvent) => { if (drag.current) { event.preventDefault(); cancelDrag(); } };
    const onOutside = (event: PointerEvent) => {
      if (!chooserLatest.current) return;
      const target = event.target as Element | null;
      if (!target?.closest?.("[role='dialog'][aria-label='Choose chart order']")) closeChooser();
    };
    const host = latest.current.hostRef.current;
    host?.addEventListener("pointerdown", onLineDown, true);
    host?.addEventListener("pointermove", onLineHover, true);
    host?.addEventListener("pointerleave", leave);
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointerdown", onRightDown, true);
    window.addEventListener("pointerdown", onOutside, true);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("contextmenu", onContext, true);
    window.addEventListener("blur",cancelDrag);
    window.addEventListener("pointercancel",cancelDrag);
    return () => {
      cancelDrag(); leave();
      host?.removeEventListener("pointerdown", onLineDown, true);
      host?.removeEventListener("pointermove", onLineHover, true);
      host?.removeEventListener("pointerleave", leave);
      window.removeEventListener("pointermove", onMove); window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointerdown", onRightDown, true); window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("pointerdown", onOutside, true);
      window.removeEventListener("contextmenu", onContext, true);
      window.removeEventListener("blur",cancelDrag);window.removeEventListener("pointercancel",cancelDrag);
    };
  }, []);

  useEffect(()=>{cancelDrag();setChooser(null);setLineChooser(null);},[props.venue,props.symbol,props.pinned,props.activeTool]);

  const cancelOrder = async (marker: ChartOrderMarker) => {
    setPending((current) => new Map(current).set(marker.order.id, { kind: "cancel", stop: false, outcome: "requested" }));
    setAnnouncement(`Cancel requested for ${orderTitle(marker)}.`);
    try {
      const ack = await latest.current.sendCommand("CancelOrder", { venue: marker.order.venue, orderId: marker.order.id });
      if (ack.ambiguous) {
        setPending((current) => new Map(current).set(marker.order.id, { kind: "cancel", stop: false, outcome: "unknown" }));
        setAnnouncement("Cancel outcome unknown. Verify the order before retrying.");
      } else if (ack.status !== "accepted") {
        setPending((current) => { const next = new Map(current); next.delete(marker.order.id); return next; });
        setAnnouncement(`Cancel rejected: ${ack.reason ?? "unknown reason"}.`);
      }
    } catch {
      setPending((current) => new Map(current).set(marker.order.id, { kind: "cancel", stop: false, outcome: "unknown" }));
      setAnnouncement("Cancel outcome unknown. Verify the order before retrying.");
    }
  };

  const adjustByKey = (event: ReactKeyboardEvent<HTMLElement>, marker: ChartOrderMarker) => {
    if (!marker.draggable || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
    event.preventDefault();
    const tick = marker.price >= 1 ? 0.01 : 0.0001;
    void sendReplace(marker, snapOrderMarkerPrice(marker.price + (event.key === "ArrowUp" ? tick : -tick)));
  };

  const sorted = [...groups].sort((a, b) => a.price - b.price || a.kind.localeCompare(b.kind));
  const stack = new Map<string, number>();
  return <div data-testid="chart-order-markers" data-drawing-ui="true" style={{ position:"absolute", inset:0, zIndex:8, pointerEvents:"none", overflow:"hidden" }}>
    {sorted.map((group) => {
      const stackKey = group.price.toFixed(4);
      const stackIndex = stack.get(stackKey) ?? 0; stack.set(stackKey, stackIndex + 1);
      const first = group.markers[0];
      const color = sideText(first) === "B" ? props.chrome.up : props.chrome.down;
      const action = group.pending ?? orderActionPending(first) ?? pending.get(first.order.id);
      const opacity = first.phase === "PAUSED" || action?.kind === "cancel" ? 0.48 : 1;
      const phaseWarning = first.phase === "ACTIVATING" || first.phase === "UNKNOWN" || first.phase === "CANCEL_REQUESTED";
      const status = action ? action.outcome === "unknown" ? "UNKNOWN — verify Orders" : action.kind === "cancel" ? "CANCEL REQUESTED" : "MODIFY REQUESTED"
        : phaseWarning || first.phase === "PAUSED" ? first.phase : riskProposal(first, first.price).aboveBudget ? "ABOVE ORIGINAL BUDGET" : "";
      const ids = group.markers.map((m) => m.order.id);
      const chosen = chooser === group.key || !!lineChooser?.includes(first.order.id) && lineChooser[0] === first.order.id;
      const choices = lineChooser ? groups.flatMap(g => g.markers).filter(marker => lineChooser.includes(marker.order.id)) : group.markers;
      return <div key={group.key} data-order-group="true" data-order-ids={ids.join(",")} data-price={group.price}
        data-confirmed-price={group.pending?.confirmedPrice ?? ""} data-kind={group.kind}
        style={{ position:"absolute", left:0, right:0, top:0, height:0, opacity, pointerEvents:"none", "--order-color":color } as CSSProperties}>
        <div data-order-confirmed-line="true" aria-hidden="true" style={{ display:group.pending ? "block" : "none", position:"absolute", left:0, right:props.rightAxisWidth, top:0, height:2, opacity:.28,
          background:"repeating-linear-gradient(90deg,var(--order-color) 0 8px,transparent 8px 13px)", pointerEvents:"none" }} />
        <div aria-hidden="true" style={{ position:"absolute", left:0, right:props.rightAxisWidth, top:stackIndex, height:2,
          background:"repeating-linear-gradient(90deg,var(--order-color) 0 8px,transparent 8px 13px)", pointerEvents:"none" }} />
        <div data-order-chip style={{ position:"absolute", top:stackIndex * 20 - 10, right:0, display:"flex", alignItems:"center", height:20,
          boxSizing:"border-box",border:`1px solid ${color}`, borderRadius:3, background:"rgba(12,16,23,.96)", color, pointerEvents:"auto", font:"600 10px ui-monospace,monospace", whiteSpace:"nowrap" }}>
          <button type="button" data-testid={`order-label-${first.order.id}`} data-order-price="true" onPointerDown={(e) => group.markers.length === 1 && startDrag(e, first)}
            onClick={() => { if (group.markers.length > 1) { setLineChooser(null); setChooser(chosen ? null : group.key); } }}
            onKeyDown={(e) => group.markers.length === 1 && adjustByKey(e, first)}
            title={`${group.markers.map(marker => tooltip(marker, marker.price, !!group.pending)).join("\n\n")}${status ? `\n${status}` : ""}`}
            aria-label={group.markers.length > 1 ? `${group.markers.length} ${first.order.side} orders at ${priceText(group.price)}; choose an order` : `${orderTitle(first)} at ${priceText(group.price)}; drag or use arrow keys to adjust`}
            style={{ border:0, background:"transparent", color:"inherit", height:"100%", padding:"0 5px", cursor:first.draggable ? "ns-resize" : "pointer", font:"inherit" }}>
            {sideText(first)} {priceText(group.price)}{group.markers.length > 1 ? ` (${group.markers.length})` : ""}
          </button>
          {group.markers.length === 1 && <button type="button" disabled={action?.kind === "cancel"}
            aria-label={first.order.riskEntryId?"Cancel Protection":`Cancel ${orderTitle(first)}`} title={first.order.riskEntryId?"Cancel Protection — cancels remaining buy and linked sells; leaves shares open":`Cancel ${orderTitle(first)}`} onClick={() => void cancelOrder(first)}
            style={{ border:0, borderLeft:`1px solid ${color}`, background:"transparent", color:"inherit", height:"100%", padding:"0 5px", cursor:"pointer", font:"bold 12px system-ui" }}>×</button>}
        </div>
        <div data-order-status data-status={status} style={{display:status ? "block" : "none",position:"absolute",right:props.rightAxisWidth + 4,top:stackIndex * 20 + 12,color:"#ff9d72",background:"#0c1017",fontSize:10}}>{status}</div>
        {chosen && choices.length > 1 && <div role="dialog" aria-label="Choose chart order" style={{ position:"absolute", right:props.rightAxisWidth + 6, top:stackIndex * 18 + 14,
          minWidth:185, padding:5, border:`1px solid ${color}`, borderRadius:4, background:"#111821", boxShadow:"0 4px 18px #0008", pointerEvents:"auto" }}>
          {choices.map((marker) => <div key={marker.order.id} style={{ display:"flex", gap:4, alignItems:"center", marginBottom:3 }}>
            <button type="button" onPointerDown={(e) => startDrag(e, marker)} onKeyDown={(e) => adjustByKey(e, marker)}
              aria-label={`${orderTitle(marker)} at ${priceText(marker.price)}; drag or use arrow keys to adjust`}
              style={{ flex:1, border:`1px solid ${color}`, borderRadius:3, background:"#0c1017", color, padding:"4px 6px", cursor:marker.draggable ? "ns-resize" : "pointer", textAlign:"left", font:"10px ui-monospace,monospace" }}>
              {orderTitle(marker)} · {marker.order.id.slice(-6)}
            </button>
            <button type="button" aria-label={`Cancel ${orderTitle(marker)}`} onClick={() => void cancelOrder(marker)}
              style={{ border:`1px solid ${color}`, borderRadius:3, background:"#0c1017", color, padding:"3px 7px", cursor:"pointer" }}>×</button>
          </div>)}
          <button type="button" aria-label="Close order chooser" onClick={() => { setChooser(null); setLineChooser(null); }} style={{ marginTop:2, border:0, background:"transparent", color:"#ddd", cursor:"pointer", fontSize:10 }}>Close</button>
        </div>}
      </div>;
    })}
    {/unknown|rejected|unavailable|zero shares/i.test(announcement) && <div role="status" style={{position:"absolute",left:10,bottom:10,maxWidth:"80%",color:"#ff9d72",background:"#0c1017",fontSize:11}}>{announcement}</div>}
    {orders.filter(o=>o.venue===props.venue && o.symbol===props.symbol && o.riskEntry).map(entry=>{
      const risk=entry.riskEntry!,stop=orders.find(o=>o.id===risk.stopId);
      const remaining=Math.max(0,entry.executedQty-orders.filter(o=>o.riskEntryId===entry.id).reduce((sum,o)=>sum+o.executedQty,0));
      if(!remaining || (!risk.failure && !risk.protectionCanceled && stop && !["EXPIRED","CANCELED","REJECTED"].includes(stop.status) && stop.held?.phase!=="PAUSED"))return null;
      return <div key={entry.id} role="alert" style={{position:"absolute",left:10,bottom:35,maxWidth:"80%",padding:6,color:"#ff9d72",background:"#0c1017",fontSize:11}}>
        {remaining} shares remain · {risk.failure || (risk.protectionCanceled ? "Protection canceled" : stop?.held?.phase==="PAUSED" ? "Protection paused — reconcile / Resume in Orders" : "DAY protection ended")}. Verify Orders and positions.</div>;
    })}
    <span aria-live="polite" className="chart-order-announcement" style={{ position:"absolute", width:1, height:1, overflow:"hidden", clipPath:"inset(50%)" }}>{announcement}</span>
  </div>;
}
