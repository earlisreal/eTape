import { useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ScannerFilters, ScannerSession } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { FONTS } from "../../render/palette";
import { formatPrice, formatTapeTime, quoteDecimals } from "../../render/format";
import { appendSsrMarker, formatChangePct, formatCompactShares, formatDollarTurnover, formatRelativeVolume, formatShortInterest, msUntilEtMidnight } from "../format";
import { formatFilterSummary } from "./scannerFilter";
import { toggleSort, sortIndicator, type SortState } from "../sortColumns";
import { bareSymbol } from "../exec/orderStatus";
import { Button } from "../controls/Button";
import { useDismissibleLayer } from "../controls/useDismissibleLayer";
import { TVContextMenu, type MenuEntry } from "./tv/TVContextMenu";
import { menuChrome } from "../menuChrome";
import { PanelHeaderSlotContext } from "./headerSlot";
import { ScannerHeader, ScannerSyncControl } from "./ScannerControls";
import { rankScannerRows, readScannerSort, scannerModeSort } from "../scannerSync";
import { focusMainWorkspace } from "../windows";
import { soundEngine } from "../../sound/SoundEngine";
import { DEFAULT_SCANNER_FILTERS, scannerFiltersFromSettings } from "../scannerFilters";

const SESSION_LABEL: Record<ScannerSession, string> = {
  premarket: "Pre-market", rth: "RTH", afterhours: "After-hours", overnight: "Overnight",
};
type ScannerColumn = "sym" | "changePct" | "last" | "float" | "relVol" | "vol" | "sessionVol" | "turnover" | "shortInterest";
type MetricColumn = Exclude<ScannerColumn, "sym">;
type ColumnSettings = { order: MetricColumn[]; hidden: MetricColumn[] };
const METRIC_COLUMNS: { col: MetricColumn; label: string }[] = [
  { col: "changePct", label: "%" },
  { col: "last", label: "Last" },
  { col: "float", label: "Float" },
  { col: "relVol", label: "REL VOL" },
  { col: "vol", label: "Vol" },
  { col: "sessionVol", label: "Session Vol" },
  { col: "turnover", label: "Turnover" },
  { col: "shortInterest", label: "Short Int" },
];
const DEFAULT_COLUMN_ORDER: MetricColumn[] = METRIC_COLUMNS.map(({ col }) => col);
const SYMBOL_COLUMN = { col: "sym" as const, label: "Symbol" };

const isMetricColumn = (value: unknown): value is MetricColumn =>
  typeof value === "string" && DEFAULT_COLUMN_ORDER.includes(value as MetricColumn);

function readColumnSettings(settings: Record<string, unknown>): ColumnSettings {
  const raw = settings.scannerColumns as { order?: unknown; hidden?: unknown } | undefined;
  const order: MetricColumn[] = [];
  const seen = new Set<MetricColumn>();
  if (Array.isArray(raw?.order)) {
    for (const value of raw.order) {
      if (isMetricColumn(value) && !seen.has(value)) { seen.add(value); order.push(value); }
    }
  }
  for (const value of DEFAULT_COLUMN_ORDER) if (!seen.has(value)) order.push(value);
  const hidden = Array.isArray(raw?.hidden)
    ? raw.hidden.filter(isMetricColumn).filter((value, index, values) => values.indexOf(value) === index)
    : [];
  return { order, hidden };
}

const columnSettingsValue = (settings: ColumnSettings) => ({ order: settings.order, hidden: settings.hidden });

const visibleColumns = (settings: ColumnSettings) => [
  SYMBOL_COLUMN,
  ...settings.order
    .filter((col) => !settings.hidden.includes(col))
    .map((col) => METRIC_COLUMNS.find((item) => item.col === col)!)
];

const defaultSortForVisibleColumns = (mode: ScannerFilters["mode"], settings: ColumnSettings): SortState => {
  const next = scannerModeSort(mode);
  if (!next) return { col: "sym", dir: "asc" };
  return settings.hidden.includes(next.col as MetricColumn) ? { col: "sym", dir: "asc" } : next;
};

const unitScale = (unit: "K" | "M") => unit === "K" ? 1_000 : 1_000_000;

export function ScannerPanel(
  { config, scannerId = "legacy", stores, linkGroups, commands, onConfigChange, group: groupProp, scannerSync }: PanelProps,
): JSX.Element {
  const { palette } = useTheme();
  const headerSlot = useContext(PanelHeaderSlotContext);
  // config.group is frozen at panel creation (dockview never re-invokes the panel
  // factory with a fresh config after a later swatch re-pick) — PanelFrame threads
  // the live re-picked group through as the `group` prop instead. Same pattern as
  // ChartPanel/LadderPanel/TapePanel/etc.
  const group = groupProp ?? config.group;
  const [menu, setMenu] = useState<{ clientX: number; clientY: number; symbol: string } | null>(null);
  const snap = useSyncExternalStore((cb) => stores.scanner.subscribe(cb), () => stores.scanner.getSnapshot());
  const cv = useMemo(() => stores.scanner.currentView(scannerId), [snap, stores.scanner, scannerId]);
  const [sort, setSort] = useState<SortState>(() => readScannerSort(config.settings));
  const [columnSettings, setColumnSettings] = useState<ColumnSettings>(() => readColumnSettings(config.settings));
  const sortedMode = useRef<ScannerFilters["mode"] | null>(null);
  const [appliedFilters, setAppliedFilters] = useState<ScannerFilters | null>(null);
  const engineFiltersRef = useRef(cv.filters);
  useEffect(() => {
    const previous = engineFiltersRef.current;
    if (previous === cv.filters) return;
    engineFiltersRef.current = cv.filters;
    if (appliedFilters && (JSON.stringify(cv.filters) === JSON.stringify(appliedFilters)
      || JSON.stringify(previous) !== JSON.stringify(cv.filters))) setAppliedFilters(null);
  }, [appliedFilters, cv.filters]);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [columnsOpen, setColumnsOpen] = useState(false);
  const [draft, setDraft] = useState<ScannerFilters>(() => scannerFiltersFromSettings(config.settings));
  const filtersRef = useRef<HTMLDivElement>(null);
  const filterTriggerRef = useRef<HTMLButtonElement>(null);
  const columnsRef = useRef<HTMLDivElement>(null);
  const columnsTriggerRef = useRef<HTMLButtonElement>(null);
  // Single click only highlights a row; double-click is the "load it" gesture — a
  // stray single click while scanning the list should never reassign the linked
  // group's live symbol.
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);
  const [hoveredSymbol, setHoveredSymbol] = useState<string | null>(null);
  const [scannerSoundMuted, setScannerSoundMuted] = useState(() => config.settings.scannerSoundMuted === true);

  useEffect(() => {
    if (scannerSoundMuted) return;
    return stores.scanner.onNewHit(() => soundEngine.scannerHit(), scannerId);
  }, [stores.scanner, scannerSoundMuted, scannerId]);

  const toggleScannerSound = () => {
    const next = !scannerSoundMuted;
    setScannerSoundMuted(next);
    onConfigChange({ scannerSoundMuted: next });
  };

  // ET-midnight dedup reset: clear the per-session seen-sets so the next session's
  // first prints flash fresh. Re-arms after each fire.
  useEffect(() => {
      let timer: ReturnType<typeof setTimeout>;
    const arm = () => { timer = setTimeout(() => { stores.scanner.resetSeen(undefined, scannerId); arm(); }, msUntilEtMidnight(new Date())); };
    arm();
    return () => clearTimeout(timer);
  }, [stores.scanner, scannerId]);

  const filters = { ...scannerFiltersFromSettings(config.settings), ...(cv.filters ?? {}), ...(appliedFilters ?? {}) };
  useEffect(() => {
    const sortHidden = sort !== null && columnSettings.hidden.includes(sort.col as MetricColumn);
    if (sortedMode.current === null) {
      sortedMode.current = filters.mode;
      if (sortHidden) {
        const next = defaultSortForVisibleColumns(filters.mode, columnSettings);
        setSort(next);
        onConfigChange({ sort: next });
      }
      return;
    }
    if (sortedMode.current === filters.mode) return;
    sortedMode.current = filters.mode;
    const next = defaultSortForVisibleColumns(filters.mode, columnSettings);
    setSort(next);
    onConfigChange({ sort: next });
  }, [filters.mode, columnSettings, sort, onConfigChange]);
  const rows = useMemo(() => rankScannerRows(cv.rows, sort), [cv.rows, sort]);
  const shownColumns = useMemo(() => visibleColumns(columnSettings), [columnSettings]);

  const openFilters = () => { setColumnsOpen(false); setDraft(filters); setFiltersOpen(true); };
  const openColumns = () => { setFiltersOpen(false); setColumnsOpen(true); };
  useDismissibleLayer(filtersRef, () => setFiltersOpen(false), { enabled: filtersOpen, anchor: filterTriggerRef.current });
  useDismissibleLayer(columnsRef, () => setColumnsOpen(false), { enabled: columnsOpen, anchor: columnsTriggerRef.current });
  const invalidPriceRange = !Number.isFinite(draft.minPrice) || !Number.isFinite(draft.maxPrice)
    || draft.minPrice < 0 || draft.maxPrice < 0
    || draft.minPrice > 0 && draft.maxPrice > 0 && draft.minPrice > draft.maxPrice;
  const applyFilters = () => {
    if (invalidPriceRange) return;
    const [workspaceId, panelId, ...extra] = scannerId.split("/");
    const identity = panelId && extra.length === 0 ? { workspaceId, panelId } : {};
    void commands.sendCommand("SetScannerFilters", { ...identity, filters: draft });
    // Persist locally as soon as Apply is pressed. During engine startup the
    // scanner service may not be installed yet; AppShell retries workspace
    // registration and sends this latest saved filter set when it becomes ready.
    setAppliedFilters(draft);
    onConfigChange({ scannerFilters: draft });
    if (draft.mode !== filters.mode) {
      const next = defaultSortForVisibleColumns(draft.mode, columnSettings);
      setSort(next);
      onConfigChange({ sort: next });
    }
    setFiltersOpen(false);
  };
  const resetDefaults = () => setDraft(DEFAULT_SCANNER_FILTERS);
  const clickSort = (col: string) => {
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ sort: next });
  };
  const updateColumnSettings = (next: ColumnSettings) => {
    setColumnSettings(next);
    onConfigChange({ scannerColumns: columnSettingsValue(next) });
    if (sort && next.hidden.includes(sort.col as MetricColumn)) {
      const fallback = defaultSortForVisibleColumns(filters.mode, next);
      setSort(fallback);
      onConfigChange({ sort: fallback });
    }
  };
  const toggleColumn = (column: MetricColumn) => {
    const hidden = columnSettings.hidden.includes(column)
      ? columnSettings.hidden.filter((value) => value !== column)
      : [...columnSettings.hidden, column];
    updateColumnSettings({ ...columnSettings, hidden });
  };
  const moveColumn = (column: MetricColumn, direction: -1 | 1) => {
    const index = columnSettings.order.indexOf(column);
    const target = index + direction;
    if (index < 0 || target < 0 || target >= columnSettings.order.length) return;
    const order = [...columnSettings.order];
    [order[index], order[target]] = [order[target], order[index]];
    updateColumnSettings({ ...columnSettings, order });
  };
  const resetColumns = () => updateColumnSettings({ order: [...DEFAULT_COLUMN_ORDER], hidden: [] });
  // Single unconditional entry — unlike ChartPanel's toggle, this menu doesn't
  // need membership state; adding an already-watchlisted symbol is a no-op on
  // the engine side (WatchlistAdd is idempotent), so no add/remove branching.
  const buildRowMenuItems = (sym: string): MenuEntry[] =>
    [{ label: `Add ${bareSymbol(sym)} to watchlist`, onClick: () => void commands.sendCommand("WatchlistAdd", { symbol: sym }) }];

  const sessionLabel = cv.session ? SESSION_LABEL[cv.session] : null;
  return (
    <div className="scanner-panel">
      <ScannerHeader title="Scanner" context={sessionLabel} muted={scannerSoundMuted} onMute={toggleScannerSound}
        filtersOpen={filtersOpen} onFilters={() => (filtersOpen ? setFiltersOpen(false) : openFilters())} filterTriggerRef={filterTriggerRef}>
        <button type="button" aria-label="columns" aria-expanded={columnsOpen} title="Columns"
          ref={columnsTriggerRef}
          onClick={() => (columnsOpen ? setColumnsOpen(false) : openColumns())}
          style={{ border: `1px solid ${palette.border}`, borderRadius: 3, background: palette.surface, color: palette.textMuted, cursor: "pointer", padding: "2px 5px", fontFamily: FONTS.mono, fontSize: 10, lineHeight: 1, flex: "0 0 auto" }}>
          columns
        </button>
      </ScannerHeader>
      {!cv.refreshedAt && cv.rows.length === 0 && <div style={{ padding: "6px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>Waiting for scanner data…</div>}
      {!cv.refreshedAt && cv.rows.length > 0 && <div role="status" style={{ padding: "6px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>Quote snapshots pending for visible symbols…</div>}
      {filtersOpen && (
        <div ref={filtersRef} className="popover" style={{ top: headerSlot === undefined ? 30 : 6, left: headerSlot === undefined ? 8 : undefined, right: headerSlot === undefined ? undefined : 8, width: 220 }}>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            <label>rank <select aria-label="rank mode" value={draft.mode} onChange={(e) => setDraft({ ...draft, mode: e.target.value as ScannerFilters["mode"] })}><option value="gainers">Top gainers</option><option value="losers">Top losers</option><option value="most_active">Most active</option><option value="session_volume">Session Volume</option></select></label>
            {draft.mode !== "most_active" && draft.mode !== "session_volume" && <label>{draft.mode === "gainers" ? "min gain %" : "min loss %"} <input aria-label={draft.mode === "gainers" ? "min gain %" : "min loss %"} type="number" min="0" value={draft.minChangePct} onChange={(e) => setDraft({ ...draft, minChangePct: Math.max(0, Number(e.target.value)) })} style={{ width: 60 }} /></label>}
            <label>float ≤ <input aria-label="float cap" type="number" min="0" value={draft.maxFloatShares === null ? "" : draft.maxFloatShares / unitScale(draft.floatUnit)} onChange={(e) => setDraft({ ...draft, maxFloatShares: e.target.value === "" ? null : Number(e.target.value) * unitScale(draft.floatUnit) })} style={{ width: 70 }} /><select aria-label="float unit" value={draft.floatUnit} onChange={(e) => setDraft({ ...draft, floatUnit: e.target.value as "K" | "M" })}><option>K</option><option>M</option></select></label>
            <label>vol ≥ <input aria-label="min volume" type="number" min="0" value={draft.minVolume / unitScale(draft.volumeUnit)} onChange={(e) => setDraft({ ...draft, minVolume: Number(e.target.value) * unitScale(draft.volumeUnit) })} style={{ width: 70 }} /><select aria-label="volume unit" value={draft.volumeUnit} onChange={(e) => setDraft({ ...draft, volumeUnit: e.target.value as "K" | "M" })}><option>K</option><option>M</option></select></label>
            <label>session vol ≥ <input aria-label="min session volume" type="number" min="0" value={draft.minSessionVolume / unitScale(draft.sessionVolumeUnit)} onChange={(e) => setDraft({ ...draft, minSessionVolume: Number(e.target.value) * unitScale(draft.sessionVolumeUnit) })} style={{ width: 70 }} /><select aria-label="session volume unit" value={draft.sessionVolumeUnit} onChange={(e) => setDraft({ ...draft, sessionVolumeUnit: e.target.value as "K" | "M" })}><option>K</option><option>M</option></select></label>
            <label>turnover ≥ <input aria-label="min turnover" type="number" min="0" step="0.01" value={draft.minTurnover / 1_000_000} onChange={(e) => setDraft({ ...draft, minTurnover: Math.max(0, Number(e.target.value) * 1_000_000) })} style={{ width: 70 }} />M</label>
            <label>rel vol ≥ <input aria-label="rel vol ≥" type="number" min="0" step="0.01" value={draft.minRelativeVolume} onChange={(e) => setDraft({ ...draft, minRelativeVolume: Math.max(0, Number(e.target.value)) })} style={{ width: 70 }} /></label>
            <label>price ≥ <input aria-label="price ≥" type="number" min="0" step="any" value={draft.minPrice === 0 ? "" : draft.minPrice} onChange={(e) => setDraft({ ...draft, minPrice: e.target.value === "" ? 0 : Math.max(0, Number(e.target.value)) })} style={{ width: 70 }} /></label>
            <label>price ≤ <input aria-label="price ≤" type="number" min="0" step="any" value={draft.maxPrice === 0 ? "" : draft.maxPrice} onChange={(e) => setDraft({ ...draft, maxPrice: e.target.value === "" ? 0 : Math.max(0, Number(e.target.value)) })} style={{ width: 70 }} /></label>
            {invalidPriceRange && <div role="alert" style={{ color: palette.down }}>Minimum price must be no greater than maximum price</div>}
            {cv.refreshedAt && <span className="mono" title={cv.rows.length > 0 ? "Oldest visible row's latest successful quote snapshot" : cv.discoveryAt ? `Candidate discovery updated ${formatTapeTime(cv.discoveryAt)}` : "Candidate discovery time unavailable"} style={{ color: palette.textMuted, whiteSpace: "nowrap" }}>{cv.rows.length > 0 ? "oldest quote" : "updated"} {formatTapeTime(cv.refreshedAt)}</span>}
            <div style={{ display: "flex", justifyContent: "space-between", marginTop: 4 }}>
              <Button onClick={resetDefaults}>Reset defaults</Button>
              <Button variant="primary" disabled={invalidPriceRange} onClick={applyFilters}>Apply</Button>
            </div>
          </div>
        </div>
      )}
      {scannerSync && <ScannerSyncControl sync={scannerSync} />}
      {(
        <div data-testid="scanner-filter-summary" className="mono scanner-summary scanner-truncate">
          <span>
            {filters.mode === "most_active" ? `Most active${cv.session === "rth" ? "" : " · approximate"}` : filters.mode === "session_volume" ? "Session Volume · top 200 candidates" : filters.mode === "gainers" ? "Top gainers" : "Top losers"} · {formatFilterSummary({ minChangePct: filters.mode === "most_active" || filters.mode === "session_volume" ? 0 : filters.minChangePct, floatCapShares: filters.maxFloatShares, minVolume: filters.minVolume, minSessionVolume: filters.minSessionVolume, minTurnover: filters.minTurnover, minRelativeVolume: filters.minRelativeVolume, minPrice: filters.minPrice, maxPrice: filters.maxPrice })}
          </span>
        </div>
      )}
      {cv.status === "delayed" && <div role="status" className="mono" style={{ padding: "3px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>Data delayed · shared request budget</div>}
      {cv.status === "paused" && <div role="status" className="mono" style={{ padding: "3px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>Scanner paused · workspace closed</div>}
      {columnsOpen && (
        <div ref={columnsRef} className="popover" style={{ top: headerSlot === undefined ? 30 : 6, right: headerSlot === undefined ? 8 : 8, width: 230 }}>
          <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
            <div className="mono" style={{ color: palette.textMuted }}>Columns</div>
            {columnSettings.order.map((column, index) => {
              const meta = METRIC_COLUMNS.find((item) => item.col === column)!;
              return (
                <div key={column} style={{ display: "flex", alignItems: "center", gap: 5 }}>
                  <label style={{ display: "flex", alignItems: "center", gap: 5, flex: 1 }}>
                    <input type="checkbox" aria-label={`show ${meta.label}`} checked={!columnSettings.hidden.includes(column)} onChange={() => toggleColumn(column)} />
                    <span>{meta.label}</span>
                  </label>
                  <button type="button" aria-label={`move ${meta.label} up`} disabled={index === 0} onClick={() => moveColumn(column, -1)}>
                    ↑
                  </button>
                  <button type="button" aria-label={`move ${meta.label} down`} disabled={index === columnSettings.order.length - 1} onClick={() => moveColumn(column, 1)}>
                    ↓
                  </button>
                </div>
              );
            })}
            <Button onClick={resetColumns}>Reset columns</Button>
          </div>
        </div>
      )}
      <div className="scanner-results">
        <table className="scanner-table">
          <thead>
            <tr style={{ color: palette.textMuted, textAlign: "right" }}>
              {shownColumns.map((c) => (
                <th key={c.col} scope="col" onClick={() => clickSort(c.col)}
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
                onClick={() => { setSelectedSymbol(r.symbol); if (cv.session) stores.scanner.markSeen(cv.session, r.symbol, scannerId); }}
                onDoubleClick={() => { if (cv.session) stores.scanner.markSeen(cv.session, r.symbol, scannerId); linkGroups.focus(group ?? "green", r.symbol); focusMainWorkspace(commands); }}
                onContextMenu={(e) => { e.preventDefault(); if (cv.session) stores.scanner.markSeen(cv.session, r.symbol, scannerId); setMenu({ clientX: e.clientX, clientY: e.clientY, symbol: r.symbol }); }}
                onMouseEnter={() => setHoveredSymbol(r.symbol)}
                onMouseLeave={() => setHoveredSymbol((h) => (h === r.symbol ? null : h))}
                style={{ cursor: "pointer", textAlign: "right", userSelect: "none", fontWeight: r.isUnseen ? 700 : undefined,
                  background: r.isUnseen ? "rgba(154,106,27,.10)" : selected ? "transparent"
                    : hoveredSymbol === r.symbol ? "rgba(154,106,27,.06)" : "transparent",
                  boxShadow: selected ? `inset 0 0 0 1px ${palette.accent}` : r.isUnseen ? `inset 2px 0 0 ${palette.accent}` : "none",
                  transition: "background 120ms ease" }}>
                {shownColumns.map((column) => {
                  switch (column.col) {
                    case "sym": return <td key={column.col} className="mono scanner-symbol" title={r.shortSellRestricted ? "Short Sell Restricted — derived Rule 201 estimate" : undefined}>{appendSsrMarker(bareSymbol(r.symbol), r.shortSellRestricted)}</td>;
                    case "changePct": return <td key={column.col} className="mono scanner-change" data-direction={r.changePct === null ? "missing" : r.changePct > 0 ? "up" : r.changePct < 0 ? "down" : undefined}>{formatChangePct(r.changePct)}</td>;
                    case "last": return <td key={column.col} className="mono">{r.last === null ? "—" : formatPrice(r.last, quoteDecimals(r.last, 2))}</td>;
                    case "float": return <td key={column.col} className="mono">{formatCompactShares(r.floatShares)}</td>;
                    case "relVol": return <td key={column.col} className="mono" title="Latest daily volume divided by the mean of up to 50 preceding daily bars.">{formatRelativeVolume(r.relativeVolume)}</td>;
                    case "vol": return <td key={column.col} className="mono" title="Latest reported base daily volume.">{formatCompactShares(r.volume)}</td>;
                    case "sessionVol": return <td key={column.col} className="mono" title="Provider-reported volume for the current Scanner session.">{formatCompactShares(r.sessionVolume)}</td>;
                    case "turnover": return <td key={column.col} className="mono" title="Latest reported base daily dollar turnover.">{formatDollarTurnover(r.turnover)}</td>;
                    case "shortInterest": return <td key={column.col} className="mono" title={r.shortInterestAsOf ? `as of ${r.shortInterestAsOf}` : undefined}>{formatShortInterest(r.shortInterest)}</td>;
                  }
                })}
              </tr>
              );
            })}
            {rows.length === 0 && cv.refreshedAt && (
              <tr className="scanner-empty"><td colSpan={shownColumns.length}>No symbols match current filters.</td></tr>
            )}
          </tbody>
        </table>
      </div>
      {menu && (
        <TVContextMenu chrome={menuChrome(palette)} x={menu.clientX} y={menu.clientY}
          items={buildRowMenuItems(menu.symbol)} onClose={() => setMenu(null)} />
      )}
    </div>
  );
}
