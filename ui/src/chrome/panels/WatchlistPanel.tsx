import { createPortal } from "react-dom";
import { useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type DragEvent } from "react";
import type { PanelProps } from "./registry";
import { ScannerSyncControl } from "./ScannerControls";
import { PanelHeaderActionsSlotContext } from "./headerSlot";
import type { WatchlistMoveArgs, WatchlistRow } from "../../gen/wsmsg";
import { useTheme } from "../ThemeProvider";
import { FONTS } from "../../render/palette";
import { formatPrice, quoteDecimals } from "../../render/format";
import { formatChangePct, formatCompactShares } from "../format";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import { bareSymbol } from "../exec/orderStatus";
import { TVContextMenu, type MenuEntry } from "./tv/TVContextMenu";
import { menuChrome } from "../menuChrome";
import { useToasts } from "../Toast";
import { useDismissibleLayer } from "../controls/useDismissibleLayer";

// A row view over the watchlist snapshot: symbols is authoritative membership +
// order, but rows may lag by up to one poll — a symbol absent from rows still
// renders (with dash placeholders), it never vanishes from the list.
interface WatchlistRowView {
  symbol: string;
  last: number | null;
  changePct: number | null;
  volume: number | null;
  sessionChangePct: number | null;
  sessionVolume: number | null;
}

const STALE_MS = 10_000;
const STALE_CHECK_MS = 2_000;

const COLUMNS = [
  { col: "sym", label: "Symbol", name: "Symbol" },
  { col: "last", label: "Last", name: "Last" },
  { col: "changePct", label: "%Chg", name: "%Chg" },
  { col: "sessionChangePct", label: "Session %", name: "Session Change %" },
  { col: "vol", label: "Volume", name: "Volume" },
  { col: "sessionVolume", label: "Session Vol", name: "Session Volume" },
] as const;
const METRICS = COLUMNS.filter((column) => column.col !== "sym");
type Metric = typeof METRICS[number]["col"];
const SESSION_LABELS = { pre: "Pre-market", rth: "Regular", post: "After-hours", overnight: "Overnight", closed: "Closed" };

function hiddenMetrics(value: unknown): Metric[] {
  return Array.isArray(value) ? METRICS.map(({ col }) => col).filter((col) => value.includes(col)) : [];
}
const SORT_ACCESSORS: Record<string, (r: WatchlistRowView) => number | string | null> = {
  sym: (r) => r.symbol,
  last: (r) => r.last,
  changePct: (r) => r.changePct,
  vol: (r) => r.volume,
  sessionChangePct: (r) => r.sessionChangePct,
  sessionVolume: (r) => r.sessionVolume,
};

function toRowView(symbol: string, row: WatchlistRow | undefined, showSession: boolean): WatchlistRowView {
  return {
    symbol, last: row?.last ?? null, changePct: row?.changePct ?? null, volume: row?.volume ?? null,
    sessionChangePct: (showSession ? row?.sessionChangePct : null) ?? null,
    sessionVolume: (showSession ? row?.sessionVolume : null) ?? null,
  };
}

function dropPosition(event: DragEvent<HTMLTableRowElement>): WatchlistMoveArgs["position"] {
  const bounds = event.currentTarget.getBoundingClientRect();
  return event.clientY < bounds.top + bounds.height / 2 ? "before" : "after";
}

export function WatchlistPanel({ config, stores, linkGroups, commands, onConfigChange, scannerSync, group: groupProp }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  // config.group is frozen at panel creation (dockview never re-invokes the panel
  // factory with a fresh config after a later swatch re-pick) — PanelFrame threads
  // the live re-picked group through as the `group` prop instead. Same pattern as
  // ChartPanel/LadderPanel/TapePanel/etc.
  const group = groupProp === undefined ? config.group : groupProp;
  const toast = useToasts();
  const snap = useSyncExternalStore((cb) => stores.watchlist.subscribe(cb), () => stores.watchlist.getSnapshot());
  const draggedSymbol = useRef<string | null>(null);
  const [dropTarget, setDropTarget] = useState<{ symbol: string; position: WatchlistMoveArgs["position"] } | null>(null);
  const [sort, setSort] = useState<SortState>(null);
  const [hidden, setHidden] = useState(() => hiddenMetrics(config.settings.watchlistHiddenColumns));
  const [columnsOpen, setColumnsOpen] = useState(false);
  const columnsRef = useRef<HTMLDivElement>(null);
  const columnsTrigger = useRef<HTMLButtonElement>(null);
  useDismissibleLayer(columnsRef, () => setColumnsOpen(false), { enabled: columnsOpen, anchor: columnsTrigger.current });
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);
  const [hoveredSymbol, setHoveredSymbol] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ clientX: number; clientY: number; symbol: string } | null>(null);
  const [addValue, setAddValue] = useState("");
  // Staleness is a function of wall-clock time vs. refreshedAt, not of the
  // snapshot's own identity — without a ticking clock the dimming would only
  // ever re-evaluate on the next store push (which may never come once the
  // poller stalls, exactly the case staleness exists to surface).
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), STALE_CHECK_MS);
    return () => clearInterval(id);
  }, []);

  const stale = snap.refreshedAt != null && now - Date.parse(snap.refreshedAt) > STALE_MS;
  const session = snap.received ? snap.session : null;
  const showSession = session !== null && session !== "closed";
  const sessionLabel = session && (!hidden.includes("sessionChangePct") || !hidden.includes("sessionVolume"))
    ? SESSION_LABELS[session] : null;
  const visible = COLUMNS.filter(({ col }) => col === "sym" || !hidden.includes(col));

  const rows = useMemo(() => {
    const views = snap.symbols.map((sym) => toRowView(sym, snap.rows.get(sym), showSession));
    return sortRows(views, sort, SORT_ACCESSORS);
  }, [snap.symbols, snap.rows, showSession, sort]);

  const updateHidden = (next: Metric[]) => {
    setHidden(next);
    onConfigChange({ watchlistHiddenColumns: next });
    if (next.some((col) => col === sort?.col)) setSort(null);
  };

  const move = (symbol: string, target: string | undefined, position: WatchlistMoveArgs["position"]) => {
    if (!target || symbol === target) return;
    const args: WatchlistMoveArgs = { symbol, target, position };
    void commands.sendCommand("WatchlistMove", args).then((ack) => {
      if (ack.status !== "accepted") {
        toast.push({ level: "warn", text: ack.reason ?? "Could not move Watchlist symbol." });
      }
    });
  };

  const clickSort = (col: string) => setSort((s) => s?.col === col && s.dir === "asc" ? null : toggleSort(s, col));

  const endDrag = () => {
    draggedSymbol.current = null;
    setDropTarget(null);
  };

  const submitAdd = () => {
    const symbol = addValue.trim();
    if (!symbol) return;
    void commands.sendCommand("WatchlistAdd", { symbol }).then((ack) => {
      if (ack.status === "accepted") {
        setAddValue("");
      } else {
        toast.push({ level: "warn", text: ack.reason ?? "rejected" });
      }
    });
  };

  const buildRowMenuItems = (sym: string): MenuEntry[] => [
    { label: `Remove ${bareSymbol(sym)} from watchlist`, danger: true,
      onClick: () => void commands.sendCommand("WatchlistRemove", { symbol: sym }) },
  ];

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  const symCell = { textAlign: "left" as const, padding: "2px 8px", fontFamily: FONTS.mono, fontWeight: 600 };
  const numCell = { padding: "2px 8px", fontFamily: FONTS.mono, fontWeight: 500, fontVariantNumeric: "tabular-nums" as const };

  const addInput = (
    <input
      aria-label="add symbol to watchlist"
      placeholder="Add symbol…"
      value={addValue}
      onChange={(e) => setAddValue(e.target.value)}
      onKeyDown={(e) => { if (e.key === "Enter") submitAdd(); }}
      style={{ padding: "3px 6px", fontFamily: FONTS.mono }}
    />
  );

  const headerActions = useContext(PanelHeaderActionsSlotContext);
  const actions = <>
    {scannerSync && <ScannerSyncControl sync={scannerSync} sourceName="Watchlist" />}
    <button type="button" ref={columnsTrigger} aria-expanded={columnsOpen} onClick={() => setColumnsOpen((open) => !open)}
      style={{ border: `1px solid ${palette.border}`, borderRadius: 3, background: palette.surface, color: palette.textMuted, cursor: "pointer", padding: "2px 5px", fontFamily: FONTS.mono, fontSize: 10, lineHeight: 1, flex: "0 0 auto" }}>
      Columns
    </button>
  </>;
  const controls = headerActions === undefined ? actions : headerActions && createPortal(actions, headerActions);
  const columnsMenu = columnsOpen && (
    <div ref={columnsRef} role="group" aria-label="Watchlist columns" className="popover"
      style={{ top: headerActions === undefined ? 28 : 6, right: 6, width: 172, maxWidth: "calc(100% - 12px)" }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {METRICS.map(({ col, name }) => (
          <label key={col} style={{ display: "flex", alignItems: "center", gap: 5 }}>
            <input type="checkbox" checked={!hidden.includes(col)}
              onChange={() => updateHidden(hidden.includes(col) ? hidden.filter((id) => id !== col) : [...hidden, col])} />
            {name}
          </label>
        ))}
        <button type="button" onClick={() => updateHidden([])}>Reset columns</button>
      </div>
    </div>
  );
  const panelStyle = { position: "relative" as const, height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 };

  if (snap.symbols.length === 0) {
    return (
      <div style={panelStyle}>
        {controls}
        {columnsMenu}
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 10, padding: 16 }}>
          <span style={{ color: palette.textMuted }}>Add a symbol to start your watchlist</span>
          {!group && <span>Choose a Link Group color to load symbols.</span>}
          {addInput}
        </div>
      </div>
    );
  }

  return (
    <div style={panelStyle}>
      {controls}
      {columnsMenu}
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}` }}>
        {addInput}
        {sessionLabel && <span style={{ color: palette.textMuted }}>{sessionLabel}</span>}
      </div>
      {!group && <p style={{ padding: "0 8px" }}>Choose a Link Group color to load symbols.</p>}
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ color: palette.textMuted, textAlign: "right" }}>
            {visible.map((c) => (
              <th key={c.col} style={{ ...th, textAlign: c.col === "sym" ? "left" : "right", cursor: "pointer", whiteSpace: "nowrap" }} onClick={() => clickSort(c.col)}
                aria-sort={sort?.col === c.col ? (sort.dir === "asc" ? "ascending" : "descending") : undefined}
                aria-label={c.label !== c.name ? c.name : undefined}
                title={sort?.col === c.col && sort.dir === "asc" ? `${c.name} · Click to show saved order` : c.name}
                className={`col-head sortable${sort?.col === c.col ? " sort-active" : ""}`}>
                {c.label} {sortIndicator(sort, c.col)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, index) => {
            const selected = r.symbol === selectedSymbol;
            const insertion = dropTarget?.symbol === r.symbol ? dropTarget.position : null;
            return (
              <tr key={r.symbol}
                draggable={!sort}
                tabIndex={sort ? undefined : 0}
                title={sort ? undefined : "Drag to reorder or use Alt+Up and Alt+Down"}
                aria-keyshortcuts={sort ? undefined : "Alt+ArrowUp Alt+ArrowDown"}
                onKeyDown={(event) => {
                  if (sort || !event.altKey || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
                  event.preventDefault();
                  const up = event.key === "ArrowUp";
                  move(r.symbol, snap.symbols[index + (up ? -1 : 1)], up ? "before" : "after");
                }}
                onDragStart={(event) => {
                  draggedSymbol.current = r.symbol;
                  event.dataTransfer.effectAllowed = "move";
                  event.dataTransfer.setData("application/x-etape-watchlist-symbol", r.symbol);
                }}
                onDragEnd={endDrag}
                onDragOver={(event) => {
                  if (!sort && draggedSymbol.current && draggedSymbol.current !== r.symbol) {
                    event.preventDefault();
                    event.dataTransfer.dropEffect = "move";
                    const position = dropPosition(event);
                    setDropTarget((current) => current?.symbol === r.symbol && current.position === position
                      ? current : { symbol: r.symbol, position });
                  }
                }}
                onDragLeave={(event) => {
                  if (event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget)) return;
                  setDropTarget((current) => current?.symbol === r.symbol ? null : current);
                }}
                onDrop={(event) => {
                  const source = draggedSymbol.current;
                  if (sort || !source) return;
                  event.preventDefault();
                  endDrag();
                  move(source, r.symbol, dropPosition(event));
                }}
                onClick={() => setSelectedSymbol(r.symbol)}
                onDoubleClick={() => { if (group) linkGroups.focus(group, r.symbol); }}
                onContextMenu={(e) => { e.preventDefault(); setMenu({ clientX: e.clientX, clientY: e.clientY, symbol: r.symbol }); }}
                onMouseEnter={() => setHoveredSymbol(r.symbol)}
                onMouseLeave={() => setHoveredSymbol((h) => (h === r.symbol ? null : h))}
                style={{ cursor: sort ? "pointer" : "default", textAlign: "right", userSelect: "none",
                  background: selected ? "rgba(154,106,27,.16)" : hoveredSymbol === r.symbol ? "rgba(154,106,27,.06)" : "transparent",
                  boxShadow: insertion ? `inset 0 ${insertion === "before" ? 2 : -2}px 0 ${palette.accent}`
                    : selected ? `inset 0 0 0 1px ${palette.accent}` : "none",
                  transition: "background 120ms ease" }}>
                {visible.map(({ col }) => {
                  if (col === "sym") return <td key={col} style={symCell}>{bareSymbol(r.symbol)}</td>;
                  const style = { ...numCell, opacity: stale ? 0.55 : 1 };
                  if (col === "last") return <td key={col} style={style}>{r.last === null ? "—" : formatPrice(r.last, quoteDecimals(r.last, 2))}</td>;
                  if (col === "changePct" || col === "sessionChangePct") {
                    const pct = r[col];
                    const color = pct === null ? palette.textMuted : pct > 0 ? palette.up : pct < 0 ? palette.down : palette.text;
                    return <td key={col} style={{ ...style, color }}>{formatChangePct(pct)}</td>;
                  }
                  return <td key={col} style={style}>{formatCompactShares(col === "vol" ? r.volume : r.sessionVolume)}</td>;
                })}
              </tr>
            );
          })}
        </tbody>
      </table>
      {menu && (
        <TVContextMenu chrome={menuChrome(palette)} x={menu.clientX} y={menu.clientY}
          items={buildRowMenuItems(menu.symbol)} onClose={() => setMenu(null)} />
      )}
    </div>
  );
}
