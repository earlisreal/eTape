// Pure derivation of what the UI shows for an order. The wire carries the 9 domain
// OrderStatus values; the UI adds two derived states that are NOT wire values:
//   PendingNew — client-optimistic (submitted, no order event yet)
//   Replacing  — derived (replacesId set && still working; a TZ-adapter cosmetic)
import type { Order, OrderStatus, OrderType, Side } from "../../wire/contract";
import { isWorking, isTerminal, sideIsSell } from "../../wire/orderStatus";

export { isWorking, isTerminal, sideIsSell };

export type DisplayStatus = "PendingNew" | "Replacing" | OrderStatus;

export function displayStatus(order: Order, optimistic: boolean): DisplayStatus {
  if (optimistic) return "PendingNew";
  if (order.replacesId !== "" && isWorking(order.status)) return "Replacing";
  return order.status;
}

export const STATUS_LABEL: Record<DisplayStatus, string> = {
  PendingNew: "Pending", Replacing: "Replacing",
  SUBMITTED: "Submitted", ACCEPTED: "Accepted", PARTIALLY_FILLED: "Part. Filled",
  FILLED: "Filled", CANCELED: "Canceled", REJECTED: "Rejected",
  EXPIRED: "Expired", BLOCKED: "Blocked", REPLACED: "Replaced",
};

export function sideLabel(side: Side): string { return side; } // BUY/SELL/SHORT/COVER already display-ready
export function bareSymbol(symbol: string): string { const i = symbol.indexOf("."); return i >= 0 ? symbol.slice(i + 1) : symbol; }
export function abbrevType(t: OrderType): string {
  return t === "MARKET" ? "MKT" : t === "LIMIT" ? "LMT" : t === "STOP" ? "STP" : "STPLMT";
}
