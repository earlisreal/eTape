import { useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import { useTheme } from "../ThemeProvider";
import { useToasts } from "../Toast";
import { useOrderCommands } from "../exec/useOrderCommands";
import { displayStatus, STATUS_LABEL, sideLabel, bareSymbol, abbrevType, isWorking, type DisplayStatus } from "../exec/orderStatus";
import { formatPrice, formatSize } from "../../render/format";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import type { OrderView } from "../../data/ExecStore";

// Lifecycle → chip variant. WORKING (Submitted/Accepted/Partially filled) is the one
// state in this panel that borrows the green/up outline — it signals "in flight,
// proceeding normally," the same exception the connection-status dot uses, not a
// market-direction read. PENDING/REPLACING are bronze (state, not direction).
// REJECTED/BLOCKED are danger — never confused with the down/red market color.
type ChipVariant = "working" | "pending" | "rejected";
function chipVariant(ds: DisplayStatus): ChipVariant | null {
  if (ds === "SUBMITTED" || ds === "ACCEPTED" || ds === "PARTIALLY_FILLED") return "working";
  if (ds === "PendingNew" || ds === "Replacing") return "pending";
  if (ds === "REJECTED" || ds === "BLOCKED") return "rejected";
  return null; // terminal others (Filled/Canceled/Expired/Replaced) — plain muted text
}

const DEFAULT_SORT: SortState = { col: "createdMs", dir: "desc" };
const COLUMNS: { col: string; label: string; align: "left" | "right" }[] = [
  { col: "symbol", label: "Symbol", align: "left" },
  { col: "side", label: "Side", align: "left" },
  { col: "qty", label: "Qty@Px", align: "right" },
  { col: "state", label: "State", align: "left" },
];
const SORT_ACCESSORS: Record<string, (r: OrderView) => number | string | null> = {
  symbol: (r) => r.order.symbol,
  side: (r) => r.order.side,
  qty: (r) => (r.order.leavesQty > 0 ? r.order.leavesQty : r.order.qty),
  state: (r) => STATUS_LABEL[displayStatus(r.order, r.optimistic)],
  createdMs: (r) => r.order.createdMs,
};

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.sort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

export function OpenOrdersPanel({ config, stores, commands, onConfigChange }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));

  const views = sortRows(stores.exec.orders(), sort, SORT_ACCESSORS);
  const reconciling = (stores.exec.status()?.venues ?? []).some((v) => v.reconcilePending);

  const clickSort = (col: string) => {
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ sort: next });
  };

  const th = { padding: "2px 8px" };
  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 8px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
        <span style={{ fontWeight: 600 }}>Open Orders</span>
        <button data-testid="cancel-all" onClick={() => void oc.cancelAll("everything")}
          style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.warn}`, background: "transparent", color: palette.warn, cursor: "pointer" }}>Cancel All</button>
        {reconciling && (
          <span data-testid="reconcile-badge" className="chip chip-pending" style={{ marginLeft: "auto" }}>
            stream gap — reconciled, verify
          </span>
        )}
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ color: palette.textMuted }}>
            {COLUMNS.map((c) => (
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
                  <button data-testid={`cancel-${order.id}`} onClick={() => void oc.cancel(order.venue, order.id)}
                    style={{ fontSize: 10, padding: "1px 6px", border: `1px solid ${palette.border}`, background: "transparent", color: palette.text, cursor: "pointer" }}>Cancel</button>
                ) : null}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
