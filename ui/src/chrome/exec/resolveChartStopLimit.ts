import type { Quote, Side, StopLimitRoutePreview, SubmitOrderArgs, TIF, OrderSession } from "../../wire/contract";
import { CHART_BINDINGS, type ChartBinding, type PlaceOrderTemplate } from "./actionTemplate";
import { resolvePlaceTemplate, type ResolveContext } from "./resolveTemplate";

export function chartStopLimitTemplate(templates: PlaceOrderTemplate[], binding: string): PlaceOrderTemplate | undefined {
  return templates.find((t) => t.type === "STOP_LIMIT" && t.chartBinding === binding);
}

export function chartBindingForModifiers(modifiers: { ctrlKey: boolean; altKey: boolean; shiftKey: boolean }): ChartBinding | undefined {
  return CHART_BINDINGS.find((binding) => {
    const keys = binding.split("+");
    return modifiers.ctrlKey === keys.includes("Ctrl") && modifiers.altKey === keys.includes("Alt") && modifiers.shiftKey === keys.includes("Shift");
  });
}

export function resolveChartStopLimit(
  template: PlaceOrderTemplate,
  ctx: Omit<ResolveContext, "quote"> & { quote?: Quote },
  stopPrice: number,
): { args: SubmitOrderArgs; flash: string; errors: string[] } {
  const quote = ctx.quote ?? { symbol: ctx.symbol, bid: stopPrice, ask: stopPrice, last: stopPrice, ts: "" };
  const resolved = resolvePlaceTemplate({ ...template, priceSource: "Last", priceOffset: 0 }, { ...ctx, quote: { ...quote, last: stopPrice } });
  return { args: resolved.args, flash: resolved.flash, errors: resolved.preCheck.errors };
}

export function chartStopLimitRouteKey(tif: TIF, session: OrderSession, symbol = ""): string {
	return `${tif}:${session}:${symbol}`;
}

export function chartStopLimitWillTrigger(side: Side, stopPrice: number, route?: StopLimitRoutePreview): boolean {
	if (!route?.hasTrustedEligiblePrint || route.lastEligiblePrice === undefined) return false;
	return side === "BUY" || side === "COVER"
		? route.lastEligiblePrice >= stopPrice
		: route.lastEligiblePrice <= stopPrice;
}

export function stopLimitRouteLabel(route: StopLimitRoutePreview): string {
  return route.route === "ENGINE_HELD" ? "LOCAL · eTape-held" : "NATIVE · venue stop";
}
