import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { WatchlistRow } from "../../gen/wsmsg";
import { useTheme } from "../ThemeProvider";
import { FONTS } from "../../render/palette";
import { formatChangePct, formatCompactShares } from "../format";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import { bareSymbol } from "../exec/orderStatus";
import { TVContextMenu, type MenuEntry } from "./tv/TVContextMenu";
import { menuChrome } from "../menuChrome";
import { useToasts } from "../Toast";

// A row view over the watchlist snapshot: symbols is authoritative membership +
// order, but rows may lag by up to one poll — a symbol absent from rows still
// renders (with dash placeholders), it never vanishes from the list.
interface WatchlistRowView {
  symbol: string;
  last: number | null;
  changePct: number | null;
  volume: number | null;
}

const STALE_MS = 10_000;
const STALE_CHECK_MS = 2_000;

const COLUMNS: { col: string; label: string; align: "left" | "right" }[] = [
  { col: "sym", label: "Symbol", align: "left" },
  { col: "last", label: "Last", align: "right" },
  { col: "changePct", label: "%Chg", align: "right" },
  { col: "vol", label: "Volume", align: "right" },
];
const SORT_ACCESSORS: Record<string, (r: WatchlistRowView) => number | string | null> = {
  sym: (r) => r.symbol,
  last: (r) => r.last,
  changePct: (r) => r.changePct,
  vol: (r) => r.volume,
};

function toRowView(symbol: string, row: WatchlistRow | undefined): WatchlistRowView {
  if (!row) return { symbol, last: null, changePct: null, volume: null };
  return { symbol, last: row.last, changePct: row.changePct, volume: row.volume };
}

export function WatchlistPanel({ config, stores, linkGroups, commands }: PanelProps): JSX.Element {
  const { palette } = useTheme();
  const toast = useToasts();
  const snap = useSyncExternalStore((cb) => stores.watchlist.subscribe(cb), () => stores.watchlist.getSnapshot());
  const [sort, setSort] = useState<SortState>(null);
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

  const rows = useMemo(() => {
    const views = snap.symbols.map((sym) => toRowView(sym, snap.rows.get(sym)));
    return sortRows(views, sort, SORT_ACCESSORS);
  }, [snap.symbols, snap.rows, sort]);

  const clickSort = (col: string) => setSort((s) => toggleSort(s, col));

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

  if (snap.symbols.length === 0) {
    return (
      <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12,
        display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 10, padding: 16 }}>
        <span style={{ color: palette.textMuted }}>Add a symbol to start your watchlist</span>
        {addInput}
      </div>
    );
  }

  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}` }}>
        {addInput}
      </div>
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ color: palette.textMuted, textAlign: "right" }}>
            {COLUMNS.map((c) => (
              <th key={c.col} style={{ ...th, textAlign: c.align, cursor: "pointer" }} onClick={() => clickSort(c.col)}
                className={`col-head sortable${sort?.col === c.col ? " sort-active" : ""}`}>
                {c.label} {sortIndicator(sort, c.col)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const selected = r.symbol === selectedSymbol;
            return (
              <tr key={r.symbol}
                onClick={() => setSelectedSymbol(r.symbol)}
                onDoubleClick={() => linkGroups.focus(config.group ?? "green", r.symbol)}
                onContextMenu={(e) => { e.preventDefault(); setMenu({ clientX: e.clientX, clientY: e.clientY, symbol: r.symbol }); }}
                onMouseEnter={() => setHoveredSymbol(r.symbol)}
                onMouseLeave={() => setHoveredSymbol((h) => (h === r.symbol ? null : h))}
                style={{ cursor: "pointer", textAlign: "right", userSelect: "none",
                  background: selected ? "rgba(154,106,27,.16)" : hoveredSymbol === r.symbol ? "rgba(154,106,27,.06)" : "transparent",
                  boxShadow: selected ? `inset 0 0 0 1px ${palette.accent}` : "none",
                  transition: "background 120ms ease" }}>
                <td style={symCell}>{bareSymbol(r.symbol)}</td>
                <td style={{ ...numCell, opacity: stale ? 0.55 : 1 }}>{r.last === null ? "—" : r.last.toFixed(2)}</td>
                <td style={{ ...numCell, opacity: stale ? 0.55 : 1,
                  color: r.changePct === null ? palette.textMuted : r.changePct > 0 ? palette.up : r.changePct < 0 ? palette.down : palette.text }}>
                  {formatChangePct(r.changePct)}
                </td>
                <td style={{ ...numCell, opacity: stale ? 0.55 : 1 }}>{formatCompactShares(r.volume)}</td>
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
