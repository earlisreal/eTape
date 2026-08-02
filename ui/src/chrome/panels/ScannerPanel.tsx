import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ScannerFilters, ScannerSession } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { FONTS } from "../../render/palette";
import { formatTapeTime } from "../../render/format";
import { formatChangePct, formatCompactShares, msUntilEtMidnight } from "../format";
import { formatFilterSummary } from "./scannerFilter";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import type { ScannerRowView } from "../../data/ScannerStore";
import { bareSymbol } from "../exec/orderStatus";
import { Button } from "../controls/Button";
import { TVContextMenu, type MenuEntry } from "./tv/TVContextMenu";
import { menuChrome } from "../menuChrome";

const SESSION_LABEL: Record<ScannerSession, string> = {
  premarket: "Pre-market", rth: "RTH", afterhours: "After-hours", overnight: "Overnight",
};
const DEFAULT_SORT: SortState = { col: "changePct", dir: "desc" };
const DEFAULT_FILTERS: ScannerFilters = { mode: "gainers", minChangePct: 0, maxFloatShares: null, minVolume: 0, floatUnit: "M", volumeUnit: "K" };
const COLUMNS: { col: string; label: string; align: "left" | "right" }[] = [
  { col: "sym", label: "Symbol", align: "left" },
  { col: "changePct", label: "%", align: "right" },
  { col: "last", label: "Last", align: "right" },
  { col: "float", label: "Float", align: "right" },
  { col: "vol", label: "Vol", align: "right" },
];
const SORT_ACCESSORS: Record<string, (r: ScannerRowView) => number | string | null> = {
  sym: (r) => r.symbol,
  changePct: (r) => r.changePct,
  last: (r) => r.last,
  float: (r) => r.floatShares,
  vol: (r) => r.volume,
};

const unitScale = (unit: "K" | "M") => unit === "K" ? 1_000 : 1_000_000;

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.sort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

export function ScannerPanel(
  { config, stores, linkGroups, commands, onConfigChange, group: groupProp }: PanelProps & { variant?: "scanner" | "movers" },
): JSX.Element {
  const { palette } = useTheme();
  // config.group is frozen at panel creation (dockview never re-invokes the panel
  // factory with a fresh config after a later swatch re-pick) — PanelFrame threads
  // the live re-picked group through as the `group` prop instead. Same pattern as
  // ChartPanel/LadderPanel/TapePanel/etc.
  const group = groupProp ?? config.group;
  const [menu, setMenu] = useState<{ clientX: number; clientY: number; symbol: string } | null>(null);
  const snap = useSyncExternalStore((cb) => stores.scanner.subscribe(cb), () => stores.scanner.getSnapshot());
  const cv = useMemo(() => stores.scanner.currentView(), [snap, stores.scanner]);
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [draft, setDraft] = useState<ScannerFilters>(DEFAULT_FILTERS);
  const [engineFilters, setEngineFilters] = useState<ScannerFilters | null>(null);
  // Single click only highlights a row; double-click is the "load it" gesture — a
  // stray single click while scanning the list should never reassign the linked
  // group's live symbol.
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);
  const [hoveredSymbol, setHoveredSymbol] = useState<string | null>(null);

  // ET-midnight dedup reset: clear the per-session seen-sets so the next session's
  // first prints flash fresh. Re-arms after each fire.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const arm = () => { timer = setTimeout(() => { stores.scanner.resetSeen(); arm(); }, msUntilEtMidnight(new Date())); };
    arm();
    return () => clearTimeout(timer);
  }, [stores.scanner]);

  useEffect(() => {
    void commands.sendCommand("GetScannerFilters", {}).then((ack) => {
      if (ack.status === "accepted" && ack.value) setEngineFilters(ack.value as ScannerFilters);
    });
  }, [commands]);

  const filters = cv.filters ?? engineFilters ?? DEFAULT_FILTERS;
  const rows = useMemo(() => sortRows(cv.rows, sort, SORT_ACCESSORS), [cv.rows, sort]);

  const openFilters = () => { setDraft(filters); setFiltersOpen(true); };
  const applyFilters = () => {
    void commands.sendCommand("SetScannerFilters", { filters: draft });
    setFiltersOpen(false);
  };
  const resetDefaults = () => setDraft(DEFAULT_FILTERS);
  const clickSort = (col: string) => {
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ sort: next });
  };
  // Single unconditional entry — unlike ChartPanel's toggle, this menu doesn't
  // need membership state; adding an already-watchlisted symbol is a no-op on
  // the engine side (WatchlistAdd is idempotent), so no add/remove branching.
  const buildRowMenuItems = (sym: string): MenuEntry[] =>
    [{ label: `Add ${bareSymbol(sym)} to watchlist`, onClick: () => void commands.sendCommand("WatchlistAdd", { symbol: sym }) }];

  const header = cv.refreshedAt
    ? `${SESSION_LABEL[cv.session!]} · updated ${formatTapeTime(cv.refreshedAt)}`
    : "Waiting for scanner data…";

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  // Data-surface treatment (matches tape/ladder): mono, tabular figures, ticker as the row anchor.
  const symCell = { textAlign: "left" as const, padding: "2px 8px", fontFamily: FONTS.mono, fontWeight: 600 };
  const numCell = { padding: "2px 8px", fontFamily: FONTS.mono, fontWeight: 500, fontVariantNumeric: "tabular-nums" as const };
  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}`, position: "relative" }}>
        <span style={{ fontWeight: 600 }}>{header}</span>
        {(
          <Button aria-label="filters" aria-expanded={filtersOpen}
            onClick={() => (filtersOpen ? setFiltersOpen(false) : openFilters())} style={{ padding: "2px 8px" }}>
            ⚙ filters
          </Button>
        )}
        {filtersOpen && (
          <div className="popover" style={{ top: 30, left: 8, width: 220 }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <label>rank <select aria-label="rank mode" value={draft.mode} onChange={(e) => setDraft({ ...draft, mode: e.target.value as ScannerFilters["mode"] })}><option value="gainers">Top gainers</option><option value="losers">Top losers</option></select></label>
			  <label>{draft.mode === "gainers" ? "min gain %" : "min loss %"} <input aria-label={draft.mode === "gainers" ? "min gain %" : "min loss %"} type="number" min="0" value={draft.minChangePct} onChange={(e) => setDraft({ ...draft, minChangePct: Math.max(0, Number(e.target.value)) })} style={{ width: 60 }} /></label>
              <label>float ≤ <input aria-label="float cap" type="number" min="0" value={draft.maxFloatShares === null ? "" : draft.maxFloatShares / unitScale(draft.floatUnit)} onChange={(e) => setDraft({ ...draft, maxFloatShares: e.target.value === "" ? null : Number(e.target.value) * unitScale(draft.floatUnit) })} style={{ width: 70 }} /><select aria-label="float unit" value={draft.floatUnit} onChange={(e) => setDraft({ ...draft, floatUnit: e.target.value as "K" | "M" })}><option>K</option><option>M</option></select></label>
              <label>vol ≥ <input aria-label="min volume" type="number" min="0" value={draft.minVolume / unitScale(draft.volumeUnit)} onChange={(e) => setDraft({ ...draft, minVolume: Number(e.target.value) * unitScale(draft.volumeUnit) })} style={{ width: 70 }} /><select aria-label="volume unit" value={draft.volumeUnit} onChange={(e) => setDraft({ ...draft, volumeUnit: e.target.value as "K" | "M" })}><option>K</option><option>M</option></select></label>
              <div style={{ display: "flex", justifyContent: "space-between", marginTop: 4 }}>
                <Button onClick={resetDefaults}>Reset defaults</Button>
                <Button variant="primary" onClick={applyFilters}>Apply</Button>
              </div>
            </div>
          </div>
        )}
      </div>
      {(
        <div className="mono" style={{ padding: "3px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
          {filters.mode === "gainers" ? "Top gainers" : "Top losers"} · {formatFilterSummary({ minChangePct: filters.minChangePct, floatCapShares: filters.maxFloatShares, minVolume: filters.minVolume })}
        </div>
      )}
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
              onClick={() => { setSelectedSymbol(r.symbol); if (cv.session) stores.scanner.markSeen(cv.session, r.symbol); }}
              onDoubleClick={() => { if (cv.session) stores.scanner.markSeen(cv.session, r.symbol); linkGroups.focus(group ?? "green", r.symbol); }}
              onContextMenu={(e) => { e.preventDefault(); if (cv.session) stores.scanner.markSeen(cv.session, r.symbol); setMenu({ clientX: e.clientX, clientY: e.clientY, symbol: r.symbol }); }}
              onMouseEnter={() => setHoveredSymbol(r.symbol)}
              onMouseLeave={() => setHoveredSymbol((h) => (h === r.symbol ? null : h))}
              style={{ cursor: "pointer", textAlign: "right", userSelect: "none", fontWeight: r.isUnseen ? 700 : undefined,
                background: selected ? "rgba(154,106,27,.16)" : r.isUnseen ? "rgba(154,106,27,.10)"
                  : hoveredSymbol === r.symbol ? "rgba(154,106,27,.06)" : "transparent",
                boxShadow: selected ? `inset 0 0 0 1px ${palette.accent}` : r.isUnseen ? `inset 2px 0 0 ${palette.accent}` : "none",
                transition: "background 120ms ease" }}>
              <td style={symCell}>{bareSymbol(r.symbol)}</td>
              <td style={{ ...numCell, color: r.changePct === null ? palette.textMuted : r.changePct > 0 ? palette.up : r.changePct < 0 ? palette.down : palette.text }}>{formatChangePct(r.changePct)}</td>
              <td style={numCell}>{r.last === null ? "—" : r.last.toFixed(2)}</td>
              <td style={numCell}>{formatCompactShares(r.floatShares)}</td>
              <td style={numCell}>{formatCompactShares(r.volume)}</td>
            </tr>
            );
          })}
          {rows.length === 0 && cv.refreshedAt && (
            <tr><td colSpan={5} style={{ padding: 12, color: palette.textMuted, textAlign: "center" }}>No symbols match current filters.</td></tr>
          )}
        </tbody>
      </table>
      {menu && (
        <TVContextMenu chrome={menuChrome(palette)} x={menu.clientX} y={menu.clientY}
          items={buildRowMenuItems(menu.symbol)} onClose={() => setMenu(null)} />
      )}
    </div>
  );
}
