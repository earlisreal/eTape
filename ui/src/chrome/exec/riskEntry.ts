import type { RiskEntryTemplate } from "./actionTemplate";
import { resolveLimitCushionPrice } from "./priceSource";
import type { Order } from "../../wire/contract";
import { isWorking } from "../../wire/orderStatus";

export function riskEntryProtection(orders: readonly Order[], qty: number, venue: string, symbol: string): { owner?: Order; stop?: Order; error?: string } {
    const working = orders.filter(o => o.venue === venue && o.symbol === symbol && isWorking(o.status));
    if (qty === 0) return working.length ? { error: "Risk entry requires no other working orders." } : {};
    const stop = working.find(o => o.side === "SELL" && o.riskEntryId);
    const owner = stop && orders.find(o => o.id === stop.riskEntryId && o.venue === venue && o.symbol === symbol);
    if (qty < 0 || !stop || !owner?.riskEntry)
        return { error: "Risk entry requires a flat symbol or a healthy Chart Risk Entry stop." };
    const h = stop.held, r = owner.riskEntry;
    if (r.closing || r.protectionCanceled || r.failure || !h || !["WAITING", "ARMED"].includes(h.phase)
        || h.childClientId || h.cancelRequested || h.deadlineMs <= Date.now() || h.trail?.retired || h.trail?.triggered
        || stop.action && ["REQUESTED", "UNKNOWN"].includes(stop.action.phase))
        return { error: "Add requires healthy untriggered shared protection." };
    if (h.trail && h.trail.config.pricing !== "CUSHION") return { error: "Add requires stop-relative Limit Cushion protection." };
    if (Math.abs(stop.leavesQty - qty) > 1e-9) return { error: "Add requires full confirmed shared protection." };
    if (working.some(o => o.id !== stop.id)) return { error: "Only one pending Chart Risk Entry is allowed; clear other working orders first." };
    return { owner, stop };
}

export function riskEntryAddTemplate(template: RiskEntryTemplate, owner?: Order, stop?: Order): RiskEntryTemplate {
    if (!owner?.riskEntry || !stop) return template;
    const protection = stop.held?.trail?.config;
    return { ...template, sellCushion: protection?.cushion ?? owner.riskEntry.sellCushion,
        ...(protection ? { protection } : { protection: undefined }) };
}
export function riskEntrySize(template: RiskEntryTemplate, buyStop: number, sellStop: number, cash: number, bp: number, savedBudget?: number) {
    const buyLimit = resolveLimitCushionPrice("BUY", buyStop, template.buyCushion.value, template.buyCushion.unit === "%" ? "%" : "$");
    const sellLimit = resolveLimitCushionPrice("SELL", sellStop, template.sellCushion.value, template.sellCushion.unit === "%" ? "%" : "$");
    const budget = savedBudget ?? (template.mode === "Dollar" ? template.value : (template.mode === "CashPct" ? cash : bp) * template.value / 100);
    const funds = template.mode === "CashPct" ? cash : bp;
    const distance = Math.round((buyLimit - sellLimit) * 10000) / 10000;
    const qty = distance > 0 && buyLimit > 0 && sellLimit > 0 && Number.isFinite(budget) && budget > 0 && Number.isFinite(funds)
        ? Math.max(0, Math.min(Math.floor(budget / distance + 1e-9), Math.floor(funds / buyLimit + 1e-9))) : 0;
    return { buyLimit, sellLimit, budget, qty, risk: qty * distance, notional: qty * buyLimit };
}
