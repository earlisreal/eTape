import type { VenueInstrumentEligibility } from "../../gen/wsmsg";
import type { DraftOrder } from "./preChecks";

export function fractionalStockSizing(
  stock: VenueInstrumentEligibility | null | undefined,
  order: DraftOrder,
  positionQty: number,
): boolean {
  if (!stock?.fractionalStockOrders || order.tif !== "DAY" || order.session === "OVERNIGHT") return false;
  if (order.side === "BUY") {
    if (!stock.fractionable || !stock.tradable || positionQty < 0) return false;
  } else if (order.side !== "SELL" || positionQty <= 0) return false;
  if (stock.phase === "rth" && order.session !== "EXTENDED") return true;
  if (stock.phase !== "pre" && stock.phase !== "post" && stock.phase !== "rth") return false;
  return stock.fractionalEhEnabled === true && order.session !== "RTH"
    && (order.type === "LIMIT" || order.type === "STOP_LIMIT" || order.type === "LIMIT_IF_TOUCHED");
}
