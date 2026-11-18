import { useState } from "react";
import type { Quote, Side, SubmitOrderArgs } from "../../wire/contract";

interface Props {
  symbol: string;
  venue: string;
  routeReady: boolean;
  quote: Quote | undefined;
  positionQty: number;
  submit(args: SubmitOrderArgs, label: string): void;
}

export function PerpetualTicket({ symbol, venue, routeReady, quote, positionQty, submit }: Props): JSX.Element {
  const [type, setType] = useState<"LIMIT" | "MARKET">("LIMIT");
  const [quantity, setQuantity] = useState("");
  const [valueUnit, setValueUnit] = useState<"contracts" | "notional">("contracts");
  const [price, setPrice] = useState("");
  const [stop, setStop] = useState("");
  const [leverage, setLeverage] = useState(1);
  const contract = quote?.contract;
  const usable = routeReady && contract && quote?.mark && venue;
  const px = type === "LIMIT" ? Number(price) : quote?.mark ?? 0;
  const multiplier = Number(contract?.multiplier ?? 0);
  const step = contract?.quantityStep ?? 1;
  const contracts = valueUnit === "contracts" ? Number(quantity) : Math.floor(Number(quantity) / (px * multiplier * step)) * step;
  const validQuantity = contract && Number.isSafeInteger(contracts) && contracts >= contract.minimum && contracts <= contract.maximum && contracts % step === 0;
  const validPrice = px > 0 && Number.isFinite(px);
  const validLeverage = contract && Number.isInteger(leverage) && leverage >= contract.minLeverage && leverage <= contract.maxLeverage;
  const notional = validQuantity && validPrice ? contracts * multiplier * px : 0;
  const place = (side: Side) => {
    if (!usable || !validQuantity || !validPrice) return;
    const opening = side === "BUY" || side === "SHORT";
    submit({
      venue, symbol, side, type, tif: type === "LIMIT" && !stop ? "GTC" : "IOC", session: "AUTO",
      qty: contracts, limitPrice: type === "LIMIT" ? px : 0, stopPrice: 0,
      perpetual: { leverage, ...(opening && Number(stop) > 0 ? { protectionPrice: Number(stop) } : {}) },
    }, `${labels[side]} ${contracts} contracts`);
  };
  const canOpen = !!(usable && validQuantity && validPrice && validLeverage && contract.tradingEnabled);
  const canClose = !!(usable && validQuantity && validPrice && contracts <= Math.abs(positionQty));
  return <div className="perpetual-ticket" style={{ display: "grid", gap: 6 }}>
    {!routeReady && <span>Select the MEXC paper Execution Venue.</span>}
    {routeReady && !contract && <span>Waiting for MEXC contract rules.</span>}
    <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
      <label>Type <select className="ctl" value={type} onChange={e => setType(e.target.value === "MARKET" ? "MARKET" : "LIMIT")}><option value="LIMIT">Limit</option><option value="MARKET">Market</option></select></label>
      <label>Limit price <input className="ctl" type="number" min={0} step={contract?.priceTick ?? "any"} disabled={type === "MARKET"} value={price} onChange={e => setPrice(e.target.value)} /></label>
    </div>
    <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
      <label>Size <input className="ctl" type="number" min={0} value={quantity} onChange={e => setQuantity(e.target.value)} /></label>
      <select aria-label="Size unit" className="ctl" value={valueUnit} onChange={e => setValueUnit(e.target.value === "notional" ? "notional" : "contracts")}><option value="contracts">Contracts</option><option value="notional">USDT position value</option></select>
      <label>Isolated leverage <input className="ctl" type="number" min={contract?.minLeverage ?? 1} max={contract?.maxLeverage} value={leverage} onChange={e => setLeverage(Number(e.target.value))} /></label>
    </div>
    <label>Fair-price stop (optional) <input className="ctl" type="number" min={0} step={contract?.priceTick ?? "any"} value={stop} onChange={e => setStop(e.target.value)} /></label>
    {notional > 0 && contract && <span>{contracts} contracts · {(contracts * multiplier).toLocaleString("en-US", { maximumFractionDigits: 12 })} {contract.base} · {notional.toFixed(2)} USDT position value{validLeverage ? ` · ≈ ${(notional / leverage).toFixed(2)} USDT margin` : ""}</span>}
    <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
      <button type="button" className="side side-buy" disabled={!canOpen || positionQty < 0} onClick={() => place("BUY")}>Open Long</button>
      <button type="button" className="side side-sell" disabled={!canOpen || positionQty > 0} onClick={() => place("SHORT")}>Open Short</button>
      <button type="button" className="side side-sell" disabled={!canClose || positionQty <= 0} onClick={() => place("SELL")}>Close Long</button>
      <button type="button" className="side side-buy" disabled={!canClose || positionQty >= 0} onClick={() => place("COVER")}>Close Short</button>
    </div>
  </div>;
}

const labels: Record<Side, string> = { BUY: "Open Long", SHORT: "Open Short", SELL: "Close Long", COVER: "Close Short" };
