import { useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ClosedTradeRow } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { formatPrice, formatSize, formatClock, formatDuration } from "../../render/format";
import { bareSymbol } from "../exec/orderStatus";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import { ColumnGroup, ColumnResizeHandle, useResizableColumns, type ResizableColumn } from "./ResizableColumns";

// Task 6 (plan B6): read-only history of closed round trips, sibling to
// PositionsTable inside the same Account panel (Task 7 wires both into a
// tabbed body — this file is not imported yet). Same table shell/sort
// pattern as PositionsTable, minus row actions (closed trades are immutable)
// and the NET aggregate row (every ClosedTradeRow already belongs to exactly
// one venue).

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

const COLUMNS: (ResizableColumn & { align: "left" | "right"; sortable: boolean })[] = [
  { col: "symbol", label: "Symbol", defaultWidth: 84, minWidth: 68, align: "left", sortable: true },
  { col: "venue", label: "Venue", defaultWidth: 92, minWidth: 72, align: "right", sortable: true },
  { col: "qty", label: "Qty", defaultWidth: 56, minWidth: 48, align: "right", sortable: true },
  { col: "entryPrice", label: "Entry", defaultWidth: 72, minWidth: 60, align: "right", sortable: true },
  { col: "exitPrice", label: "Exit", defaultWidth: 72, minWidth: 60, align: "right", sortable: true },
  { col: "realized", label: "Realized", defaultWidth: 84, minWidth: 72, align: "right", sortable: true },
  { col: "openMs", label: "Opened", defaultWidth: 84, minWidth: 72, align: "right", sortable: true },
  { col: "closeMs", label: "Closed", defaultWidth: 84, minWidth: 72, align: "right", sortable: true },
  { col: "duration", label: "Duration", defaultWidth: 76, minWidth: 64, align: "right", sortable: true },
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
  stores, palette, config, onConfigChange, venue, availableWidth,
}: {
  stores: PanelProps["stores"];
  palette: ReturnType<typeof useTheme>["palette"];
  config: PanelProps["config"];
  onConfigChange: PanelProps["onConfigChange"];
  venue: string;
  availableWidth: number;
}): JSX.Element {
  useSyncExternalStore((cb) => stores.trades.subscribe(cb), () => stores.trades.getSnapshot());
  const rows0 = stores.trades.trades().filter((r) => r.venue === venue);
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const resize = useResizableColumns(config.settings, "tradesColumnWidths", COLUMNS, onConfigChange, availableWidth);
  const rows = useMemo(() => sortRows(rows0, sort, SORT_ACCESSORS), [rows0, sort]);

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
        <table ref={resize.tableRef} data-testid="trade-history-table" style={{ width: "100%", minWidth: resize.totalWidth, tableLayout: "fixed", borderCollapse: "collapse", whiteSpace: "nowrap" }}>
          <ColumnGroup columns={COLUMNS} widths={resize.widths} />
          <thead>
            <tr style={{ color: palette.textMuted, textAlign: "center" }}>
              {COLUMNS.map((c) => (
                <th key={c.col} data-column={c.col} style={{ ...th, textAlign: "center", cursor: c.sortable ? "pointer" : "default" }}
                  onClick={() => clickSort(c.col, c.sortable)}>
                  {c.label} {c.sortable ? sortIndicator(sort, c.col) : ""}
                  <ColumnResizeHandle column={c} width={resize.widths[c.col]} testId={`trades-resize-${c.col}`}
                    onMouseDown={(event) => resize.startResize(c.col, event)} onDoubleClick={() => resize.autoFit(c.col)}
                    onKeyDown={(event) => resize.onKeyDown(c.col, event)} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.seq} data-testid={`trade-${r.seq}`} style={{ textAlign: "center", borderTop: `1px solid ${palette.border}` }}>
                <td data-column="symbol" style={{ padding: "2px 8px" }}>{bareSymbol(r.symbol)}</td>
                <td data-column="venue" style={{ color: palette.textMuted }}>{r.venue}</td>
                <td data-column="qty">{formatSize(r.qty)}</td>
                <td data-column="entryPrice">{formatPrice(r.entryPrice, 2)}</td>
                <td data-column="exitPrice">{formatPrice(r.exitPrice, 2)}</td>
                <td data-column="realized" style={{ color: r.realized >= 0 ? palette.up : palette.down }}>{formatPrice(r.realized, 2)}</td>
                <td data-column="openMs" style={{ color: palette.textMuted }}>{formatClock(r.openMs)}</td>
                <td data-column="closeMs" style={{ color: palette.textMuted }}>{formatClock(r.closeMs)}</td>
                <td data-column="duration" style={{ color: palette.textMuted }}>{formatDuration(r.closeMs - r.openMs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
