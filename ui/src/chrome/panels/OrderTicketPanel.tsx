import { useEffect, useMemo, useState } from "react";
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { Side, OrderType, TIF, SubmitOrderArgs, VenueID } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useOrderConfig } from "../exec/useOrderConfig";
import { useThrottledQuote } from "../exec/useThrottledQuote";
import { resolveShares, type SizingMode } from "../exec/sizing";
import { preCheck, type DraftOrder } from "../exec/preChecks";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { sideLabel, bareSymbol, abbrevType } from "../exec/orderStatus";
import { formatPrice } from "../../render/format";
import { useOpenSettings } from "../OpenSettingsContext";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];

export function OrderTicketPanel({ config, stores, commands, linkGroups }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config: orderCfg, setActiveVenue } = useOrderConfig(); // shared context (Task 8)
  const openSettings = useOpenSettings(); // unified Settings modal, Orders section (Task 11)
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const [symbol, setSymbol] = useState<string>(() => linkGroups.symbolFor(config.group) ?? (config.settings.symbol as string) ?? "US.AAPL");
  useEffect(() => {
    const apply = () => setSymbol(linkGroups.symbolFor(config.group) ?? (config.settings.symbol as string) ?? "US.AAPL");
    apply();
    return linkGroups.subscribe(apply);
  }, [linkGroups, config.group, config.settings.symbol]);

  const quote = useThrottledQuote(stores.quote, symbol);
  const status = stores.exec.status();
  const venues = status?.venues.map((v) => v.venue) ?? [];
  const venue: VenueID = orderCfg.activeVenue || venues[0] || "";
  const vStatus = status?.venues.find((v) => v.venue === venue);
  const armed = (status?.masterArmed ?? false) && (vStatus?.venueArmed ?? false);

  const [side, setSide] = useState<Side>("BUY");
  const [type, setType] = useState<OrderType>("LIMIT");
  const [tif, setTif] = useState<TIF>("DAY");
  const [mode, setMode] = useState<SizingMode>("Shares");
  const [amount, setAmount] = useState("100");
  const [price, setPrice] = useState("");
  const [stop, setStop] = useState("");

  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const buyingPower = account?.buyingPower ?? 0;
  const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);

  const presets = useMemo(() => orderCfg.templates.filter((t): t is PlaceOrderTemplate => t.kind === "place"), [orderCfg.templates]);

  const submitManual = () => {
    if (venue === "") { toast.push({ level: "danger", text: "No venue configured." }); return; }
    const px = Number(price) || 0;
    const spec = mode === "Shares" ? { mode, shares: Number(amount) || 0 }
      : mode === "Dollar" ? { mode, dollar: Number(amount) || 0 }
      : mode === "BuyingPowerPct" ? { mode, pct: Number(amount) || 0 }
      : { mode, fraction: "all" as const };
    const qty = resolveShares(spec, { price: px, buyingPower, positionQty });
    const draft: DraftOrder = { symbol, side, type, tif, qty, limitPrice: type === "MARKET" ? 0 : px, stopPrice: type === "STOP" || type === "STOP_LIMIT" ? Number(stop) || 0 : 0 };
    const pc = preCheck(draft, quote?.last ?? 0, Date.now());
    for (const n of pc.notices) toast.push({ level: "warn", text: n });
    if (!pc.ok) { toast.push({ level: "danger", text: pc.errors.join(" ") }); return; }
    const o = pc.order;
    const args: SubmitOrderArgs = { venue, symbol, side: o.side, type: o.type, tif: o.tif, qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice };
    const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(2)} ${abbrevType(o.type)}`;
    const flash = `${sideLabel(o.side)} ${o.qty.toLocaleString("en-US")} ${bareSymbol(symbol)} @ ${tail}`;
    void oc.submit(args, flash);
  };

  const firePreset = (t: PlaceOrderTemplate) => {
    if (venue === "" || !quote) { toast.push({ level: "danger", text: "No venue/quote for preset." }); return; }
    const r = resolvePlaceTemplate(t, { venue, symbol, quote, buyingPower, positionQty, nowMs: Date.now() });
    for (const n of r.preCheck.notices) toast.push({ level: "warn", text: n });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
  };

  const inp = { background: palette.bg, color: palette.text, border: `1px solid ${palette.border}`, fontSize: 12, padding: "2px 4px" } as const;
  const quoteBtn = (label: string, testid: string, value: number | undefined, tone: string) => (
    <button data-testid={testid} onClick={() => value !== undefined && setPrice(String(value))}
      style={{ ...inp, borderColor: tone, color: tone, cursor: "pointer", flex: 1 }}>{label} {value === undefined ? "—" : formatPrice(value, 2)}</button>
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4, padding: 8, height: "100%", background: palette.surface, color: palette.text, fontSize: 12, overflow: "auto" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <strong>{bareSymbol(symbol)}</strong>
        <select data-testid="venue" value={venue} onChange={(e) => setActiveVenue(e.target.value)} style={inp}>
          {venues.map((v) => <option key={v} value={v}>{v}</option>)}
        </select>
        <button data-testid="open-settings" onClick={() => openSettings?.openOrderSettings()} style={inp}>⚙</button>
      </div>
      <div style={{ display: "flex", gap: 4 }}>
        {quoteBtn("Bid", "bid", quote?.bid, palette.up)}
        {quoteBtn("Ask", "ask", quote?.ask, palette.down)}
      </div>
      <div style={{ display: "flex", gap: 4 }}>
        <select value={side} onChange={(e) => setSide(e.target.value as Side)} style={inp}>{SIDES.map((s) => <option key={s}>{s}</option>)}</select>
        <select data-testid="order-type" value={type} onChange={(e) => setType(e.target.value as OrderType)} style={inp}>{TYPES.map((t) => <option key={t}>{t}</option>)}</select>
        <select value={tif} onChange={(e) => setTif(e.target.value as TIF)} style={inp}>{TIFS.map((t) => <option key={t}>{t}</option>)}</select>
      </div>
      <label>Price <input data-testid="price" value={price} onChange={(e) => setPrice(e.target.value)} disabled={type === "MARKET"} style={inp} /></label>
      {(type === "STOP" || type === "STOP_LIMIT") && <label>Stop <input data-testid="stop" value={stop} onChange={(e) => setStop(e.target.value)} style={inp} /></label>}
      <div style={{ display: "flex", gap: 4 }}>
        <input data-testid="amount" value={amount} onChange={(e) => setAmount(e.target.value)} style={{ ...inp, flex: 1 }} />
        <select data-testid="mode" value={mode} onChange={(e) => setMode(e.target.value as SizingMode)} style={inp}>{MODES.map((m) => <option key={m}>{m}</option>)}</select>
      </div>
      <div data-testid="ticket-armed-state" style={{ fontSize: 11, fontWeight: 700, textAlign: "center", padding: "2px 0",
        color: armed ? palette.up : palette.warn }}>
        {armed ? "ARMED" : "DISARMED — order will be blocked"}
      </div>
      <button data-testid="submit" onClick={submitManual} style={{ ...inp, background: palette.accent, color: palette.bg, fontWeight: 700, padding: "6px", cursor: "pointer" }}>
        Submit {side} {symbol && bareSymbol(symbol)}
      </button>
      {presets.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
          {presets.map((t) => (
            <button key={t.id} data-testid={`preset-${t.id}`} onClick={() => firePreset(t)}
              style={{ ...inp, cursor: "pointer" }}>{t.label}</button>
          ))}
        </div>
      )}
      <div style={{ display: "flex", gap: 4, marginTop: "auto" }}>
        <button data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")} style={{ ...inp, flex: 1, borderColor: palette.warn, color: palette.warn, cursor: "pointer" }}>Cancel All</button>
        <button data-testid="kill" onClick={() => void oc.kill()}
          style={{ flex: 1, background: palette.danger, color: "#fff", border: "2px solid #000", fontWeight: 800, letterSpacing: 1, padding: "6px", cursor: "pointer" }}>KILL</button>
      </div>
    </div>
  );
}
