import { useContext, useEffect, useState } from "react";
import { useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import type { CSSProperties } from "react";
import type { PanelProps } from "./registry";
import type { Side, OrderType, TIF, OrderSession, SubmitOrderArgs } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useVenueSelection } from "../exec/venueSelection";
import { useOrderConfig } from "../exec/useOrderConfig";
import { useThrottledQuote } from "../exec/useThrottledQuote";
import { resolveShares, type SizingMode } from "../exec/sizing";
import { preCheck, type DraftOrder } from "../exec/preChecks";
import { sideLabel, bareSymbol, abbrevType } from "../exec/orderStatus";
import { formatPrice, QUOTE_DECIMALS } from "../../render/format";
import { useOpenSettings } from "../OpenSettingsContext";
import { StepperInput } from "./StepperInput";
import { PanelHeaderActionsSlotContext } from "./headerSlot";
import { IconGear } from "./tv/tvIcons";

const SIDES: Side[] = ["BUY", "SELL", "SHORT", "COVER"];
const TYPES: OrderType[] = ["LIMIT", "MARKET", "STOP", "STOP_LIMIT"];
const TIFS: TIF[] = ["DAY", "GTC", "IOC", "FOK"];
const SESSIONS: OrderSession[] = ["AUTO", "RTH", "EXTENDED", "OVERNIGHT"];
const MODES: SizingMode[] = ["Shares", "Dollar", "BuyingPowerPct", "PositionFraction"];
// Full words in the ticket's own dropdowns — abbrevType (orderStatus.ts) stays
// abbreviated since it's shared with OpenOrdersPanel and the submit-flash toast.
const TYPE_LABEL: Record<OrderType, string> = { MARKET: "Market", LIMIT: "Limit", STOP: "Stop", STOP_LIMIT: "Stop Limit" };
const MODE_LABEL: Record<SizingMode, string> = { Shares: "Shares", Dollar: "Dollars", BuyingPowerPct: "Buying Power %", PositionFraction: "Position" };
// AUTO resolves session-dependent behavior (extended_hours flags, TIF
// coercion) from the server clock at submit time — today's behavior, kept as
// the default so nothing changes until the trader picks an explicit session.
const SESSION_LABEL: Record<OrderSession, string> = { AUTO: "Auto", RTH: "Regular", EXTENDED: "Extended", OVERNIGHT: "Overnight" };

export function OrderTicketPanel({ config, stores, commands, linkGroups, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const openSettings = useOpenSettings();
  // Portaled into PanelFrame's ledger-header actions slot, beside the close
  // button (see headerSlot.ts's PanelHeaderActionsSlotContext) — same pattern
  // as TapePanel's settings gear. undefined (no frame above, e.g. a body-level
  // test) falls back to rendering inline; null (frame present, slot div not
  // yet mounted) renders nothing for that tick.
  const actionsSlot = useContext(PanelHeaderActionsSlotContext);
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
  const { config: orderConfig } = useOrderConfig();
  const extBufferPct = orderConfig.extHoursMarketBufferPct ?? 1;

  const [type, setType] = useState<OrderType>("LIMIT");
  const [tif, setTif] = useState<TIF>("DAY");
  const [session, setSession] = useState<OrderSession>("AUTO");
  const [mode, setMode] = useState<SizingMode>("Shares");
  const [amount, setAmount] = useState("100");
  const [price, setPrice] = useState("");
  const [stop, setStop] = useState("");

  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const buyingPower = account?.buyingPower ?? 0;
  const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);

  const hasStop = type === "STOP" || type === "STOP_LIMIT";

  const submitManual = (side: Side) => {
    if (venue === "") { toast.push({ level: "danger", text: "No venue configured." }); return; }
    const px = Number(price) || 0;
    const spec = mode === "Shares" ? { mode, shares: Number(amount) || 0 }
      : mode === "Dollar" ? { mode, dollar: Number(amount) || 0 }
      : mode === "BuyingPowerPct" ? { mode, pct: Number(amount) || 0 }
      : { mode, pct: Number(amount) || 0 };
    const qty = resolveShares(spec, { price: px, buyingPower, positionQty });
    const draft: DraftOrder = { symbol, side, type, tif, session, qty, limitPrice: type === "MARKET" ? 0 : px, stopPrice: hasStop ? Number(stop) || 0 : 0 };
    const pc = preCheck(draft, quote ?? { bid: 0, ask: 0, last: 0 }, Date.now(), extBufferPct);
    for (const n of pc.notices) toast.push({ level: "warn", text: n });
    if (!pc.ok) { toast.push({ level: "danger", text: pc.errors.join(" ") }); return; }
    const o = pc.order;
    const args: SubmitOrderArgs = { venue, symbol, side: o.side, type: o.type, tif: o.tif, session: o.session, qty: o.qty, limitPrice: o.limitPrice, stopPrice: o.stopPrice };
    const tail = o.type === "MARKET" ? "MKT" : `${o.limitPrice.toFixed(QUOTE_DECIMALS)} ${abbrevType(o.type)}`;
    const flash = `${sideLabel(o.side)} ${o.qty.toLocaleString("en-US")} ${bareSymbol(symbol)} @ ${tail}`;
    void oc.submit(args, flash);
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
  const sideTone = (s: Side) => `side ${s === "BUY" || s === "COVER" ? "side-buy" : "side-sell"}`;
  // Labeled-field wrapper: a small uppercase .col-head caption above its control,
  // wrapped in a real <label> so the caption is associated with the control.
  const fieldCol = { display: "flex", flexDirection: "column", gap: 2, flex: 1, minWidth: 0 } as const;
  const field = (label: string, child: JSX.Element, style: CSSProperties = fieldCol) => (
    <label style={style}>
      <span className="col-head">{label}</span>
      {child}
    </label>
  );
  // border-box so width:100% includes the .ctl control's own padding/border —
  // without it, controls overflow their flex column and overlap the neighbor.
  const full = { width: "100%", boxSizing: "border-box" } as const;

  // Header row: venue select then settings gear, in that order so the gear
  // lands immediately left of PanelFrame's close button — same layout as
  // TapePanel's lone header gear, extended with the venue picker.
  const headerActions = (
    <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
      <select data-testid="venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
        {venues.map((v) => <option key={v} value={v}>{v}</option>)}
      </select>
      <button type="button" data-testid="open-settings" aria-label="order settings"
        onClick={() => openSettings?.openOrderSettings()}
        style={{ display: "inline-flex", border: "none", background: "transparent", color: palette.textMuted, cursor: "pointer", padding: 3 }}>
        <IconGear size={13} />
      </button>
    </div>
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: 6, height: "100%", background: palette.surface, color: palette.text, fontSize: 12, overflow: "auto" }}>
      {actionsSlot === undefined ? headerActions : actionsSlot ? createPortal(headerActions, actionsSlot) : null}
      {/* Strip 1 — header blotter line */}
      <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
        <strong className="serif" style={{ fontSize: 14 }}>{bareSymbol(symbol)}</strong>
        <span className="mono" style={{ fontSize: 12 }}>
          {priceSpan("bid", quote?.bid, palette.up)}
          <span style={{ color: palette.textMuted }}>/</span>
          {priceSpan("ask", quote?.ask, palette.down)}
        </span>
      </div>
      {/* Strip 2 — type · price · stop */}
      <div style={{ display: "flex", gap: 6 }}>
        {field("Type", (
          <select data-testid="order-type" className="ctl mono" value={type} onChange={(e) => setType(e.target.value as OrderType)} style={full}>
            {TYPES.map((t) => <option key={t} value={t}>{TYPE_LABEL[t]}</option>)}
          </select>
        ))}
        {field("Price", (
          <StepperInput testid="price" value={price} onChange={setPrice} disabled={type === "MARKET"} placeholder="price" style={full} />
        ))}
        {field("Stop", (
          <StepperInput testid="stop" value={stop} onChange={setStop} disabled={!hasStop} placeholder="stop" style={{ ...full, opacity: hasStop ? 1 : 0.4 }} />
        ))}
      </div>
      {/* Strip 3 — size · size-by · tif, same equal-width columns as strip 2 */}
      <div style={{ display: "flex", gap: 6 }}>
        {field("Size", (
          <input type="number" inputMode="decimal" min={0} data-testid="amount" className="ctl numfield mono" value={amount} onChange={(e) => setAmount(e.target.value)} style={full} />
        ))}
        {field("Size by", (
          <select data-testid="mode" className="ctl mono" value={mode} onChange={(e) => setMode(e.target.value as SizingMode)} style={full}>
            {MODES.map((m) => <option key={m} value={m}>{MODE_LABEL[m]}</option>)}
          </select>
        ))}
        {field("TIF", (
          <select className="ctl mono" value={tif} onChange={(e) => setTif(e.target.value as TIF)} style={full}>
            {TIFS.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        ))}
        {field("Session", (
          <select data-testid="session" className="ctl mono" value={session} onChange={(e) => setSession(e.target.value as OrderSession)} style={full}>
            {SESSIONS.map((s) => <option key={s} value={s}>{SESSION_LABEL[s]}</option>)}
          </select>
        ))}
      </div>
      {/* Strip 4 — action row: each button submits its side directly */}
      <div style={{ display: "flex", gap: 3 }}>
        {SIDES.map((s) => (
          <button key={s} type="button" data-testid={`side-${s}`} className={sideTone(s)} onClick={() => submitManual(s)}>{s}</button>
        ))}
      </div>
    </div>
  );
}
