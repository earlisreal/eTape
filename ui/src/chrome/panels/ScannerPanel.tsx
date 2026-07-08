import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { PanelProps } from "./registry";
import type { ScannerSession } from "../../wire/contract";
import { useTheme } from "../ThemeProvider";
import { formatTapeTime } from "../../render/format";
import { formatChangePct, formatCompactShares, msUntilEtMidnight } from "../format";
import { applyScannerFilters, formatFilterSummary, type ScannerThresholds } from "./scannerFilter";
import { toggleSort, sortRows, sortIndicator, type SortState } from "../sortColumns";
import type { ScannerRowView } from "../../data/ScannerStore";

const SESSION_LABEL: Record<ScannerSession, string> = {
  premarket: "Pre-market", rth: "RTH movers", afterhours: "After-hours", overnight: "Overnight",
};
const DEFAULT_SORT: SortState = { col: "changePct", dir: "desc" };
const DEFAULT_THRESHOLDS: ScannerThresholds = { minChangePct: 0, floatCapShares: null, minVolume: 0 };
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

function readThresholds(s: Record<string, unknown>): ScannerThresholds {
  const t = (s.thresholds ?? {}) as Partial<ScannerThresholds>;
  return {
    minChangePct: typeof t.minChangePct === "number" ? t.minChangePct : 0,
    floatCapShares: typeof t.floatCapShares === "number" ? t.floatCapShares : null,
    minVolume: typeof t.minVolume === "number" ? t.minVolume : 0,
  };
}

function readSort(s: Record<string, unknown>): SortState {
  const raw = s.sort as { col?: unknown; dir?: unknown } | undefined;
  if (raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")) {
    return { col: raw.col, dir: raw.dir };
  }
  return DEFAULT_SORT;
}

export function ScannerPanel(
  { config, stores, linkGroups, onConfigChange, variant }: PanelProps & { variant: "scanner" | "movers" },
): JSX.Element {
  const { palette } = useTheme();
  const snap = useSyncExternalStore((cb) => stores.scanner.subscribe(cb), () => stores.scanner.getSnapshot());
  const cv = useMemo(() => stores.scanner.currentView(), [snap, stores.scanner]);
  const [thresholds, setThresholds] = useState<ScannerThresholds>(() => readThresholds(config.settings));
  const [sort, setSort] = useState<SortState>(() => readSort(config.settings));
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [draft, setDraft] = useState<ScannerThresholds>(thresholds);
  // Single click only highlights a row; double-click is the "load it" gesture — a
  // stray single click while scanning the list should never reassign the linked
  // group's live symbol.
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);

  // ET-midnight dedup reset: clear the per-session seen-sets so the next session's
  // first prints flash fresh. Re-arms after each fire.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const arm = () => { timer = setTimeout(() => { stores.scanner.resetSeen(); arm(); }, msUntilEtMidnight(new Date())); };
    arm();
    return () => clearTimeout(timer);
  }, [stores.scanner]);

  const rows = useMemo(
    () => sortRows(applyScannerFilters(cv.rows, variant === "movers" ? DEFAULT_THRESHOLDS : thresholds), sort, SORT_ACCESSORS),
    [cv.rows, thresholds, sort, variant],
  );

  const openFilters = () => { setDraft(thresholds); setFiltersOpen(true); };
  const applyFilters = () => {
    setThresholds(draft);
    onConfigChange({ ...config.settings, thresholds: draft });
    setFiltersOpen(false);
  };
  const resetDefaults = () => setDraft(DEFAULT_THRESHOLDS);
  const clickSort = (col: string) => {
    const next = toggleSort(sort, col);
    setSort(next);
    onConfigChange({ ...config.settings, sort: next });
  };

  const header = cv.refreshedAt
    ? `${SESSION_LABEL[cv.session!]} · updated ${formatTapeTime(cv.refreshedAt)}`
    : "Waiting for scanner data…";

  const th = { padding: "2px 8px", position: "sticky" as const, top: 0, background: palette.surface };
  return (
    <div style={{ height: "100%", overflow: "auto", background: palette.bg, color: palette.text, fontSize: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 8px", borderBottom: `1px solid ${palette.border}`, position: "relative" }}>
        <span style={{ fontWeight: 600 }}>{header}</span>
        {variant === "scanner" && (
          <button type="button" className="btn" aria-label="filters" aria-expanded={filtersOpen}
            onClick={() => (filtersOpen ? setFiltersOpen(false) : openFilters())} style={{ padding: "2px 8px" }}>
            ⚙ filters
          </button>
        )}
        {variant === "scanner" && filtersOpen && (
          <div className="popover" style={{ top: 30, left: 8, width: 220 }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <label>min change % <input aria-label="min change %" type="number" value={draft.minChangePct}
                onChange={(e) => setDraft({ ...draft, minChangePct: Number(e.target.value) || 0 })} style={{ width: 60 }} /></label>
              <label>float ≤ <input aria-label="float cap" type="number" value={draft.floatCapShares ?? ""}
                onChange={(e) => setDraft({ ...draft, floatCapShares: e.target.value === "" ? null : Number(e.target.value) })} style={{ width: 100 }} /></label>
              <label>vol ≥ <input aria-label="min volume" type="number" value={draft.minVolume}
                onChange={(e) => setDraft({ ...draft, minVolume: Number(e.target.value) || 0 })} style={{ width: 90 }} /></label>
              <div style={{ display: "flex", justifyContent: "space-between", marginTop: 4 }}>
                <button type="button" className="btn" onClick={resetDefaults}>Reset defaults</button>
                <button type="button" className="btn btn-primary" onClick={applyFilters}>Apply</button>
              </div>
            </div>
          </div>
        )}
      </div>
      {variant === "scanner" && (
        <div className="mono" style={{ padding: "3px 8px", color: palette.textMuted, borderBottom: `1px solid ${palette.border}` }}>
          {formatFilterSummary(thresholds)}
        </div>
      )}
      <table style={{ width: "100%", borderCollapse: "collapse" }}>
        <thead>
          <tr style={{ color: palette.textMuted, textAlign: "right" }}>
            {COLUMNS.map((c) => (
              <th key={c.col} style={{ ...th, textAlign: c.align, cursor: "pointer" }} onClick={() => clickSort(c.col)}
                className={`col-head${sort?.col === c.col ? " sort-active" : ""}`}>
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
              style={{ cursor: "pointer", textAlign: "right", opacity: r.muted ? 0.55 : 1, userSelect: "none",
                background: selected ? "rgba(154,106,27,.16)" : r.isNewHit ? "rgba(154,106,27,.10)" : "transparent",
                boxShadow: selected ? `inset 0 0 0 1px ${palette.accent}` : r.isNewHit ? `inset 2px 0 0 ${palette.accent}` : "none" }}>
              <td style={{ textAlign: "left", padding: "2px 8px" }}>{r.symbol}</td>
              <td style={{ padding: "2px 8px", color: r.changePct === null ? palette.textMuted : r.changePct > 0 ? palette.up : r.changePct < 0 ? palette.down : palette.text }}>{formatChangePct(r.changePct)}</td>
              <td style={{ padding: "2px 8px" }}>{r.last === null ? "—" : r.last.toFixed(2)}</td>
              <td style={{ padding: "2px 8px" }}>{formatCompactShares(r.floatShares)}</td>
              <td style={{ padding: "2px 8px" }}>{formatCompactShares(r.volume)}</td>
            </tr>
            );
          })}
          {rows.length === 0 && cv.refreshedAt && (
            <tr><td colSpan={5} style={{ padding: 12, color: palette.textMuted, textAlign: "center" }}>{variant === "movers" ? "No movers right now." : "No symbols match the current filters."}</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
