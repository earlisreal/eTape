import type { Order } from "../../wire/contract";
import { isWorking } from "../../wire/orderStatus";

export interface ChartOrderMarker {
  order: Order;
  price: number;
  kind: "limit" | "stop-limit" | "limit-if-touched";
  draggable: boolean;
  phase: string;
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
    if (order.type === "LIMIT") {
      const actionPending = order.action?.phase === "REQUESTED" || order.action?.phase === "UNKNOWN";
      out.push({ order, price: order.limitPrice, kind: "limit", draggable: !order.held?.cancelRequested && !actionPending, phase: order.held?.phase ?? "" });
      continue;
    }
    if (order.type !== "STOP_LIMIT" && order.type !== "LIMIT_IF_TOUCHED") continue;
    const held = order.held;
    const childExists = !!held?.childClientId || held?.phase === "WORKING" || held?.phase === "ACTIVATING";
    const paused = held?.phase === "PAUSED";
    const price = childExists ? order.limitPrice : order.stopPrice;
    out.push({
      order, price, kind: childExists ? "limit" : order.type === "LIMIT_IF_TOUCHED" ? "limit-if-touched" : "stop-limit",
      draggable: !paused && !held?.cancelRequested && order.action?.phase !== "REQUESTED" && order.action?.phase !== "UNKNOWN" && (held?.phase === "WORKING" || held?.phase === "WAITING" || held?.phase === "ARMED"),
      phase: held?.phase ?? "NATIVE",
    });
  }
  return out.filter((marker) => marker.price > 0 && Number.isFinite(marker.price));
}

export function snapOrderMarkerPrice(price: number): number {
  const tick = price >= 1 ? 0.01 : 0.0001;
  return Math.round((price + Number.EPSILON) / tick) * tick;
}
