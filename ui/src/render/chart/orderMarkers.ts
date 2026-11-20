import type { Order } from "../../wire/contract";
import { isWorking } from "../../wire/orderStatus";

export interface ChartOrderMarker {
  order: Order;
  price: number;
  kind: "limit" | "stop" | "stop-limit" | "limit-if-touched" | "pending-protection";
  draggable: boolean;
  phase: string;
}

export function markerId(marker: ChartOrderMarker): string {
  return marker.kind === "pending-protection" ? `${marker.order.id}:protection` : marker.order.id;
}

export function markerSide(marker: ChartOrderMarker): Order["side"] {
  if (marker.kind === "pending-protection") return marker.order.side === "SHORT" ? "COVER" : "SELL";
  return marker.order.side;
}

export function orderActionPending(marker: ChartOrderMarker): { kind: "replace" | "cancel"; price?: number; stop: boolean; confirmedPrice?: number; outcome: "requested" | "unknown" } | undefined {
  const action = marker.order.action;
  if (!action || (action.phase !== "REQUESTED" && action.phase !== "UNKNOWN")) return undefined;
  const outcome = action.phase === "UNKNOWN" ? "unknown" : "requested";
  if (action.kind === "CANCEL") return { kind: "cancel", stop: false, outcome };
  const stop = marker.kind !== "limit";
  const price = stop ? action.requestedStopPrice : action.requestedLimitPrice;
  if (price == null || price <= 0) return undefined;
  const confirmedPrice = stop ? action.previousStopPrice : action.previousLimitPrice;
  return {
    kind: "replace",
    price,
    stop,
    ...(confirmedPrice == null ? {} : { confirmedPrice }),
    outcome,
  };
}

// Chart orders are a view of confirmed domain orders, never optimistic ticket rows.
export function chartOrderMarkers(orders: Iterable<Order>, venue: string, symbol: string, pinned: boolean): ChartOrderMarker[] {
  if (pinned || !venue || !symbol) return [];
  const out: ChartOrderMarker[] = [];
  for (const order of orders) {
    if (order.venue !== venue || order.symbol !== symbol || !isWorking(order.status)) continue;
    if (order.status === "SUBMITTED" && !order.held) continue;
    const perpetual = order.symbol.startsWith("PERP.");
    const held = order.held;
    const actionPending = order.action?.phase === "REQUESTED" || order.action?.phase === "UNKNOWN";
    const pretrigger = !held?.childClientId && !held?.cancelRequested && !held?.perpetualTriggered &&
      (held?.phase === "WAITING" || held?.phase === "ARMED");
    if (order.pendingProtectionPrice && held) {
      out.push({ order, price: order.pendingProtectionPrice, kind: "pending-protection",
        draggable: !!order.perpetualRisk && pretrigger && !actionPending, phase: held.phase });
    }
    if (order.type === "LIMIT") {
      out.push({ order, price: order.limitPrice, kind: "limit", draggable: !perpetual && !held?.cancelRequested && !actionPending, phase: held?.phase ?? "" });
      continue;
    }
    if (order.type === "STOP" && order.symbol.startsWith("PERP.MEXC.")) {
      out.push({ order, price: order.stopPrice, kind: "stop", draggable: (!held || pretrigger) && !actionPending, phase: held?.phase ?? order.status });
      continue;
    }
    if (order.type !== "STOP_LIMIT" && order.type !== "LIMIT_IF_TOUCHED" && order.type!=="TRAILING_STOP") continue;
    const childExists = !!held?.childClientId || held?.phase === "WORKING" || held?.phase === "ACTIVATING";
    if(held?.trail?.retired && !childExists)continue;
    const paused = held?.phase === "PAUSED";
    const price = childExists ? order.limitPrice : order.stopPrice;
    out.push({
      order, price, kind: childExists ? "limit" : order.type === "LIMIT_IF_TOUCHED" ? "limit-if-touched" : "stop-limit",
      draggable: perpetual ? !!order.perpetualRisk && pretrigger && !actionPending :
        (!held?.trail || childExists) && !paused && !held?.cancelRequested && !actionPending && (held?.phase === "WORKING" || held?.phase === "WAITING" || held?.phase === "ARMED"),
      phase: held?.phase ?? "NATIVE",
    });
  }
  return out.filter((marker) => marker.price > 0 && Number.isFinite(marker.price));
}

export function snapOrderMarkerPrice(price: number): number {
  const tick = price >= 1 ? 0.01 : 0.0001;
  return Number((Math.round((price + Number.EPSILON) / tick) * tick).toFixed(tick === 0.01 ? 2 : 4));
}

export function nearestPriceLines<T extends { price: number }>(lines: T[], y: number,
  priceToY: (price: number) => number | null): T[] {
  let distance = 8;
  let nearest: T[] = [];
  for (const line of lines) {
    const lineY = priceToY(line.price);
    if (lineY == null || !Number.isFinite(lineY)) continue;
    const delta = Math.abs(lineY - y);
    if (delta > distance) continue;
    if (delta < distance) { distance = delta; nearest = []; }
    nearest.push(line);
  }
  return nearest;
}

// Keep chips apart without moving their price lines or changing the chart scale.
// ponytail: 22px per group; use a combined chooser if groups outgrow the pane.
export function orderMarkerChipYs(ys: number[], paneHeight: number): number[] {
  const sorted = ys.map((y, index) => ({ y, index })).sort((a, b) => a.y - b.y);
  let edge = 10;
  for (const item of sorted) { item.y = Math.max(edge, item.y); edge = item.y + 22; }
  edge = paneHeight - 10;
  for (const item of [...sorted].reverse()) { item.y = Math.min(edge, item.y); edge = item.y - 22; }
  const result = [...ys];
  for (const item of sorted) result[item.index] = item.y;
  return result;
}
