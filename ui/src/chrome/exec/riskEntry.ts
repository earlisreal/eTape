import type { RiskEntryTemplate } from "./actionTemplate";
import { resolveLimitCushionPrice } from "./priceSource";
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
