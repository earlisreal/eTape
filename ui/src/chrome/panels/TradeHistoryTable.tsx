import { useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ClosedTradeRow } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { formatPrice, formatSize, formatClock, formatDuration } from "../../render/format";
import { bareSymbol } from "../exec/orderStatus";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";

// Task 6 (plan B6): read-only history of closed round trips, sibling to
// PositionsTable inside the same Account panel (Task 7 wires both into a
// tabbed body — this file is not imported yet). Same table shell/sort
// pattern as PositionsTable, minus row actions (closed trades are immutable)
// and the NET aggregate row (every ClosedTradeRow already belongs to exactly
// one venue).

// Distinct from PositionsTable's money() (AccountPanel.tsx) — deliberately not
// imported from there (that file stays untouched by this task) even though the
// ± convention (unicode minus, $ before the magnitude) matches exactly.
const money = (n: number): string => (n < 0 ? "−$" : "$") + formatPrice(Math.abs(n), 2);

// settings.tradesSort is a DISTINCT key from PositionsTable's settings.sort —
// both tables will live in the same panel config once Task 7 assembles them,
// so sharing the generic "sort" key would collide.
const DEFAULT_SORT: SortState = { col: "closeMs", dir: "desc" };

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.tradesSort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

const COLUMNS: { col: string; label: string; align: "left" | "right"; sortable: boolean }[] = [
  { col: "symbol", label: "Symbol", align: "left", sortable: true },
  { col: "venue", label: "Venue", align: "right", sortable: true },
  { col: "qty", label: "Qty", align: "right", sortable: true },
  { col: "entryPrice", label: "Entry", align: "right", sortable: true },
  { col: "exitPrice", label: "Exit", align: "right", sortable: true },
  { col: "realized", label: "Realized", align: "right", sortable: true },
  { col: "openMs", label: "Opened", align: "right", sortable: true },
  { col: "closeMs", label: "Closed", align: "right", sortable: true },
  { col: "duration", label: "Duration", align: "right", sortable: true },
];
const SORT_ACCESSORS: Record<string, (r: ClosedTradeRow) => number | string | null> = {
  symbol: (r) => bareSymbol(r.symbol),
  venue: (r) => r.venue,
  qty: (r) => r.qty,
  entryPrice: (r) => r.entryPrice,
  exitPrice: (r) => r.exitPrice,
  realized: (r) => r.realized,
  openMs: (r) => r.openMs,
  closeMs: (r) => r.closeMs,
  duration: (r) => r.closeMs - r.openMs,
};

export function TradeHistoryTable({
  stores, palette, config, onConfigChange, venue,
}: {
  stores: PanelProps["stores"];
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
}): JSX.Element {
  useSyncExternalStore((cb) => stores.trades.subscribe(cb), () => stores.trades.getSnapshot());
  const rows0 = stores.trades.trades().filter((r) => r.venue === venue);
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const rows = useMemo(() => sortRows(rows0, sort, SORT_ACCESSORS), [rows0, sort]);

  // Venue-scoped realized sum, computed locally (NOT stores.trades.dayRealized() —
  // that sums across ALL venues unconditionally per Task 5's design; see the
  // plan's resolved discrepancy note). Same filtered set as the row list above.
  const dayRealized = useMemo(() => rows0.reduce((sum, r) => sum + r.realized, 0), [rows0]);

  const clickSort = (col: string, sortable: boolean) => {
    if (!sortable) return;
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ tradesSort: next });
  };

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };

  return (
    <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ padding: "4px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
        {rows0.length} closed trade{rows0.length === 1 ? "" : "s"}
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
            {rows.map((r) => (
              <tr key={r.seq} style={{ textAlign: "right", borderTop: `1px solid ${palette.border}` }}>
                <td style={{ textAlign: "left", padding: "2px 8px" }}>{bareSymbol(r.symbol)}</td>
                <td style={{ color: palette.textMuted }}>{r.venue}</td>
                <td>{formatSize(r.qty)}</td>
                <td>{formatPrice(r.entryPrice, 2)}</td>
                <td>{formatPrice(r.exitPrice, 2)}</td>
                <td style={{ color: r.realized >= 0 ? palette.up : palette.down }}>{formatPrice(r.realized, 2)}</td>
                <td style={{ color: palette.textMuted }}>{formatClock(r.openMs)}</td>
                <td style={{ color: palette.textMuted }}>{formatClock(r.closeMs)}</td>
                <td style={{ color: palette.textMuted }}>{formatDuration(r.closeMs - r.openMs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div title="Computed from closed round trips — may differ from the account strip's broker-reported Realized figure (fees, overnight carry)."
        style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", padding: "4px 8px", borderTop: `1px solid ${palette.border}`, background: palette.surface }}>
        <span style={{ color: palette.textMuted }}>Day realized (closed round trips)</span>
        <span data-testid="trades-day-realized" className="mono" style={{ color: dayRealized >= 0 ? palette.up : palette.down }}>{money(dayRealized)}</span>
      </div>
    </div>
  );
}
