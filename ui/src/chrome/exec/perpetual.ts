import type { Order, PerpetualContract } from "../../gen/wsmsg";
import { isWorking } from "../../wire/orderStatus";
import { defaultLeverageCap, type RiskEntryTemplate } from "./actionTemplate";

export function pendingPerpetualStop(order: Order): { price: number; side: "SELL" | "COVER" } | undefined {
  const price = order.pendingProtectionPrice;
  if (!price || !isWorking(order.status)) return undefined;
  return { price, side: order.side === "SHORT" ? "COVER" : "SELL" };
}

export function perpetualTick(price: number, contract: PerpetualContract, rounding = Math.round): number {
  const tick = Number(contract.priceTick);
  if (tick <= 0 || !Number.isFinite(price)) return 0;
  const exponent = contract.priceTick.toLowerCase().split("e");
  const decimals = Math.max(0, (exponent[0].split(".")[1]?.length ?? 0) - Number(exponent[1] ?? 0));
  return Number((rounding(price / tick) * tick).toFixed(Math.min(decimals, 20)));
}

export function perpetualRiskSize(
  template: Pick<RiskEntryTemplate, "perpetual" | "mode" | "value" | "buyCushion">, contract: PerpetualContract | undefined, trigger: number, stop: number, collateral: number,
  maxQty = Infinity,
) {
  const p = template.perpetual;
  if (!p || !contract || !contract.tradingEnabled || trigger <= 0 || stop <= 0 ||
      (p.side === "BUY" ? stop >= trigger : stop <= trigger)) return null;
  const long = p.side === "BUY";
  const cushion = template.buyCushion.unit === "%" ? trigger * template.buyCushion.value / 100 : template.buyCushion.value;
  const limit = perpetualTick(trigger + (long ? cushion : -cushion), contract, long ?
    value => Math.ceil(value - 1e-9) : value => Math.floor(value + 1e-9));
  if (limit <= 0) return null;
  const unit = Number(contract.multiplier), fee = Number(contract.takerFee);
  const fees = (limit + stop) * unit * fee;
  const riskPerContract = Math.abs(limit - stop) * unit + fees;
  const budget = template.mode === "Dollar" ? template.value : collateral * template.value / 100;
  const target = Math.min(contract.maximum, budget / riskPerContract, maxQty);
  const sizeAt = (leverage: number, maximum: number) => {
    const available = Math.min(target, maximum, collateral / (limit * unit / leverage + fees));
    const qty = Math.floor(available / contract.quantityStep + 1e-10) * contract.quantityStep;
    return Number.isSafeInteger(qty) && qty >= contract.minimum ? qty : 0;
  };
  const autoLeverage = p.leverageCap !== undefined || p.leverage === undefined;
  let qty = 0, leverage = p.leverage ?? 0;
  if (autoLeverage) {
    // Match ContractRules.AutoRiskSize at the buffered mark beyond the stop.
    const cap = Math.min(p.leverageCap ?? defaultLeverageCap, contract.maxLeverage);
    const buffer = Math.max(Number(contract.priceTick), limit * 0.001);
    const mark = stop + (long ? -buffer : buffer);
    for (const tier of contract.riskTiers ?? []) {
      const required = Math.abs(limit - mark) + Math.max(limit, mark) *
        (tier.maintenance + tier.liquidationFee) + (limit + mark) * fee;
      const maxInTier = Math.min(cap, tier.maxLeverage);
      for (let candidate = contract.minLeverage; candidate <= maxInTier; candidate++) {
        if (mark <= 0 || limit / candidate <= required) break;
        const size = sizeAt(candidate, tier.maxContracts);
        if (size > qty || (size > 0 && size === qty && candidate < leverage)) {
          qty = size;
          leverage = candidate;
        }
        if (size > 0 && size + contract.quantityStep > Math.min(target, tier.maxContracts)) break;
      }
    }
  } else if (Number.isInteger(leverage) && leverage >= contract.minLeverage && leverage <= contract.maxLeverage) {
    qty = sizeAt(leverage, contract.maximum);
  }
  const notional = qty * limit * unit;
  return { kind: "perpetual" as const, side: p.side, buyLimit: limit, sellLimit: stop, budget, qty, risk: qty * riskPerContract, notional,
    leverage, autoLeverage, margin: qty > 0 ? notional / leverage : 0 };
}
