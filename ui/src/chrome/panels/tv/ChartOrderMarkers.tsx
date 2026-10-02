import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MutableRefObject, type PointerEvent as ReactPointerEvent } from "react";
import type { AckMsg, Order, ReplaceOrderArgs } from "../../../wire/contract";
import type { ChartApiFacade } from "../../../render/chart/ChartApiFacade";
import { chartOrderMarkers, orderActionPending, snapOrderMarkerPrice, type ChartOrderMarker } from "../../../render/chart/orderMarkers";

const LIMIT = "#f4c84a";
const STOP = "#34c6dc";
type Pending = { kind: "replace" | "cancel"; price?: number; stop: boolean; confirmedPrice?: number; outcome: "requested" | "unknown" };
type Drag = { id: string; marker: ChartOrderMarker; startX: number; startY: number; startPrice: number; price: number; moved: boolean };

interface Props {
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
}

interface Group { key: string; price: number; kind: ChartOrderMarker["kind"]; markers: ChartOrderMarker[]; pending?: Pending }

function priceText(price: number): string { return price < 1 ? price.toFixed(4) : price.toFixed(2); }
function orderTitle(marker: ChartOrderMarker): string {
  const type = marker.order.type === "LIMIT_IF_TOUCHED" ? "LIT" : marker.kind === "limit" ? "LIMIT" : "STOP-LIMIT";
  return `${marker.order.side} ${marker.order.leavesQty.toLocaleString("en-US")} ${type}`;
}

export function ChartOrderMarkers(props: Props): JSX.Element {
  const markers = useMemo(() => chartOrderMarkers(props.orders, props.venue, props.symbol, props.pinned),
    [props.orders, props.venue, props.symbol, props.pinned]);
  const [pending, setPending] = useState<Map<string, Pending>>(() => new Map());
  const groups = useMemo(() => {
    const map = new Map<string, Group>();
    for (const marker of markers) {
      const action = orderActionPending(marker) ?? pending.get(marker.order.id);
      const waiting = action?.kind === "replace" && action.price !== marker.price ? action : undefined;
      const price = waiting ? waiting.price! : marker.price;
      const key = waiting ? `${marker.kind}:${price.toFixed(4)}:pending:${marker.order.id}` : `${marker.kind}:${price.toFixed(4)}`;
      const group = map.get(key) ?? { key, kind: marker.kind, price, markers: [], ...(waiting ? { pending: waiting } : {}) };
      group.markers.push(waiting ? { ...marker, price, draggable: false } : marker); map.set(key, group);
    }
    return [...map.values()];
  }, [markers, pending]);
  const latest = useRef(props);
  latest.current = props;
  const drag = useRef<Drag | null>(null);
  const [chooser, setChooser] = useState<string | null>(null);
  const chooserLatest = useRef(chooser); chooserLatest.current = chooser;
  props.chooserOpenRef.current = chooser !== null;
  const [announcement, setAnnouncement] = useState("");

  const layout = () => {
    const host = latest.current.hostRef.current;
    const facade = latest.current.facadeRef.current;
    if (!host || !facade) return;
    for (const el of host.querySelectorAll<HTMLElement>("[data-order-group]")) {
      if (drag.current && el.dataset.orderIds?.split(",").includes(drag.current.id)) continue;
      const price = Number(el.dataset.price);
      const y = facade.priceToCoordinate(price);
      if (y != null && Number.isFinite(y)) el.style.top = `${Math.round(y)}px`;
      const confirmedLine = el.querySelector<HTMLElement>("[data-order-confirmed-line]");
      if (confirmedLine) {
        const confirmedY = facade.priceToCoordinate(Number(el.dataset.confirmedPrice));
        confirmedLine.style.top = confirmedY != null && Number.isFinite(confirmedY) ? `${Math.round(confirmedY)}px` : "-10000px";
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
      confirmedLine.style.display = "block"; confirmedLine.style.top = `${Math.round(confirmedY)}px`;
    }
    row.dataset.proposedPrice = String(price);
    const color = highlight ? "#fff29a" : (row.dataset.kind === "limit" ? LIMIT : STOP);
    row.style.setProperty("--order-color", color);
    const chip = row.querySelector<HTMLElement>("[data-order-price]");
    if (chip) { chip.textContent = priceText(price); chip.style.borderColor = color; }
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
    setAnnouncement("Price change canceled.");
  };

  const sendReplace = async (marker: ChartOrderMarker, price: number) => {
    const stop = marker.kind !== "limit";
    const args: ReplaceOrderArgs = {
      venue: marker.order.venue, orderId: marker.order.id,
      qty: 0, // price-only chart edits preserve the broker-authoritative total quantity in Core.
      limitPrice: stop ? marker.order.limitPrice : price,
      stopPrice: stop ? price : marker.order.stopPrice,
    };
    setPending((current) => new Map(current).set(marker.order.id, { kind: "replace", price, confirmedPrice: marker.price, stop, outcome: "requested" }));
    setAnnouncement(`Modify requested at ${priceText(price)}.`);
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

  const startDrag = (event: ReactPointerEvent<HTMLElement>, marker: ChartOrderMarker) => {
    if (!marker.draggable || event.button !== 0) return;
    event.preventDefault(); event.stopPropagation();
    drag.current = { id: marker.order.id, marker, startX: event.clientX, startY: event.clientY,
      startPrice: marker.price, price: marker.price, moved: false };
    const row = [...(latest.current.hostRef.current?.querySelectorAll<HTMLElement>("[data-order-group]") ?? [])]
      .find((el) => el.dataset.orderIds?.split(",").includes(marker.order.id));
    const confirmedLine = row?.querySelector<HTMLElement>("[data-order-confirmed-line]");
    const confirmedY = latest.current.facadeRef.current?.priceToCoordinate(marker.price);
    if (confirmedLine && confirmedY != null && Number.isFinite(confirmedY)) {
      confirmedLine.style.display = "block"; confirmedLine.style.top = `${Math.round(confirmedY)}px`;
    }
    try { event.currentTarget.setPointerCapture(event.pointerId); } catch { /* window listeners cover browsers without capture */ }
  };

  useEffect(() => {
    const onMove = (event: PointerEvent) => {
      const current = drag.current;
      if (!current) return;
      const distance = Math.hypot(event.clientX - current.startX, event.clientY - current.startY);
      if (distance < 3) return;
      const host = latest.current.hostRef.current;
      const rect = host?.getBoundingClientRect();
      const raw = rect && latest.current.facadeRef.current?.coordinateToPrice(event.clientY - rect.top);
      if (raw == null || !Number.isFinite(raw) || raw <= 0) return;
      current.moved = true; current.price = snapOrderMarkerPrice(raw);
      paintProposal(current.id, current.price, true);
    };
    const onUp = () => {
      const current = drag.current;
      if (!current) return;
      drag.current = null;
      const host = latest.current.hostRef.current;
      const row = [...(host?.querySelectorAll<HTMLElement>("[data-order-group]") ?? [])]
        .find((el) => el.dataset.orderIds?.split(",").includes(current.id));
      if (row) { delete row.dataset.proposedPrice; row.querySelector<HTMLElement>("[data-order-price]")?.style.removeProperty("border-color");
        if (!current.moved || current.price === current.startPrice) { const confirmed = row.querySelector<HTMLElement>("[data-order-confirmed-line]"); if (confirmed) confirmed.style.display = "none"; } }
      if (current.moved && current.price !== current.startPrice) void sendReplace(current.marker, current.price);
      else paintProposal(current.id, current.startPrice, false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (drag.current) { event.preventDefault(); cancelDrag(); }
      else if (chooserLatest.current !== null) { event.preventDefault(); setChooser(null); }
    };
    const onRightDown = (event: PointerEvent) => { if (event.button === 2 && drag.current) { event.preventDefault(); cancelDrag(); } };
    const onContext = (event: MouseEvent) => { if (drag.current) { event.preventDefault(); cancelDrag(); } };
    const onOutside = (event: PointerEvent) => {
      if (chooserLatest.current === null) return;
      const target = event.target as Element | null;
      if (!target?.closest?.("[role='dialog'][aria-label='Choose chart order']")) setChooser(null);
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointerdown", onRightDown, true);
    window.addEventListener("pointerdown", onOutside, true);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("contextmenu", onContext, true);
    return () => {
      window.removeEventListener("pointermove", onMove); window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointerdown", onRightDown, true); window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("pointerdown", onOutside, true);
      window.removeEventListener("contextmenu", onContext, true);
    };
  }, []);

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
      const color = group.kind === "limit" ? LIMIT : STOP;
      const stackKey = group.price.toFixed(4);
      const stackIndex = stack.get(stackKey) ?? 0; stack.set(stackKey, stackIndex + 1);
      const first = group.markers[0];
      const action = group.pending ?? orderActionPending(first) ?? pending.get(first.order.id);
      const opacity = first.phase === "PAUSED" || action?.kind === "cancel" ? 0.48 : 1;
      const phaseWarning = first.phase === "ACTIVATING" || first.phase === "UNKNOWN" || first.phase === "CANCEL_REQUESTED";
      const warningColor = phaseWarning || action?.outcome === "unknown" ? "#ff9d72" : color;
      const ids = group.markers.map((m) => m.order.id);
      const chosen = chooser === group.key;
      return <div key={group.key} data-order-group="true" data-order-ids={ids.join(",")} data-price={group.price}
        data-confirmed-price={group.pending?.confirmedPrice ?? ""} data-kind={group.kind}
        style={{ position:"absolute", left:0, right:props.rightAxisWidth, top:0, height:0, opacity, pointerEvents:"none", "--order-color":warningColor } as CSSProperties}>
        {group.pending && <div data-order-confirmed-line="true" aria-hidden="true" style={{ display:"block", position:"absolute", left:0, right:0, top:0, height:2, opacity:.28,
          background:"repeating-linear-gradient(90deg,var(--order-color) 0 8px,transparent 8px 13px)", pointerEvents:"none" }} />}
        <div aria-hidden="true" style={{ position:"absolute", left:0, right:0, top:stackIndex, height:2,
          background:"repeating-linear-gradient(90deg,var(--order-color) 0 8px,transparent 8px 13px)", pointerEvents:"none" }} />
        <button type="button" data-testid={`order-label-${first.order.id}`} onPointerDown={(e) => group.markers.length === 1 && startDrag(e, first)}
          onClick={() => group.markers.length > 1 && setChooser(chosen ? null : group.key)} onKeyDown={(e) => group.markers.length === 1 && adjustByKey(e, first)}
          aria-label={group.markers.length > 1 ? `${group.markers.length} orders at ${priceText(group.price)}; choose an order` : `${orderTitle(first)} at ${priceText(group.price)}; drag or use arrow keys to adjust`}
          style={{ position:"absolute", left:8, top:stackIndex * 2 - 10, height:20, padding:"0 7px", border:`1px solid ${warningColor}`,
            borderRadius:3, background:"rgba(12,16,23,.94)", color:warningColor, font:"600 10px ui-monospace,monospace", cursor:first.draggable ? "ns-resize" : "pointer", pointerEvents:"auto", whiteSpace:"nowrap" }}>
          {group.markers.length > 1 ? `${group.markers.length} ${group.kind === "limit" ? "LIMITS" : group.kind === "limit-if-touched" ? "LITS" : "STOPS"}` : orderTitle(first)}
          {phaseWarning && <span style={{ marginLeft:5 }}>{first.phase}</span>}
          {action && <span style={{ marginLeft:5 }}>{action.outcome === "unknown" ? "UNKNOWN" : action.kind === "cancel" ? "CANCEL REQUESTED" : "MODIFY REQUESTED"}</span>}
        </button>
        <div style={{ position:"absolute", top:stackIndex * 18 - 10, right:0, display:"flex", alignItems:"center", height:20,
          border:`1px solid ${warningColor}`, borderRadius:3, background:"rgba(12,16,23,.96)", color:warningColor, pointerEvents:"auto", font:"600 10px ui-monospace,monospace" }}>
          <button type="button" data-order-price="true" onPointerDown={(e) => group.markers.length === 1 && startDrag(e, first)}
            onClick={() => group.markers.length > 1 && setChooser(chosen ? null : group.key)}
            aria-label={`${priceText(group.price)} ${group.markers.length > 1 ? `for ${group.markers.length} orders` : "order price"}`}
            style={{ border:0, background:"transparent", color:"inherit", height:"100%", padding:"0 5px", cursor:first.draggable ? "ns-resize" : "pointer", font:"inherit" }}>
            {priceText(group.price)}{group.markers.length > 1 ? ` ×${group.markers.length}` : ""}
          </button>
          {group.markers.length === 1 && <button type="button" disabled={action?.kind === "cancel"}
            aria-label={`Cancel ${orderTitle(first)}`} title={`Cancel ${orderTitle(first)}`} onClick={() => void cancelOrder(first)}
            style={{ border:0, borderLeft:`1px solid ${warningColor}`, background:"transparent", color:"inherit", height:"100%", padding:"0 5px", cursor:"pointer", font:"bold 12px system-ui" }}>×</button>}
        </div>
        {chosen && group.markers.length > 1 && <div role="dialog" aria-label="Choose chart order" style={{ position:"absolute", right:props.rightAxisWidth + 6, top:stackIndex * 18 + 14,
          minWidth:185, padding:5, border:`1px solid ${color}`, borderRadius:4, background:"#111821", boxShadow:"0 4px 18px #0008", pointerEvents:"auto" }}>
          {group.markers.map((marker) => <div key={marker.order.id} style={{ display:"flex", gap:4, alignItems:"center", marginBottom:3 }}>
            <button type="button" onPointerDown={(e) => startDrag(e, marker)} onKeyDown={(e) => adjustByKey(e, marker)}
              aria-label={`${orderTitle(marker)} at ${priceText(marker.price)}; drag or use arrow keys to adjust`}
              style={{ flex:1, border:`1px solid ${color}`, borderRadius:3, background:"#0c1017", color, padding:"4px 6px", cursor:marker.draggable ? "ns-resize" : "pointer", textAlign:"left", font:"10px ui-monospace,monospace" }}>
              {orderTitle(marker)} · {marker.order.id.slice(-6)}
            </button>
            <button type="button" aria-label={`Cancel ${orderTitle(marker)}`} onClick={() => void cancelOrder(marker)}
              style={{ border:`1px solid ${color}`, borderRadius:3, background:"#0c1017", color, padding:"3px 7px", cursor:"pointer" }}>×</button>
          </div>)}
          <button type="button" aria-label="Close order chooser" onClick={() => setChooser(null)} style={{ marginTop:2, border:0, background:"transparent", color:"#ddd", cursor:"pointer", fontSize:10 }}>Close</button>
        </div>}
      </div>;
    })}
    <span aria-live="polite" className="chart-order-announcement" style={{ position:"absolute", width:1, height:1, overflow:"hidden", clipPath:"inset(50%)" }}>{announcement}</span>
  </div>;
}
