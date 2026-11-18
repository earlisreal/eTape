import type { PerpetualContract } from "../../gen/wsmsg";
import type { RiskEntryTemplate } from "./actionTemplate";

export function perpetualTick(price: number, contract: PerpetualContract, rounding = Math.round): number {
  const tick = Number(contract.priceTick);
  if (tick <= 0 || !Number.isFinite(price)) return 0;
  const exponent = contract.priceTick.toLowerCase().split("e");
  const decimals = Math.max(0, (exponent[0].split(".")[1]?.length ?? 0) - Number(exponent[1] ?? 0));
  return Number((rounding(price / tick) * tick).toFixed(Math.min(decimals, 20)));
}

export function perpetualRiskSize(
  template: RiskEntryTemplate, contract: PerpetualContract | undefined, trigger: number, stop: number, collateral: number,
) {
  const p = template.perpetual;
  if (!p || !contract || !contract.tradingEnabled || trigger <= 0 || stop <= 0 ||
      p.leverage < contract.minLeverage || p.leverage > contract.maxLeverage ||
      (p.side === "BUY" ? stop >= trigger : stop <= trigger)) return null;
  const long = p.side === "BUY";
  const cushion = template.buyCushion.unit === "%" ? trigger * template.buyCushion.value / 100 : template.buyCushion.value;
  const limit = perpetualTick(trigger + (long ? cushion : -cushion), contract, long ? Math.ceil : Math.floor);
  if (limit <= 0) return null;
  const unit = Number(contract.multiplier), fee = Number(contract.takerFee);
  const fees = (limit + stop) * unit * fee;
  const riskPerContract = Math.abs(limit - stop) * unit + fees;
  const marginPerContract = limit * unit / p.leverage + fees;
  const budget = template.mode === "Dollar" ? template.value : collateral * template.value / 100;
  const available = Math.min(contract.maximum, budget / riskPerContract, collateral / marginPerContract);
  const rounded = Math.floor(available / contract.quantityStep + 1e-10) * contract.quantityStep;
  const qty = Number.isSafeInteger(rounded) && rounded >= contract.minimum ? rounded : 0;
  return { buyLimit: limit, sellLimit: stop, budget, qty, risk: qty * riskPerContract, notional: qty * limit * unit };
}
