import { useContext, useMemo, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import type { PanelProps } from "./registry";
import { HoverButton } from "../controls/HoverButton";
import type { PositionRow } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useVenueSelection } from "../exec/venueSelection";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { formatPrice, formatSize } from "../../render/format";
import { displayStatus, STATUS_LABEL, sideLabel, bareSymbol, abbrevType, isWorking, type DisplayStatus } from "../exec/orderStatus";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import type { OrderView } from "../../data/ExecStore";
import { TradeHistoryTable } from "./TradeHistoryTable";
import { PanelHeaderActionsSlotContext } from "./headerSlot";

// Task 19 merges the old AccountBarPanel (stats strip + master/per-venue arm
// chips) and PositionsPanel (sortable positions table, Flatten) into one
// Account panel. Connection-link status dots stay in the top bar (Task 9) —
// this panel keeps only the per-venue "connected" health dot inside its own
// arm chip (a legitimate ok/danger health signal, distinct from arm state).

const money = (n: number | null): string => (n === null ? "—" : (n < 0 ? "−$" : "$") + formatPrice(Math.abs(n), 2));

const DEFAULT_SORT: SortState = { col: "unrealizedPnl", dir: "desc" };

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.posSort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

const COLUMNS: { col: string; label: string; align: "left" | "right"; sortable: boolean }[] = [
  { col: "symbol", label: "Symbol", align: "left", sortable: true },
  { col: "venue", label: "Venue", align: "right", sortable: true },
  { col: "qty", label: "Qty", align: "right", sortable: true },
  { col: "avgPrice", label: "Avg", align: "right", sortable: true },
  { col: "unrealizedPnl", label: "Unrl P&L", align: "right", sortable: true },
  { col: "flatten", label: "", align: "right", sortable: false },
];
const SORT_ACCESSORS: Record<string, (r: PositionRow) => number | string | null> = {
  symbol: (r) => bareSymbol(r.symbol),
  venue: (r) => r.venue ?? "NET",
  qty: (r) => r.qty,
  avgPrice: (r) => r.avgPrice,
  unrealizedPnl: (r) => r.unrealizedPnl,
};

// ---- Orders table (folded from OpenOrdersPanel; now always-visible, venue-scoped) ----

type ChipVariant = "working" | "pending" | "rejected";
function chipVariant(ds: DisplayStatus): ChipVariant | null {
  if (ds === "SUBMITTED" || ds === "ACCEPTED" || ds === "PARTIALLY_FILLED") return "working";
  if (ds === "PendingNew" || ds === "Replacing") return "pending";
  if (ds === "REJECTED" || ds === "BLOCKED") return "rejected";
  return null;
}

const ORDERS_DEFAULT_SORT: SortState = { col: "createdMs", dir: "desc" };
const ORDERS_COLUMNS: { col: string; label: string; align: "left" | "right" }[] = [
  { col: "symbol", label: "Symbol", align: "left" },
  { col: "side", label: "Side", align: "left" },
  { col: "qty", label: "Qty@Px", align: "right" },
  { col: "state", label: "State", align: "left" },
];
const ORDERS_SORT_ACCESSORS: Record<string, (r: OrderView) => number | string | null> = {
  symbol: (r) => r.order.symbol,
  side: (r) => r.order.side,
  qty: (r) => (r.order.leavesQty > 0 ? r.order.leavesQty : r.order.qty),
  state: (r) => STATUS_LABEL[displayStatus(r.order, r.optimistic)],
  createdMs: (r) => r.order.createdMs,
};

function readOrdersSort(s: Record<string, unknown>): SortState {
  const raw = s.ordersSort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return ORDERS_DEFAULT_SORT;
}

function OrdersTable({
  stores, oc, palette, config, onConfigChange, venue, height,
}: {
  stores: PanelProps["stores"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
  height: number;
}): JSX.Element {
  const [sort, setSort] = useState<SortState>(() => readOrdersSort(config.settings));

  const views = sortRows(stores.exec.orders().filter((v) => v.order.venue === venue), sort, ORDERS_SORT_ACCESSORS);
  const reconciling = (stores.exec.status()?.venues ?? []).some((v) => v.reconcilePending);

  const clickSort = (col: string) => {
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ ordersSort: next });
  };

  const th = { padding: "2px 8px" };
  return (
    <div data-testid="orders-table" style={{ height, flexShrink: 0, overflow: "hidden", display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
        <span style={{ fontWeight: 600 }}>Open Orders ({views.length})</span>
        <HoverButton data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")}
          style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.warn}`, background: "transparent", color: palette.warn, cursor: "pointer" }}>Cancel All</HoverButton>
        {reconciling && (
          <span data-testid="reconcile-badge" className="chip chip-pending" style={{ marginLeft: "auto" }}>
            stream gap — reconciled, verify
          </span>
        )}
      </div>
      <div style={{ flex: 1, overflow: "auto" }}>
        <table style={{ width: "100%", borderCollapse: "collapse" }}>
          <thead>
            <tr style={{ color: palette.textMuted }}>
              {ORDERS_COLUMNS.map((c) => (
                <th key={c.col} style={{ ...th, textAlign: c.align, cursor: "pointer" }} onClick={() => clickSort(c.col)}
                  className={`col-head${sort?.col === c.col ? " sort-active" : ""}`}>
                  {c.label} {sortIndicator(sort, c.col)}
                </th>
              ))}
              <th style={th} />
              <th style={th} />
              <th style={th} />
            </tr>
          </thead>
          <tbody>
            {views.map(({ order, optimistic }) => {
              const ds = displayStatus(order, optimistic);
              const variant = chipVariant(ds);
              const working = !optimistic && isWorking(order.status);
              const priceStr = order.type === "MARKET" ? "MKT" : formatPrice(order.type === "STOP" ? order.stopPrice : order.limitPrice, 2);
              return (
                <tr key={order.id} style={{ borderTop: `1px solid ${palette.border}` }}>
                  <td style={{ padding: "2px 8px" }}>{bareSymbol(order.symbol)}</td>
                  <td style={{ color: order.side === "BUY" || order.side === "COVER" ? palette.up : palette.down }}>{sideLabel(order.side)}</td>
                  <td style={{ textAlign: "right" }}>{formatSize(order.leavesQty > 0 ? order.leavesQty : order.qty)} @ {priceStr} {abbrevType(order.type)}</td>
                  <td>{variant ? (
                    <span className={`chip chip-${variant}`} data-chip={variant}>{STATUS_LABEL[ds]}</span>
                  ) : (
                    <span style={{ color: palette.textMuted }}>{STATUS_LABEL[ds]}</span>
                  )}</td>
                  <td style={{ color: palette.danger, fontSize: 10 }}>{order.rejectReason}</td>
                  <td style={{ color: palette.textMuted, fontSize: 10 }}>{order.venue}</td>
                  <td>{(working || optimistic) ? (
                    <HoverButton data-testid={`cancel-${order.id}`} onClick={() => void oc.cancel(order.venue, order.id)}
                      style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Cancel</HoverButton>
                  ) : null}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ---- Stats strip (folded from AccountBarPanel) ----
// Per-venue arm chips were removed (master arm + risk-limit gate now cover
// this — TopBar's arm-chip owns the single master arm switch).

function StatsStrip({
  stores, palette, venue,
}: {
  stores: PanelProps["stores"];
  palette: ReturnType<typeof useTheme>["palette"];
  venue: string;
}): JSX.Element {
  const account = stores.exec.accounts().find((a) => a.venue === venue);
  const equity = account?.equity ?? null;
  const bp = account?.buyingPower ?? null;
  const dayPnl = account?.dayPnl ?? null;
  const realized = account?.realized ?? null;

  const cell = (label: string, testid: string, value: string, tone?: number) => (
    <div style={{ display: "flex", flexDirection: "column", padding: "2px 10px" }}>
      <span style={{ fontSize: 10, color: palette.textMuted }}>{label}</span>
      <span data-testid={testid} className="mono" style={{ fontSize: 13, color: tone === undefined ? palette.text : tone >= 0 ? palette.up : palette.down }}>{value}</span>
    </div>
  );

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "4px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      {cell("Equity", "acct-equity", money(equity))}
      {cell("Buying Power", "acct-bp", money(bp))}
      {cell("Day P&L", "acct-daypnl", money(dayPnl), dayPnl ?? 0)}
      {cell("Realized", "acct-realized", money(realized), realized ?? 0)}
      <div style={{ flex: 1 }} />
    </div>
  );
}

// ---- Positions table (folded from PositionsPanel, now sortable via T16) ----

function PositionsTable({
  stores, commands, oc, palette, config, onConfigChange, venue,
}: {
  stores: PanelProps["stores"];
  commands: PanelProps["commands"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
}): JSX.Element {
  const toast = useToasts();
  const rows0 = stores.exec.positions().filter((p) => p.venue === venue); // venue-scoped; NET (venue===null) rows drop out
  const status = stores.exec.status();
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const masterArmed = !!status?.masterArmed;

  const rows = useMemo(() => sortRows(rows0, sort, SORT_ACCESSORS), [rows0, sort]);
  const openCount = rows0.length;

  const clickSort = (col: string, sortable: boolean) => {
    if (!sortable) return;
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ posSort: next });
  };

  const flatten = (row: PositionRow) => {
    if (row.venue === null) return; // net rows have no single venue to route to (button is hidden anyway)
    const venue = row.venue;        // narrowed to VenueID
    const quote = stores.quote.get(row.symbol);
    if (!quote) { toast.push({ level: "danger", text: `No quote to price the close for ${bareSymbol(row.symbol)}.` }); return; }
    const long = row.qty > 0;
    const t: PlaceOrderTemplate = {
      kind: "place", id: "flatten", label: "Flatten", side: long ? "SELL" : "COVER",
      type: "MARKET", tif: "DAY", priceSource: long ? "Bid" : "Ask", priceOffset: 0,
      sizing: { mode: "PositionFraction", pct: 100 },
    };
    const r = resolvePlaceTemplate(t, { venue, symbol: row.symbol, quote, buyingPower: 0, positionQty: row.qty, nowMs: Date.now() });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
  };

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  void commands; // oc already wraps commands; kept in signature for parity/legibility

  return (
    <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ padding: "4px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
        {openCount} open position{openCount === 1 ? "" : "s"}
      </div>
      <div style={{ flex: 1, overflow: "auto" }}>
        <table style={{ width: "100%", borderCollapse: "collapse" }}>
          <thead>
            <tr style={{ color: palette.textMuted, textAlign: "right" }}>
              {COLUMNS.map((c) => (
                <th key={c.col} style={{ ...th, textAlign: c.align, cursor: c.sortable ? "pointer" : "default" }}
                  onClick={() => clickSort(c.col, c.sortable)}>
                  {c.label} {c.sortable ? sortIndicator(sort, c.col) : ""}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => {
              const net = r.venue === null;
              return (
                <tr key={`${r.venue ?? "NET"}-${r.symbol}-${i}`} data-testid={net ? "pos-net" : undefined}
                  style={{ textAlign: "right", borderTop: `1px solid ${palette.border}`, fontWeight: net ? 700 : 400 }}>
                  <td style={{ textAlign: "left", padding: "2px 8px" }}>{bareSymbol(r.symbol)}</td>
                  <td style={{ color: palette.textMuted }}>{net ? "NET" : r.venue}</td>
                  <td style={{ color: r.qty >= 0 ? palette.up : palette.down }}>{formatSize(r.qty)}</td>
                  <td>{formatPrice(r.avgPrice, 2)}</td>
                  <td style={{ color: r.unrealizedPnl >= 0 ? palette.up : palette.down }}>{formatPrice(r.unrealizedPnl, 2)}</td>
                  <td>{net ? null : (
                    <HoverButton data-testid={`flatten-${r.venue}-${r.symbol}`} data-armed={masterArmed}
                      title={masterArmed ? "Flatten position" : "Master disarmed — flatten still allowed (exposure-reducing)"}
                      onClick={() => flatten(r)}
                      style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Flatten</HoverButton>
                  )}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

type Tab = "positions" | "history";

export function AccountPanel({ config, stores, commands, onConfigChange, linkGroups, group: groupProp, height }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const group = groupProp ?? config.group;
  const { venue, venues, selectVenue } = useVenueSelection(group, linkGroups, stores);
  // Portaled into PanelFrame's ledger-header actions slot, beside the close
  // button (see headerSlot.ts's PanelHeaderActionsSlotContext). undefined (no
  // frame above, e.g. a body-level test) falls back to rendering inline; null
  // (frame present, slot div not yet mounted) renders nothing for that tick.
  const actionsSlot = useContext(PanelHeaderActionsSlotContext);
  const venueSelect = (
    <select data-testid="acct-venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}>
      {venues.map((v) => <option key={v} value={v}>{v}</option>)}
    </select>
  );

  const [ordersHeight, setOrdersHeight] = useState<number>(() => {
    const raw = config.settings.ordersHeight;
    return typeof raw === "number" && raw >= 80 ? raw : 200;
  });
  const [activeTab, setActiveTab] = useState<Tab>(() => (config.settings.tab === "history" ? "history" : "positions"));

  // Pinned per the reference implementation (task brief): `finalHeight` is a
  // plain closure-captured variable, not React state, so `onUp` always reads
  // the LATEST drag value regardless of React's state-update batching timing.
  // Reading `ordersHeight` (the state var) inside `onUp` instead would close
  // over the value from mousedown-time, not the live value — a stale-closure
  // bug. Persisting once on mouseup (not on every mousemove) is the debounce.
  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    const startHeight = ordersHeight;
    let finalHeight = startHeight;
    const onMove = (ev: MouseEvent) => {
      finalHeight = Math.max(80, Math.min(height - 120, startHeight + (ev.clientY - startY)));
      setOrdersHeight(finalHeight);
    };
    const onUp = () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
      onConfigChange({ ordersHeight: finalHeight });
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  };

  const selectTab = (t: Tab) => { setActiveTab(t); onConfigChange({ tab: t }); };

  const positionsCount = stores.exec.positions().filter((p) => p.venue === venue).length;

  const tabBtn = (label: string, active: boolean, onClick: () => void) => (
    <button onClick={onClick} style={{
      fontSize: 12, padding: "4px 10px", background: "transparent", border: "none",
      borderBottom: active ? `2px solid ${palette.accent}` : "2px solid transparent",
      color: active ? palette.text : palette.textMuted, cursor: "pointer",
    }}>{label}</button>
  );

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontFamily: "inherit" }}>
      {actionsSlot === undefined ? venueSelect : actionsSlot ? createPortal(venueSelect, actionsSlot) : null}
      <StatsStrip stores={stores} palette={palette} venue={venue} />
      <OrdersTable stores={stores} oc={oc} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} height={ordersHeight} />
      <div data-testid="orders-resize-handle" onMouseDown={startResize}
        style={{ height: 4, cursor: "row-resize", background: palette.border, flexShrink: 0 }} />
      <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <div style={{ display: "flex", borderBottom: `1px solid ${palette.border}`, background: palette.surface }}>
          {tabBtn(`Positions (${positionsCount})`, activeTab === "positions", () => selectTab("positions"))}
          {tabBtn("Trade History", activeTab === "history", () => selectTab("history"))}
        </div>
        {activeTab === "positions"
          ? <PositionsTable stores={stores} commands={commands} oc={oc} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} />
          : <TradeHistoryTable stores={stores} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} />}
      </div>
    </div>
  );
}
