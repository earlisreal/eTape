import { useEffect, useMemo, useState } from "react";
import { useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { Side, OrderType, TIF, SubmitOrderArgs } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useOrderConfig } from "../exec/useOrderConfig";
import { useVenueSelection } from "../exec/venueSelection";
import { useThrottledQuote } from "../exec/useThrottledQuote";
import { resolveShares, type SizingMode } from "../exec/sizing";
import { preCheck, type DraftOrder } from "../exec/preChecks";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { sideLabel, bareSymbol, abbrevType } from "../exec/orderStatus";
import { formatPrice, QUOTE_DECIMALS } from "../../render/format";
import { useOpenSettings } from "../OpenSettingsContext";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];
const MODE_LABEL: Record<SizingMode, string> = { Shares: "Sh", Dollar: "$", BuyingPowerPct: "BP%", PositionFraction: "Pos" };

export function OrderTicketPanel({ config, stores, commands, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config: orderCfg } = useOrderConfig(); // presets/templates
  const openSettings = useOpenSettings();
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());

  const group = groupProp ?? config.group;
  const [symbol, setSymbol] = useState<string>(() => linkGroups.symbolFor(group) ?? (config.settings.symbol as string) ?? "US.AAPL");
  useEffect(() => {
    const apply = () => setSymbol(linkGroups.symbolFor(group) ?? (config.settings.symbol as string) ?? "US.AAPL");
    apply();
    return linkGroups.subscribe(apply);
  }, [linkGroups, group, config.settings.symbol]);

  const quote = useThrottledQuote(stores.quote, symbol);
  const { venue, venues, selectVenue } = useVenueSelection(group, linkGroups, stores);

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
  const hasStop = type === "STOP" || type === "STOP_LIMIT";

  const submitManual = () => {
    if (venue === "") { toast.push({ level: "danger", text: "No venue configured." }); return; }
    const px = Number(price) || 0;
    const spec = mode === "Shares" ? { mode, shares: Number(amount) || 0 }
      : mode === "Dollar" ? { mode, dollar: Number(amount) || 0 }
      : mode === "BuyingPowerPct" ? { mode, pct: Number(amount) || 0 }
      : { mode, fraction: "all" as const };
    const qty = resolveShares(spec, { price: px, buyingPower, positionQty });
    const draft: DraftOrder = { symbol, side, type, tif, qty, limitPrice: type === "MARKET" ? 0 : px, stopPrice: hasStop ? Number(stop) || 0 : 0 };
    const pc = preCheck(draft, quote?.last ?? 0, Date.now());
    for (const n of pc.notices) toast.push({ level: "warn", text: n });
    if (!pc.ok) { toast.push({ level: "danger", text: pc.errors.join(" ") }); return; }
    const o = pc.order;
    const args: SubmitOrderArgs = { venue, symbol, side: o.side, type: o.type, tif: o.tif, qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice };
    const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(QUOTE_DECIMALS)} ${abbrevType(o.type)}`;
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

  // Clickable inline bid/ask in the header blotter line (replaces the old Bid/Ask
  // button row). No quote => em dash, click no-ops.
  const quoteFill = (value: number | undefined) => { if (value !== undefined) setPrice(value.toFixed(QUOTE_DECIMALS)); };
  const priceSpan = (testid: string, value: number | undefined, tone: string) => (
    <span data-testid={testid} onClick={() => quoteFill(value)}
      style={{ color: tone, cursor: value === undefined ? "default" : "pointer" }}>
      {value === undefined ? "—" : formatPrice(value, QUOTE_DECIMALS)}
    </span>
  );
  const sideClass = (s: Side) => `side${s !== side ? "" : s === "BUY" ? " side-selected-buy" : " side-selected"}`;
  const ctl = { flex: 1 } as const;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 3, padding: 6, height: "100%", background: palette.surface, color: palette.text, fontSize: 12, overflow: "auto" }}>
      {/* Strip 1 — header blotter line */}
      <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
        <strong className="serif" style={{ fontSize: 14 }}>{bareSymbol(symbol)}</strong>
        <span className="mono" style={{ fontSize: 12 }}>
          {priceSpan("bid", quote?.bid, palette.up)}
          <span style={{ color: palette.textMuted }}>/</span>
          {priceSpan("ask", quote?.ask, palette.down)}
        </span>
        <div style={{ flex: 1 }} />
        <select data-testid="venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
          {venues.map((v) => <option key={v} value={v}>{v}</option>)}
        </select>
        <button data-testid="open-settings" className="btn" onClick={() => openSettings?.openOrderSettings()}>⚙</button>
      </div>
      {/* Strip 2 — side row */}
      <div style={{ display: "flex", gap: 3 }}>
        {SIDES.map((s) => (
          <button key={s} type="button" className={sideClass(s)} onClick={() => setSide(s)}>{s}</button>
        ))}
      </div>
      {/* Strip 3 — type · tif · price · stop */}
      <div style={{ display: "flex", gap: 3 }}>
        <select data-testid="order-type" className="ctl mono" value={type} onChange={(e) => setType(e.target.value as OrderType)} style={ctl}>
          {TYPES.map((t) => <option key={t} value={t}>{abbrevType(t)}</option>)}
        </select>
        <select className="ctl mono" value={tif} onChange={(e) => setTif(e.target.value as TIF)} style={ctl}>
          {TIFS.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        <input data-testid="price" className="ctl mono" value={price} onChange={(e) => setPrice(e.target.value)} disabled={type === "MARKET"} placeholder="price" style={ctl} />
        <input data-testid="stop" className="ctl mono" value={stop} onChange={(e) => setStop(e.target.value)} disabled={!hasStop} placeholder="stop" style={{ ...ctl, opacity: hasStop ? 1 : 0.4 }} />
      </div>
      {/* Strip 4 — qty · mode · submit */}
      <div style={{ display: "flex", gap: 3 }}>
        <input data-testid="amount" className="ctl mono" value={amount} onChange={(e) => setAmount(e.target.value)} style={{ width: 64 }} />
        <select data-testid="mode" className="ctl mono" value={mode} title={mode} onChange={(e) => setMode(e.target.value as SizingMode)} style={{ width: 56 }}>
          {MODES.map((m) => <option key={m} value={m} title={m}>{MODE_LABEL[m]}</option>)}
        </select>
        <button data-testid="submit" className="btn btn-primary" onClick={submitManual} style={{ flex: 1, fontWeight: 700 }}>
          {side} {bareSymbol(symbol)}
        </button>
      </div>
      {/* Strip 5 — preset chips (only when presets exist) */}
      {presets.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 3 }}>
          {presets.map((t) => (
            <button key={t.id} data-testid={`preset-${t.id}`} className="btn" onClick={() => firePreset(t)}>{t.label}</button>
          ))}
        </div>
      )}
    </div>
  );
}
