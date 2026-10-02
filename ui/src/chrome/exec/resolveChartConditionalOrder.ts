import type { Quote, Side, StopLimitRoutePreview, SubmitOrderArgs, TIF, OrderSession } from "../../wire/contract";
import { CHART_BINDINGS, type ChartBinding, type PlaceOrderTemplate } from "./actionTemplate";
import { resolvePlaceTemplate, type ResolveContext } from "./resolveTemplate";

export type ChartConditionalTemplate = PlaceOrderTemplate & { type: "STOP_LIMIT" | "LIMIT_IF_TOUCHED" };

export function chartConditionalTemplate(templates: PlaceOrderTemplate[], binding: string): ChartConditionalTemplate | undefined {
  return templates.find((t): t is ChartConditionalTemplate =>
    (t.type === "STOP_LIMIT" || t.type === "LIMIT_IF_TOUCHED") && t.chartBinding === binding);
}

export function chartBindingForModifiers(modifiers: { ctrlKey: boolean; altKey: boolean; shiftKey: boolean }): ChartBinding | undefined {
  return CHART_BINDINGS.find((binding) => {
    const keys = binding.split("+");
    return modifiers.ctrlKey === keys.includes("Ctrl") && modifiers.altKey === keys.includes("Alt") && modifiers.shiftKey === keys.includes("Shift");
  });
}

export function resolveChartConditionalOrder(
  template: PlaceOrderTemplate,
  ctx: Omit<ResolveContext, "quote"> & { quote?: Quote },
  stopPrice: number,
): { args: SubmitOrderArgs; flash: string; errors: string[] } {
  const quote = ctx.quote ?? { symbol: ctx.symbol, bid: stopPrice, ask: stopPrice, last: stopPrice, ts: "" };
  const resolved = resolvePlaceTemplate({ ...template, priceSource: "Last", priceOffset: 0 }, { ...ctx, quote: { ...quote, last: stopPrice } });
  return { args: resolved.args, flash: resolved.flash, errors: resolved.preCheck.errors };
}

export function chartConditionalRouteKey(tif: TIF, session: OrderSession, symbol = "", deferred = false, type: "STOP_LIMIT" | "LIMIT_IF_TOUCHED" = "STOP_LIMIT"): string {
	return `${type}:${tif}:${session}:${symbol}:${deferred ? "deferred" : "fixed"}`;
}

export function chartConditionalOrderWillTrigger(side: Side, stopPrice: number, route?: StopLimitRoutePreview, type: "STOP_LIMIT" | "LIMIT_IF_TOUCHED" = "STOP_LIMIT"): boolean {
	if (!route?.hasTrustedEligiblePrint || route.lastEligiblePrice === undefined) return false;
	const buyish = side === "BUY" || side === "COVER";
	return type === "LIMIT_IF_TOUCHED"
		? buyish ? route.lastEligiblePrice <= stopPrice : route.lastEligiblePrice >= stopPrice
		: buyish ? route.lastEligiblePrice >= stopPrice : route.lastEligiblePrice <= stopPrice;
}

export function conditionalRouteLabel(route: StopLimitRoutePreview): string {
	return route.route === "ENGINE_HELD" ? "LOCAL · eTape-held" : route.route === "UNSUPPORTED" ? "UNSUPPORTED" : "NATIVE · venue stop";
}
