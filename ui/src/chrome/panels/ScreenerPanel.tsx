import { useState, useSyncExternalStore, type CSSProperties } from "react";
import type { ScreenerFilters, ScreenerRow } from "../../wire/contract";
import type { PanelProps } from "./registry";
import { Button } from "../controls/Button";
import { useTheme } from "../ThemeProvider";
import { DEFAULT_SCREENER_FILTERS, readScreenerFilters, readScreenerPresets } from "../screenerFilters";
import { sortRows, toggleSort, sortIndicator, type SortState } from "../sortColumns";
import { formatDollarTurnover } from "../format";
import { formatPrice, quoteDecimals } from "../../render/format";
import { bareSymbol } from "../exec/orderStatus";

const field: CSSProperties = { display: "grid", gap: 4, fontSize: 11, minWidth: 110 };
const input: CSSProperties = { background: "var(--surface)", color: "var(--text)", border: "1px solid var(--border)", borderRadius: 3, padding: "5px 7px", font: "inherit", minWidth: 0, width: "100%", boxSizing: "border-box" };
const grid: CSSProperties = { display: "grid", gridTemplateColumns: "repeat(auto-fit,minmax(140px,1fr))", gap: 10 };
const cell: CSSProperties = { padding: "7px 9px", borderBottom: "1px solid var(--border)", whiteSpace: "nowrap", textAlign: "right" };
const price = (value: number) => formatPrice(value, quoteDecimals(value, 2));
const percent = (value: number | null | undefined) => value == null ? "—" : `${value.toFixed(2)}%`;
const accessors: Record<string, (row: ScreenerRow) => string | number | null> = {
  symbol: r => r.symbol, close: r => r.close, high: r => r.high, distancePct: r => r.distancePct,
  status: r => r.closeConfirmed ? 2 : r.newHigh ? 1 : 0, dollarVolume: r => r.dollarVolume ?? null,
  marketCap: r => r.marketCap ?? null, changePct: r => r.changePct ?? null, atrPct: r => r.atrPct ?? null,
};
const columns = [["symbol", "Symbol"], ["close", "Close"], ["high", "High"], ["distancePct", "% below high"], ["status", "Status"], ["dollarVolume", "Avg $ volume"], ["marketCap", "Market cap"], ["changePct", "1M change"], ["atrPct", "ATR %"]];

export function ScreenerPanel({ config, screenerId, stores, commands, onConfigChange, group: liveGroup, linkGroups }: Pick<PanelProps, "config" | "screenerId" | "stores" | "commands" | "onConfigChange" | "group" | "linkGroups">): JSX.Element {
  const { palette } = useTheme();
  const group = liveGroup === undefined ? config.group : liveGroup;
  const panelId = screenerId ?? config.id;
  const screens = useSyncExternalStore(cb => stores.screener.subscribe(cb), () => stores.screener.getSnapshot());
  const state = screens.get(panelId);
  const [filters, setFilters] = useState(() => readScreenerFilters(config.settings.screenerFilters));
  const [presets, setPresets] = useState(() => readScreenerPresets(config.settings.screenerPresets));
  const [preset, setPreset] = useState("");
  const [presetName, setPresetName] = useState("");
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const [selected, setSelected] = useState("");
  const [sort, setSort] = useState<SortState>({ col: "distancePct", dir: "asc" });
  const busy = sending || ["queued", "discovering", "running"].includes(state?.status ?? "");
  const changed = !!state && JSON.stringify(filters) !== JSON.stringify(state.filters);
  const update = (patch: Partial<ScreenerFilters>) => {
    const next = { ...filters, ...patch };
    setFilters(next); setPreset(""); onConfigChange({ screenerFilters: next });
  };
  const action = async (name: string, args: unknown) => {
    setError("");
    try { const ack = await commands.sendCommand(name, args); if (ack.status !== "accepted") setError(ack.reason ?? "Action failed"); }
    catch (e) { setError(String(e)); }
  };
  const run = async () => { setSending(true); try { await action("RunScreener", { panelId, filters }); } finally { setSending(false); } };
  const choosePreset = (value: string) => {
    const next = value === "builtin-near" ? { ...DEFAULT_SCREENER_FILTERS, mode: "near" }
      : value === "builtin-new" ? { ...DEFAULT_SCREENER_FILTERS, mode: "new" }
      : presets.find(p => `saved:${p.name}` === value)?.filters;
    if (next) { update(next); setPreset(value); }
  };
  const savePreset = () => {
    const name = presetName.trim();
    if (!name) return;
    if (presets.length >= 20 && !presets.some(p => p.name === name)) { setError("Save up to 20 presets per panel."); return; }
    const next = [...presets.filter(p => p.name !== name), { name, filters: { ...filters } }];
    setPresets(next); setPreset(`saved:${name}`); setPresetName(""); onConfigChange({ screenerPresets: next });
  };
  const numberField = (label: string, key: keyof ScreenerFilters, scale = 1) => {
    const value = filters[key];
    return <label style={field}>{label}<input aria-label={label} type="number" min={key.includes("Change") ? undefined : 0} step="any" style={input}
      value={typeof value === "number" ? (value === 0 && !key.includes("Change") ? "" : value / scale) : ""}
      onChange={e => update({ [key]: e.target.value === "" && key.includes("Change") ? undefined : Number(e.target.value) * scale })} /></label>;
  };
  const rows = sortRows(state?.rows ?? [], sort, accessors);
  return <div data-screener-panel={config.id} style={{ height: "100%", display: "flex", flexDirection: "column", color: palette.text, fontSize: 12, minHeight: 0 }}>
    <div style={{ padding: 12, display: "grid", gap: 12, overflow: "auto", maxHeight: "65%", flexShrink: 0, borderBottom: "1px solid var(--border)" }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" }}>
        <select aria-label="Screener preset" style={{ ...input, width: "auto", flex: "1 1 140px" }} value={preset} onChange={e => choosePreset(e.target.value)}>
          <option value="">Custom screen</option><option value="builtin-near">Near ATH</option><option value="builtin-new">ATH Breakouts</option>
          {presets.map(p => <option key={p.name} value={`saved:${p.name}`}>{p.name}</option>)}
        </select>
        <Button variant="primary" disabled={busy} onClick={() => void run()}>{busy ? "Screening…" : "Run screen"}</Button>
        {busy && <Button onClick={() => void action("CancelScreener", { panelId })}>Cancel</Button>}
      </div>
      <div style={grid}>
        <label style={field}>High lookback<select aria-label="High lookback" style={input} value={filters.lookback} onChange={e => update({ lookback: e.target.value })}>
          <option value="all-time">All-time</option><option value="6-month">6 months</option><option value="1-year">1 year</option><option value="trading-days">Custom trading days</option>
        </select></label>
        {filters.lookback === "trading-days" && <label style={field}>Trading days<input aria-label="Trading days" style={input} type="number" min={1} max={2520} step={1} value={filters.tradingDays} onChange={e => update({ tradingDays: Number(e.target.value) })} /></label>}
        <label style={field}>Condition<select aria-label="High condition" style={input} value={filters.mode} onChange={e => update({ mode: e.target.value })}>
          <option value="near">Near high</option><option value="new">New high</option><option value="either">Near or new high</option>
        </select></label>
        {filters.mode !== "new" && <label style={field}>Within % of high<input aria-label="Within % of high" style={input} type="number" min={0} max={100} step="any" value={filters.nearPct} onChange={e => update({ nearPct: Number(e.target.value) })} /></label>}
      </div>
      {filters.mode !== "near" && <label style={{ display: "flex", alignItems: "center", gap: 6 }}><input type="checkbox" checked={filters.requireClose} onChange={e => update({ requireClose: e.target.checked })} />Require breakout close above previous high</label>}
      <details><summary style={{ cursor: "pointer" }}>Additional filters</summary><div style={{ ...grid, marginTop: 10 }}>
        {numberField("Minimum price ($)", "minPrice")}{numberField("Maximum price ($)", "maxPrice")}
        {numberField("Min average daily $ volume (M)", "minDollarVolume", 1e6)}
        {numberField("Minimum market cap (B)", "minMarketCap", 1e9)}{numberField("Maximum market cap (B)", "maxMarketCap", 1e9)}
        {numberField("Minimum 1M change (%)", "minChangePct")}{numberField("Maximum 1M change (%)", "maxChangePct")}
        {numberField("Minimum ATR (%)", "minAtrPct")}{numberField("Maximum ATR (%)", "maxAtrPct")}
        <label style={field}>Extension average<select aria-label="Extension average" style={input} value={filters.extensionSma} onChange={e => update({ extensionSma: Number(e.target.value) })}>
          <option value={0}>Off</option><option value={20}>SMA 20</option><option value={50}>SMA 50</option><option value={200}>SMA 200</option>
        </select></label>{numberField("Max % above average", "maxExtensionPct")}
      </div><div style={{ display: "flex", gap: 12, flexWrap: "wrap", marginTop: 10 }}>
        {([20, 50, 200] as const).map(n => { const key = `aboveSma${n}` as const; return <label key={n}><input type="checkbox" checked={filters[key]} onChange={e => update({ [key]: e.target.checked })} />Above SMA {n}</label>; })}
      </div><p style={{ color: palette.textMuted, fontSize: 11 }}>Blank numeric bounds are off. Average $ volume uses 20 sessions of close × volume; 1M change uses 21 sessions; ATR uses 14-session mean true range. Market cap is close × latest reported issued shares.</p></details>
      <details><summary style={{ cursor: "pointer" }}>Save a preset</summary><div style={{ display: "flex", gap: 6, marginTop: 8 }}>
        <input aria-label="Preset name" placeholder="Preset name" maxLength={60} style={input} value={presetName} onChange={e => setPresetName(e.target.value)} />
        <Button disabled={!presetName.trim()} onClick={savePreset}>Save preset</Button>
        {preset.startsWith("saved:") && <Button onClick={() => { const next = presets.filter(p => `saved:${p.name}` !== preset); setPresets(next); setPreset(""); onConfigChange({ screenerPresets: next }); }}>Delete preset</Button>}
      </div></details>
    </div>
    <div role="status" style={{ padding: "8px 12px", color: palette.textMuted, borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
      {state ? <>Regular session · {state.asOf} · {state.status === "complete" ? `${rows.length} matches` : state.status === "discovering" ? "Discovering US stocks…" : state.status === "queued" ? "Queued…" : state.status === "canceled" ? "Canceled · partial results" : state.status === "error" ? "Screen failed · partial results" : `Checked ${state.checked} / ${state.candidates}`}
        {state.status === "complete" && ` · checked ${state.checked} of ${state.universe} stocks`}
        {state.unavailable > 0 && <div>{state.unavailable} stocks unavailable: incomplete, stale, or missing daily history.</div>}
        {state.warning && <div>{state.warning}</div>}
      </> : "End-of-day US stocks · run a screen to load results"}
      {changed && <div>Filters changed. Run screen to update results.</div>}
      {!group && <div>Choose a Link Group color to load symbols into charts.</div>}
    </div>
    {(error || state?.error) && <div role="alert" style={{ padding: 10, color: palette.down }}>{error || state?.error}</div>}
    <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
      <table aria-label="Screener results" style={{ width: "100%", borderCollapse: "collapse", fontSize: 12 }}>
        <thead style={{ position: "sticky", top: 0, background: palette.surface, zIndex: 1 }}><tr>{columns.map(([key, label]) => <th key={key} style={cell} aria-sort={sort?.col === key ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}>
          <Button variant="quiet" onClick={() => setSort(toggleSort(sort, key))}>{label} {sortIndicator(sort, key)}</Button>
        </th>)}<th style={cell}>Watchlist</th></tr></thead>
        <tbody>{rows.map(row => <tr key={row.symbol} tabIndex={0} aria-selected={selected === row.symbol} onClick={() => setSelected(row.symbol)}
          onDoubleClick={() => { if (group) linkGroups.focus(group, row.symbol); }} onKeyDown={e => { if (e.key === "Enter" && group) { e.preventDefault(); linkGroups.focus(group, row.symbol); } }}
          style={{ background: selected === row.symbol ? "rgba(154,106,27,.16)" : undefined, cursor: "pointer" }}>
          <td style={{ ...cell, textAlign: "left", fontWeight: 700 }} title={`${row.name} · history since ${row.historyStart}`}>{bareSymbol(row.symbol)}</td>
          <td style={cell}>{price(row.close)}</td><td style={cell} title={`Previous high: $${price(row.previousHigh)}`}>{price(row.high)}</td><td style={cell}>{percent(row.distancePct)}</td>
          <td style={{ ...cell, color: row.closeConfirmed ? palette.up : palette.textMuted }}>{row.closeConfirmed ? "New high · close confirmed" : row.newHigh ? "New high · faded" : "Near high"}</td>
          <td style={cell}>{formatDollarTurnover(row.dollarVolume ?? null)}</td><td style={cell}>{formatDollarTurnover(row.marketCap ?? null)}</td><td style={cell}>{percent(row.changePct)}</td><td style={cell}>{percent(row.atrPct)}</td>
          <td style={cell}><Button aria-label={`Add ${bareSymbol(row.symbol)} to watchlist`} onClick={e => { e.stopPropagation(); void action("WatchlistAdd", { symbol: row.symbol }); }}>+ Watch</Button></td>
        </tr>)}</tbody>
      </table>
      {rows.length === 0 && <p style={{ padding: 16, color: palette.textMuted }}>{state?.status === "complete" ? "No stocks match this screen." : busy ? "Loading completed daily history…" : "Choose your filters and run a screen."}</p>}
    </div>
    <div style={{ padding: "6px 12px", color: palette.textMuted, fontSize: 10 }}>Completed regular sessions · split-adjusted daily highs · incomplete histories excluded · double-click a symbol to review</div>
  </div>;
}
