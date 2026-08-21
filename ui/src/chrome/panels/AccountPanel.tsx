import { useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import type { PanelProps } from "./registry";
import { HoverButton } from "../controls/HoverButton";
import type { ClosedOrder, Fill, Order, PositionRow, Quote } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { useVenueSelection } from "../exec/venueSelection";
import { useOrderConfig } from "../exec/useOrderConfig";
import { resolvePlaceTemplate } from "../exec/resolveTemplate";
import type { PlaceOrderTemplate } from "../exec/actionTemplate";
import { formatClock, formatEtDateTime, formatPrice, formatSize } from "../../render/format";
import { displayStatus, STATUS_LABEL, sideLabel, bareSymbol, isTerminal, isWorking, type DisplayStatus } from "../exec/orderStatus";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import type { OrderView } from "../../data/ExecStore";
import { TradeHistoryTable } from "./TradeHistoryTable";
import { PanelHeaderActionsSlotContext } from "./headerSlot";
import { ExportTradesPopover } from "./ExportTradesPopover";
import { ColumnGroup, ColumnResizeHandle, useResizableColumns, type ResizableColumn } from "./ResizableColumns";

// Task 19 merges the old AccountBarPanel (stats strip) and PositionsPanel
// (sortable positions table, Flatten) into one Account panel. Per-venue arm
// chips were later removed entirely (see StatsStrip below) — arming is
// master-only now, owned by TopBar's arm chip. Connection-link status dots
// stay in the top bar (Task 9).

const money = (n: number | null): string => (n === null ? "—" : (n < 0 ? "−$" : "$") + formatPrice(Math.abs(n), 2));

const DEFAULT_SORT: SortState = { col: "unrealizedPnl", dir: "desc" };

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.posSort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

const COLUMNS: (ResizableColumn & { align: "left" | "right"; sortable: boolean })[] = [
  { col: "symbol", label: "Symbol", defaultWidth: 84, minWidth: 68, align: "left", sortable: true },
  { col: "venue", label: "Venue", defaultWidth: 92, minWidth: 72, align: "right", sortable: true },
  { col: "qty", label: "Qty", defaultWidth: 58, minWidth: 48, align: "right", sortable: true },
  { col: "avgPrice", label: "Avg", defaultWidth: 72, minWidth: 60, align: "right", sortable: true },
  { col: "unrealizedPnl", label: "Unrl P&L", defaultWidth: 88, minWidth: 72, align: "right", sortable: true },
  { col: "flatten", label: "", defaultWidth: 64, minWidth: 60, align: "right", sortable: false },
];
const SORT_ACCESSORS: Record<string, (r: PositionRow) => number | string | null> = {
  symbol: (r) => bareSymbol(r.symbol),
  venue: (r) => r.venue ?? "NET",
  qty: (r) => r.qty,
  avgPrice: (r) => r.avgPrice,
  unrealizedPnl: (r) => r.unrealizedPnl,
};

// Live mark price from quote: long → bid, short → ask, fallback → last.
function liveMark(quote: Quote | undefined, qty: number): number | null {
  if (!quote) return null;
  if (qty > 0) {
    if (quote.bid > 0) return quote.bid;
    if (quote.last > 0) return quote.last;
  } else {
    if (quote.ask > 0) return quote.ask;
    if (quote.last > 0) return quote.last;
  }
  return null;
}

// Compute display unrealized P&L from live mark. Works for long (qty>0) and short (qty<0).
function displayUnrealized(quote: Quote | undefined, avgPrice: number, qty: number, fallbackPnl: number): number {
  const mark = liveMark(quote, qty);
  if (mark !== null) return (mark - avgPrice) * qty;
  return fallbackPnl;
}

// ---- Orders table (folded from OpenOrdersPanel; now always-visible, venue-scoped) ----

type ChipVariant = "working" | "pending" | "rejected";
function chipVariant(ds: DisplayStatus): ChipVariant | null {
  if (ds === "SUBMITTED" || ds === "ACCEPTED" || ds === "PARTIALLY_FILLED") return "working";
  if (ds === "PendingNew" || ds === "Replacing") return "pending";
  if (ds === "REJECTED" || ds === "BLOCKED") return "rejected";
  return null;
}

const ORDERS_DEFAULT_SORT: SortState = { col: "createdMs", dir: "desc" };
const ORDERS_COLUMNS: (ResizableColumn & { align: "left" | "right"; sortable: boolean })[] = [
  { col: "createdMs", label: "Submitted", defaultWidth: 128, minWidth: 108, align: "left", sortable: true },
  { col: "symbol", label: "Symbol", defaultWidth: 84, minWidth: 68, align: "left", sortable: true },
  { col: "side", label: "Side", defaultWidth: 56, minWidth: 48, align: "left", sortable: true },
  { col: "qty", label: "Qty@Px", defaultWidth: 96, minWidth: 80, align: "right", sortable: true },
  { col: "state", label: "State", defaultWidth: 76, minWidth: 64, align: "left", sortable: true },
  { col: "actions", label: "", defaultWidth: 64, minWidth: 60, align: "right", sortable: false },
];
const ORDERS_SORT_ACCESSORS: Record<string, (r: OrderView) => number | string | null> = {
  createdMs: (r) => r.order.createdMs,
  symbol: (r) => r.order.symbol,
  side: (r) => r.order.side,
  qty: (r) => (r.order.leavesQty > 0 ? r.order.leavesQty : r.order.qty),
  state: (r) => STATUS_LABEL[displayStatus(r.order, r.optimistic)],
};

function readOrdersSort(s: Record<string, unknown>): SortState {
  const raw = s.ordersSort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return ORDERS_DEFAULT_SORT;
}

const CLOSED_DEFAULT_SORT: SortState = { col: "updatedMs", dir: "desc" };
const CLOSED_COLUMNS: (ResizableColumn & { align: "left" | "right"; sortable: boolean })[] = [
  { col: "updatedMs", label: "Closed", defaultWidth: 120, minWidth: 104, align: "left", sortable: true },
  { col: "symbol", label: "Symbol", defaultWidth: 84, minWidth: 68, align: "left", sortable: true },
  { col: "side", label: "Side", defaultWidth: 56, minWidth: 48, align: "left", sortable: true },
  { col: "qty", label: "Qty", defaultWidth: 56, minWidth: 48, align: "right", sortable: true },
  { col: "executedQty", label: "Filled", defaultWidth: 56, minWidth: 48, align: "right", sortable: true },
  { col: "price", label: "Price", defaultWidth: 82, minWidth: 64, align: "right", sortable: false },
  { col: "avgFillPrice", label: "Avg Fill", defaultWidth: 84, minWidth: 68, align: "right", sortable: true },
  { col: "state", label: "State", defaultWidth: 72, minWidth: 64, align: "left", sortable: true },
  { col: "reason", label: "Reason", defaultWidth: 120, minWidth: 96, align: "left", sortable: false },
  { col: "venue", label: "Venue", defaultWidth: 84, minWidth: 68, align: "left", sortable: true },
];
const CLOSED_SORT_ACCESSORS: Record<string, (r: ClosedOrder) => number | string | null> = {
  updatedMs: (r) => r.updatedMs,
  symbol: (r) => bareSymbol(r.symbol),
  side: (r) => r.side,
  qty: (r) => r.qty,
  executedQty: (r) => r.executedQty,
  avgFillPrice: (r) => r.executedQty > 0 ? r.avgFillPrice : null,
  state: (r) => STATUS_LABEL[r.status],
  venue: (r) => r.venue,
};

function readClosedSort(s: Record<string, unknown>): SortState {
  const raw = s.closedOrdersSort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return CLOSED_DEFAULT_SORT;
}

function orderInstructionPrice(order: Pick<Order, "type" | "limitPrice" | "stopPrice"> | Pick<ClosedOrder, "type" | "limitPrice" | "stopPrice">): string {
  if (order.type === "MARKET") return "MKT";
  if (order.type === "LIMIT") return `${formatPrice(order.limitPrice, 3)} LMT`;
  if (order.type === "STOP") return `${formatPrice(order.stopPrice, 3)} STP`;
  return `${formatPrice(order.stopPrice, 3)} / ${formatPrice(order.limitPrice, 3)} STPLMT`;
}

type UpperOrdersTab = "open" | "closed";

function OrdersTable({
  stores, oc, palette, config, onConfigChange, venue, height, availableWidth,
}: {
  stores: PanelProps["stores"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
  height: number;
  availableWidth: number;
}): JSX.Element {
  const [tab, setTab] = useState<UpperOrdersTab>("open");
  const [openSort, setOpenSort] = useState<SortState>(() => readOrdersSort(config.settings));
  const [closedSort, setClosedSort] = useState<SortState>(() => readClosedSort(config.settings));
  const openResize = useResizableColumns(config.settings, "openOrdersColumnWidths", ORDERS_COLUMNS, onConfigChange, availableWidth);
  const closedResize = useResizableColumns(config.settings, "closedOrdersColumnWidths", CLOSED_COLUMNS, onConfigChange, availableWidth);
  const views = sortRows(stores.exec.orders().filter((v) => v.order.venue === venue && (v.optimistic || isWorking(v.order.status))), openSort, ORDERS_SORT_ACCESSORS);
  const closedRows = sortRows(stores.exec.closedOrders().filter((o) => o.venue === venue && isTerminal(o.status)), closedSort, CLOSED_SORT_ACCESSORS);
  const reconciling = (stores.exec.status()?.venues ?? []).some((v) => v.reconcilePending);

  const clickOpenSort = (col: string, sortable: boolean) => {
    if (!sortable) return;
    const next = toggleSort(openSort, col);
    setOpenSort(next);
    onConfigChange({ ordersSort: next });
  };
  const clickClosedSort = (col: string, sortable: boolean) => {
    if (!sortable) return;
    const next = toggleSort(closedSort, col);
    setClosedSort(next);
    onConfigChange({ closedOrdersSort: next });
  };

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, zIndex: 1, background: palette.surface };
  const upperTab = (label: string, active: boolean, onClick: () => void, testid: string) => (
    <button data-testid={testid} onClick={onClick} style={{
      fontSize: 12, padding: "3px 8px", background: "transparent", border: "none",
      borderBottom: active ? `2px solid ${palette.accent}` : "2px solid transparent",
      color: active ? palette.text : palette.textMuted, cursor: "pointer", fontWeight: active ? 600 : 400,
    }}>{label}</button>
  );
  return (
    <div data-testid="orders-table" style={{ height, flexShrink: 0, overflow: "hidden", display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
        {upperTab(`Open Orders (${views.length})`, tab === "open", () => setTab("open"), "open-orders-tab")}
        {upperTab(`Closed Orders (${closedRows.length})`, tab === "closed", () => setTab("closed"), "closed-orders-tab")}
        {tab === "open" && <HoverButton data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")}
          style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.warn}`, background: "transparent", color: palette.warn, cursor: "pointer" }}>Cancel All</HoverButton>}
        {reconciling && (
          <span data-testid="reconcile-badge" className="chip chip-pending" style={{ marginLeft: "auto" }}>
            stream gap — reconciled, verify
          </span>
        )}
      </div>
      {tab === "open" ? <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <table ref={openResize.tableRef} data-testid="open-orders-table" style={{ width: "100%", minWidth: openResize.totalWidth, tableLayout: "fixed", borderCollapse: "collapse", whiteSpace: "nowrap" }}>
          <ColumnGroup columns={ORDERS_COLUMNS} widths={openResize.widths} />
          <thead><tr style={{ color: palette.textMuted, textAlign: "center" }}>
            {ORDERS_COLUMNS.map((c) => <th key={c.col} data-column={c.col} style={{ ...th, textAlign: "center", cursor: c.sortable ? "pointer" : "default" }} onClick={() => clickOpenSort(c.col, c.sortable)}
              className={`col-head${c.sortable && openSort?.col === c.col && !(c.col === "createdMs" && openSort.dir === "desc") ? " sort-active" : ""}`}>
              {c.label} {c.sortable && openSort?.col === c.col && !(c.col === "createdMs" && openSort.dir === "desc") ? sortIndicator(openSort, c.col) : ""}
              <ColumnResizeHandle column={c} width={openResize.widths[c.col]} testId={`open-orders-resize-${c.col}`}
                onMouseDown={(event) => openResize.startResize(c.col, event)} onDoubleClick={() => openResize.autoFit(c.col)}
                onKeyDown={(event) => openResize.onKeyDown(c.col, event)} />
            </th>)}
          </tr></thead>
          <tbody>{views.map(({ order, optimistic }) => {
            const ds = displayStatus(order, optimistic);
            const variant = chipVariant(ds);
            const working = !optimistic && isWorking(order.status);
            return <tr key={order.id} style={{ textAlign: "center", borderTop: `1px solid ${palette.border}` }}>
              <td data-column="createdMs" style={{ padding: "2px 8px" }}>{formatEtDateTime(order.createdMs)}</td>
              <td data-column="symbol" style={{ padding: "2px 8px" }}>{bareSymbol(order.symbol)}</td>
              <td data-column="side" style={{ color: order.side === "BUY" || order.side === "COVER" ? palette.up : palette.down }}>{sideLabel(order.side)}</td>
              <td data-column="qty">{formatSize(order.leavesQty > 0 ? order.leavesQty : order.qty)} @ {orderInstructionPrice(order)}</td>
              <td data-column="state">{variant ? <span className={`chip chip-${variant}`} data-chip={variant}>{STATUS_LABEL[ds]}</span>
                : <span style={{ color: palette.textMuted }}>{STATUS_LABEL[ds]}</span>}</td>
              <td data-column="actions">{(working || optimistic) ? <HoverButton data-testid={`cancel-${order.id}`} onClick={() => void oc.cancel(order.venue, order.id)}
                style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Cancel</HoverButton> : null}</td>
            </tr>;
          })}</tbody>
        </table>
      </div> : <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <table ref={closedResize.tableRef} data-testid="closed-orders-table" style={{ width: "100%", minWidth: closedResize.totalWidth, tableLayout: "fixed", borderCollapse: "collapse", whiteSpace: "nowrap" }}>
          <ColumnGroup columns={CLOSED_COLUMNS} widths={closedResize.widths} />
          <thead><tr style={{ color: palette.textMuted, textAlign: "center" }}>
            {CLOSED_COLUMNS.map((c) => <th key={c.col} data-column={c.col} style={{ ...th, textAlign: "center", cursor: c.sortable ? "pointer" : "default" }}
              onClick={() => clickClosedSort(c.col, c.sortable)} className={`col-head${c.sortable && closedSort?.col === c.col && !(c.col === "updatedMs" && closedSort.dir === "desc") ? " sort-active" : ""}`}>
              {c.label} {c.sortable && closedSort?.col === c.col && !(c.col === "updatedMs" && closedSort.dir === "desc") ? sortIndicator(closedSort, c.col) : ""}
              <ColumnResizeHandle column={c} width={closedResize.widths[c.col]} testId={`closed-orders-resize-${c.col}`}
                onMouseDown={(event) => closedResize.startResize(c.col, event)} onDoubleClick={() => closedResize.autoFit(c.col)}
                onKeyDown={(event) => closedResize.onKeyDown(c.col, event)} />
            </th>)}
          </tr></thead>
          <tbody>{closedRows.map((order) => {
            const danger = order.status === "REJECTED" || order.status === "BLOCKED";
            const muted = order.status === "CANCELED" || order.status === "EXPIRED" || order.status === "REPLACED";
            const reason = order.rejectReason || "—";
            return <tr key={order.id} style={{ textAlign: "center", borderTop: `1px solid ${palette.border}` }}>
              <td data-column="updatedMs" style={{ padding: "2px 8px" }}>{formatEtDateTime(order.updatedMs)}</td>
              <td data-column="symbol" style={{ padding: "2px 8px" }}>{bareSymbol(order.symbol)}</td>
              <td data-column="side" style={{ color: order.side === "BUY" || order.side === "COVER" ? palette.up : palette.down }}>{sideLabel(order.side)}</td>
              <td data-column="qty">{formatSize(order.qty)}</td>
              <td data-column="executedQty">{formatSize(order.executedQty)}</td>
              <td data-column="price">{orderInstructionPrice(order)}</td>
              <td data-column="avgFillPrice">{order.executedQty > 0 ? formatPrice(order.avgFillPrice, 3) : "—"}</td>
              <td data-column="state">{danger ? <span className="chip chip-rejected" data-chip="rejected">{STATUS_LABEL[order.status]}</span>
                : <span style={{ color: muted ? palette.textMuted : palette.text }}>{STATUS_LABEL[order.status]}</span>}</td>
              <td data-column="reason" style={{ maxWidth: 180 }}><span title={order.rejectReason || undefined} style={{ display: "block", maxWidth: 180, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{reason}</span></td>
              <td data-column="venue" style={{ color: palette.textMuted }}>{order.venue}</td>
            </tr>;
          })}</tbody>
        </table>
      </div>}
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
  const cash = account?.availableCash ?? null;
  const bp = account?.buyingPower ?? null;
  const dayPnl = account?.dayPnl ?? null;
  const realized = account?.realized ?? null;
  const unrealized = stores.exec.positions()
    .filter((p) => p.venue === venue && p.qty !== 0)
    .reduce((sum, p) => sum + displayUnrealized(stores.quote.get(p.symbol), p.avgPrice, p.qty, p.unrealizedPnl), 0);

  // Equity/Buying Power read as "settled" values: freeze them at their last
  // flat (no open position) snapshot while a position is open for this venue,
  // and resume live updates once the venue goes flat again. Cash, Day P&L,
  // Unrealized (quote-derived), and Realized stay live always — this freeze is
  // scoped to Equity/BP only.
  const positionOpen = stores.exec.positions().some((p) => p.venue === venue && p.qty !== 0);

  // Held pairs are keyed by venue (not a bare pair of refs) because StatsStrip
  // is a single long-lived instance shared across every venue in the <select>
  // — a reset-on-venue-change ref would lose venue A's held snapshot for good
  // the moment the trader glances at venue B and back, even though A's
  // position (and its freeze) never closed. A venue-keyed map survives that
  // round trip: each venue keeps its own last-flat snapshot independently, and
  // switching venues never reads or writes another venue's entry.
  const heldRef = useRef<Map<string, { equity: number; bp: number }>>(new Map());
  if (!positionOpen && equity !== null && bp !== null) {
    heldRef.current.set(venue, { equity, bp });
  }
  const held = heldRef.current.get(venue);
  const displayEquity = positionOpen ? held?.equity ?? equity : equity;
  const displayBp = positionOpen ? held?.bp ?? bp : bp;

  const cell = (label: string, testid: string, value: string, tone?: number) => (
    <div style={{ display: "flex", flexDirection: "column", padding: "2px 10px" }}>
      <span style={{ fontSize: 10, color: palette.textMuted }}>{label}</span>
      <span data-testid={testid} className="mono" style={{ fontSize: 13, color: tone === undefined ? palette.text : tone >= 0 ? palette.up : palette.down }}>{value}</span>
    </div>
  );

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 4, padding: "4px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      {cell("Equity", "acct-equity", money(displayEquity))}
      {cell("Cash", "acct-cash", money(cash))}
      {cell("Buying Power", "acct-bp", money(displayBp))}
      {cell("Day P&L", "acct-daypnl", money(dayPnl), dayPnl ?? 0)}
      {cell("Unrealized", "acct-unrealized", money(unrealized), unrealized)}
      {cell("Realized", "acct-realized", money(realized), realized ?? 0)}
      <div style={{ flex: 1 }} />
    </div>
  );
}

// ---- Positions table (folded from PositionsPanel, now sortable via T16) ----

function PositionsTable({
  stores, commands, oc, palette, config, onConfigChange, venue, extBufferPct, onOpenSymbol, availableWidth,
}: {
  stores: PanelProps["stores"];
  commands: PanelProps["commands"];
  oc: ReturnType<typeof useOrderCommands>;
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
  extBufferPct: number;
  onOpenSymbol: (symbol: string) => void;
  availableWidth: number;
}): JSX.Element {
  const toast = useToasts();
  const rows0 = stores.exec.positions().filter((p) => p.venue === venue && p.qty !== 0); // venue-scoped; NET (venue===null) rows drop out
  const status = stores.exec.status();
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const [selectedPosition, setSelectedPosition] = useState<string | null>(null);
  const [hoveredPosition, setHoveredPosition] = useState<string | null>(null);
  const resize = useResizableColumns(config.settings, "posColumnWidths", COLUMNS, onConfigChange, availableWidth);
  const masterArmed = !!status?.masterArmed;

  const rows = useMemo(() => sortRows(rows0, sort, {
    ...SORT_ACCESSORS,
    unrealizedPnl: (r: PositionRow) => {
      const quote = stores.quote.get(r.symbol);
      return displayUnrealized(quote, r.avgPrice, r.qty, r.unrealizedPnl);
    },
  }), [rows0, sort]);
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
    const r = resolvePlaceTemplate(t, { venue, symbol: row.symbol, quote, buyingPower: 0, availableCash: 0, positionQty: row.qty, nowMs: Date.now(), extHoursMarketBufferPct: extBufferPct });
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
        <table ref={resize.tableRef} style={{ width: "100%", minWidth: resize.totalWidth, tableLayout: "fixed", borderCollapse: "collapse", whiteSpace: "nowrap" }}>
          <ColumnGroup columns={COLUMNS} widths={resize.widths} />
          <thead>
            <tr style={{ color: palette.textMuted, textAlign: "center" }}>
              {COLUMNS.map((c) => (
                <th key={c.col} data-column={c.col} style={{ ...th, textAlign: "center", cursor: c.sortable ? "pointer" : "default" }}
                  onClick={() => clickSort(c.col, c.sortable)}>
                  {c.label} {c.sortable ? sortIndicator(sort, c.col) : ""}
                  <ColumnResizeHandle column={c} width={resize.widths[c.col]} testId={`positions-resize-${c.col}`}
                    onMouseDown={(event) => resize.startResize(c.col, event)} onDoubleClick={() => resize.autoFit(c.col)}
                    onKeyDown={(event) => resize.onKeyDown(c.col, event)} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const net = r.venue === null;
              const positionKey = `${r.venue ?? "NET"}:${r.symbol}`;
              const selected = positionKey === selectedPosition;
              return (
                <tr key={positionKey} data-testid={net ? "pos-net" : `pos-row-${r.venue}-${r.symbol}`} aria-selected={selected}
                  onClick={() => setSelectedPosition(positionKey)}
                  onDoubleClick={() => onOpenSymbol(r.symbol)}
                  onMouseEnter={() => setHoveredPosition(positionKey)}
                  onMouseLeave={() => setHoveredPosition((key) => key === positionKey ? null : key)}
                  style={{ cursor: "pointer", textAlign: "center", userSelect: "none", borderTop: `1px solid ${palette.border}`, fontWeight: net ? 700 : 400,
                    background: selected ? "rgba(154,106,27,.16)" : hoveredPosition === positionKey ? "rgba(154,106,27,.06)" : "transparent",
                    boxShadow: selected ? `inset 0 0 0 1px ${palette.accent}` : "none", transition: "background 120ms ease" }}>
                  <td data-column="symbol" style={{ padding: "2px 8px" }}>{bareSymbol(r.symbol)}</td>
                  <td data-column="venue" style={{ color: palette.textMuted }}>{net ? "NET" : r.venue}</td>
                  <td data-column="qty" style={{ color: r.qty >= 0 ? palette.up : palette.down }}>{formatSize(r.qty)}</td>
                  <td data-column="avgPrice">{formatPrice(r.avgPrice, 2)}</td>
                  {(() => {
                    const quote = stores.quote.get(r.symbol);
                    const dUnrealized = displayUnrealized(quote, r.avgPrice, r.qty, r.unrealizedPnl);
                    return <td data-column="unrealizedPnl" style={{ color: dUnrealized >= 0 ? palette.up : palette.down }}>{formatPrice(dUnrealized, 2)}</td>;
                  })()}
                  <td data-column="flatten">{net ? null : (
                    <HoverButton data-testid={`flatten-${r.venue}-${r.symbol}`} data-armed={masterArmed}
                      title={masterArmed ? "Flatten position" : "Master disarmed — flatten still allowed (exposure-reducing)"}
                      onClick={(e) => { e.stopPropagation(); flatten(r); }} onDoubleClick={(e) => e.stopPropagation()}
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

const FILLS_DEFAULT_SORT: SortState = { col:"time", dir:"desc" };
const FILLS_COLUMNS: ResizableColumn[] = [
  { col:"symbol", label:"Symbol", defaultWidth:84, minWidth:68 }, { col:"side", label:"Side", defaultWidth:56, minWidth:48 }, { col:"qty", label:"Qty", defaultWidth:56, minWidth:48 },
  { col:"price", label:"Price", defaultWidth:72, minWidth:60 }, { col:"time", label:"Time", defaultWidth:84, minWidth:68 }, { col:"orderId", label:"Order ID", defaultWidth:140, minWidth:100 },
];
const FILLS_SORT_ACCESSORS: Record<string, (f:Fill) => number|string> = {
  symbol:(f) => bareSymbol(f.symbol), side:(f) => f.side, qty:(f) => f.qty,
  price:(f) => f.price, time:(f) => f.tsMs, orderId:(f) => f.orderId,
};

function readFillsSort(settings:Record<string, unknown>):SortState {
  const raw = settings.fillsSort as { col?:unknown; dir?:unknown } | undefined;
  return raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc") ? { col:raw.col, dir:raw.dir } : FILLS_DEFAULT_SORT;
}

function FillsTable({ stores, palette, venue, cycleStartMs, config, onConfigChange, availableWidth }: {
  stores: PanelProps["stores"]; palette: ReturnType<typeof useTheme>["palette"];
  venue: string; cycleStartMs:number; config:PanelProps["config"]; onConfigChange:PanelProps["onConfigChange"];
  availableWidth: number;
}): JSX.Element {
  const [sort, setSort] = useState<SortState>(() => readFillsSort(config.settings));
  const resize = useResizableColumns(config.settings, "fillsColumnWidths", FILLS_COLUMNS, onConfigChange, availableWidth);
  const fills = sortRows(stores.fills.forVenue(venue, cycleStartMs), sort, FILLS_SORT_ACCESSORS);
  const cell = { textAlign:"center" as const, padding:"2px 8px" };
  const clickSort = (col:string) => { const next = toggleSort(sort, col); setSort(next); onConfigChange({ fillsSort:next }); };
  return <div data-testid="fills-table" style={{ overflow:"auto", flex:1, fontSize:12 }}>
    <table ref={resize.tableRef} style={{ width:"100%", minWidth:resize.totalWidth, tableLayout:"fixed", borderCollapse:"collapse", whiteSpace:"nowrap" }}><ColumnGroup columns={FILLS_COLUMNS} widths={resize.widths} /><thead><tr style={{ color:palette.textMuted, background:palette.surface }}>
      {FILLS_COLUMNS.map((c) => <th key={c.col} data-column={c.col} className={`col-head${sort?.col === c.col ? " sort-active" : ""}`}
        style={{ ...cell, position:"sticky", top:0, zIndex:1, cursor:"pointer" }} onClick={() => clickSort(c.col)}>{c.label} {sortIndicator(sort, c.col)}
        <ColumnResizeHandle column={c} width={resize.widths[c.col]} testId={`fills-resize-${c.col}`}
          onMouseDown={(event) => resize.startResize(c.col, event)} onDoubleClick={() => resize.autoFit(c.col)}
          onKeyDown={(event) => resize.onKeyDown(c.col, event)} />
      </th>)}
    </tr></thead><tbody>{fills.map((f, n) => <tr key={`${f.orderId}-${f.tsMs}-${n}`} style={{ borderTop:`1px solid ${palette.border}` }}>
      <td data-column="symbol" style={cell}>{bareSymbol(f.symbol)}</td><td data-column="side" style={cell}>{sideLabel(f.side)}</td>
      <td data-column="qty" style={cell}>{formatSize(f.qty)}</td><td data-column="price" style={cell}>{formatPrice(f.price, 2)}</td>
      <td data-column="time" style={cell}>{formatClock(f.tsMs)}</td><td data-column="orderId" style={cell}>{f.orderId}</td>
    </tr>)}</tbody></table>
  </div>;
}

type Tab = "positions" | "history" | "fills";

export function AccountPanel({ config, stores, commands, onConfigChange, linkGroups, group: groupProp, width, height }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  useSyncExternalStore((cb) => stores.fills.subscribe(cb), () => stores.fills.getRev());
  // Force re-render on quote updates so live Unrl P&L refreshes.
  useSyncExternalStore((cb) => stores.quote.subscribe(cb), () => stores.quote.getRev());
  const group = groupProp === undefined ? config.group : groupProp;
  const openPositionSymbol = (symbol: string) => linkGroups.focus(group ?? "green", symbol);
  const { venue, venues, selectVenue } = useVenueSelection(group, linkGroups, stores);
  const { config: orderConfig } = useOrderConfig();
  const extBufferPct = orderConfig.extHoursMarketBufferPct ?? 1;
  // Portaled into PanelFrame's ledger-header actions slot, beside the close
  // button (see headerSlot.ts's PanelHeaderActionsSlotContext). undefined (no
  // frame above, e.g. a body-level test) falls back to rendering inline; null
  // (frame present, slot div not yet mounted) renders nothing for that tick.
  const actionsSlot = useContext(PanelHeaderActionsSlotContext);
  const [exportOpen, setExportOpen] = useState(false);
  const exportBtnRef = useRef<HTMLButtonElement | null>(null);
  const venueSelect = (
    <select data-testid="acct-venue" className="ctl mono" value={venue} onChange={(e) => selectVenue(e.target.value)}
      style={{ padding: "2px 9px", margin: "1px 0" }}>
      {venues.map((v) => <option key={v} value={v}>{v}</option>)}
    </select>
  );
  const headerActions = (
    <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
      {venueSelect}
    </div>
  );

  const [ordersHeight, setOrdersHeight] = useState<number>(() => {
    const raw = config.settings.ordersHeight;
    return typeof raw === "number" && raw >= 80 ? raw : 200;
  });
  const [activeTab, setActiveTab] = useState<Tab>(() => config.settings.tab === "history" || config.settings.tab === "fills" ? config.settings.tab : "positions");
  const accountCycleStart = stores.exec.accounts().find((a) => a.venue === venue)?.cycleStartMs ?? 0;
  const [fillCycleStart, setFillCycleStart] = useState(accountCycleStart);
  useEffect(() => {
    let live = true;
    void commands.sendQuery("QueryCycleFills", { venue }).then((raw) => {
      if (!live) return;
      const r = raw as { cycleStartMs?:number; fills?:Fill[] };
      stores.fills.ingest(r.fills ?? []);
      setFillCycleStart(r.cycleStartMs ?? accountCycleStart);
    }).catch(() => { /* reconnect settles the next cycle query */ });
    return () => { live = false; };
  }, [venue, accountCycleStart, commands, stores.fills]);

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

  const positionsCount = stores.exec.positions().filter((p) => p.venue === venue && p.qty !== 0).length;

  const tabBtn = (label: string, active: boolean, onClick: () => void) => (
    <button onClick={onClick} style={{
      fontSize: 12, padding: "4px 10px", background: "transparent", border: "none",
      borderBottom: active ? `2px solid ${palette.accent}` : "2px solid transparent",
      color: active ? palette.text : palette.textMuted, cursor: "pointer",
    }}>{label}</button>
  );

  return (
    <div style={{ height: "100%", display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontFamily: "inherit" }}>
      {actionsSlot === undefined ? headerActions : actionsSlot ? createPortal(headerActions, actionsSlot) : null}
      <StatsStrip stores={stores} palette={palette} venue={venue} />
      <OrdersTable stores={stores} oc={oc} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} height={ordersHeight} availableWidth={width} />
      <div data-testid="orders-resize-handle" onMouseDown={startResize}
        style={{ height: 4, cursor: "row-resize", background: palette.border, flexShrink: 0 }} />
      <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <div style={{ display: "flex", alignItems: "center", borderBottom: `1px solid ${palette.border}`, background: palette.surface }}>
          {tabBtn(`Positions (${positionsCount})`, activeTab === "positions", () => selectTab("positions"))}
          {tabBtn("Trade History", activeTab === "history", () => selectTab("history"))}
          {tabBtn("Fills", activeTab === "fills", () => selectTab("fills"))}
          <div style={{ flex: 1 }} />
          {activeTab === "history" && (
            <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "0 8px" }}>
              <HoverButton ref={exportBtnRef} data-testid="acct-export" className="ctl mono" aria-haspopup="menu" aria-expanded={exportOpen}
                onClick={() => setExportOpen((v) => !v)} style={{ background: "transparent" }}>
                Export
              </HoverButton>
              {exportOpen && (
                <ExportTradesPopover palette={palette} anchor={exportBtnRef.current} venue={venue} commands={commands} toast={toast}
                  onClose={() => setExportOpen(false)} />
              )}
            </div>
          )}
        </div>
        {activeTab === "positions" ? <PositionsTable stores={stores} commands={commands} oc={oc} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} extBufferPct={extBufferPct} onOpenSymbol={openPositionSymbol} availableWidth={width} />
          : activeTab === "history" ? <TradeHistoryTable stores={stores} palette={palette} config={config} onConfigChange={onConfigChange} venue={venue} availableWidth={width} />
          : <FillsTable stores={stores} palette={palette} venue={venue} cycleStartMs={fillCycleStart} config={config} onConfigChange={onConfigChange} availableWidth={width} />}
      </div>
    </div>
  );
}
