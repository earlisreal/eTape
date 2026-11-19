import { useContext, useEffect, useMemo, useRef, useState } from "react";
import { soundEngine } from "../../sound/SoundEngine";
import { Button } from "../controls/Button";
import { CryptoScannerContext, cryptoColumns, cryptoExchanges, cryptoExchangeLabel, cryptoModes, rankCryptoScanner, readCryptoScanner } from "../cryptoScanner";
import { scannerSyncStatusText } from "../scannerSync";
import type { PanelProps } from "./registry";

const price = new Intl.NumberFormat(undefined, { maximumSignificantDigits: 9 });
const turnover = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 2 });
const decimal = (value: number | null) => value === null ? "—" : value.toFixed(2);

export function CryptoScannerPanel({ config, scannerSync, group, linkGroups, onConfigChange }: PanelProps): JSX.Element {
  const [settings, setSettings] = useState(config.settings);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const { defaultExchange, byExchange } = useContext(CryptoScannerContext);
  const filters = useMemo(() => readCryptoScanner(settings), [settings]);
  const exchange = filters.exchange ?? defaultExchange;
  const { snapshot, error } = byExchange[exchange];
  const muted = settings.scannerSoundMuted === true;
  const rows = useMemo(() => rankCryptoScanner(snapshot?.rows ?? [], filters), [snapshot, filters]);
  const previous = useRef<{ settings: Record<string, unknown>; symbols: Set<string> } | null>(null);
  const lastSound = useRef(0);
  useEffect(() => {
    if (!snapshot || (snapshot.warming && filters.mode !== "Most Active")) {
      previous.current = null;
      return;
    }
    const prior = previous.current;
    if (!muted && prior?.settings === settings && rows.some(row => !prior.symbols.has(row.symbol)) && Date.now() - lastSound.current >= 30_000) {
      lastSound.current = Date.now();
      soundEngine.scannerHit();
    }
    previous.current = { settings, symbols: new Set(rows.map(row => row.symbol)) };
  }, [filters.mode, muted, rows, settings, snapshot]);

  const update = (patch: Record<string, unknown>) => {
    setSettings(current => ({ ...current, ...patch }));
    onConfigChange(patch);
  };
  return <section style={{ display: "flex", flexDirection: "column", minWidth: 0, height: "100%", fontSize: 12 }}>
    <div style={{ display: "flex", gap: 6, padding: 6, flexWrap: "wrap", alignItems: "center" }}>
      <select className="ctl" aria-label="Crypto Scanner exchange" value={filters.exchange ?? ""}
        onChange={event => update({ cryptoExchange: event.target.value })}>
        <option value="">App default ({cryptoExchangeLabel[defaultExchange]})</option>
        {cryptoExchanges.map(value => <option key={value} value={value}>{cryptoExchangeLabel[value]}</option>)}
      </select>
      <select className="ctl" aria-label="Crypto Scanner ranking" value={filters.mode}
        onChange={event => update({ cryptoMode: event.target.value, cryptoSort: null })}>
        {cryptoModes.map(mode => <option key={mode}>{mode}</option>)}
      </select>
      <Button variant="quiet" aria-expanded={filtersOpen} onClick={() => setFiltersOpen(open => !open)}>Filters</Button>
      <Button variant="quiet" aria-pressed={muted} onClick={() => update({ scannerSoundMuted: !muted })}>{muted ? "Unmute" : "Mute"}</Button>
    </div>
    {filtersOpen && <div style={{ display: "flex", gap: 6, padding: 6, flexWrap: "wrap" }}>
      {([["minMove", "Min 5m move (%)"], ["minTurnover", "Min 24h turnover (USDT)"], ["maxSpread", "Max spread (bps)"]] as const).map(([key, label]) =>
        <label key={key} style={{ display: "grid", gap: 4 }}>{label}<input className="field" type="number" min={0} step="any"
          value={filters[key] || ""} placeholder="Any" onChange={event => update({ [key]: Math.max(0, Number(event.target.value) || 0) })} /></label>)}
    </div>}
    {scannerSync && <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 6px" }}>
      {scannerSync.selected ? <>
        <span>Sync to Following</span>
        <Button variant="quiet" aria-label={scannerSync.enabled ? "Disable Scanner Sync" : "Enable Scanner Sync"}
          aria-pressed={scannerSync.enabled} onClick={scannerSync.onToggle}>{scannerSync.enabled ? "ON" : "OFF"}</Button>
      </> : <>
        <span>Crypto Monitoring source</span>
        <Button variant="quiet" onClick={scannerSync.onSelect}>Use this Scanner</Button>
      </>}
      {scannerSync.statusVisible && scannerSync.status.kind !== "disabled" && <span role="status">{scannerSyncStatusText(scannerSync.status)}</span>}
    </div>}
    {(error || !snapshot || (snapshot.warming && filters.mode !== "Most Active") || rows.length === 0) &&
      <p role="status" style={{ margin: 6, color: "var(--text-muted)" }}>{error ||
        (!snapshot ? "Loading markets…" : snapshot.warming && filters.mode !== "Most Active" ? "Collecting a 5-minute baseline…" : "No markets match your filters.")}</p>}
    <div style={{ minHeight: 0, overflow: "auto" }}>
      <table aria-label={`${exchange} crypto Scanner results`} className="mono" style={{ width: "100%", borderCollapse: "collapse", textAlign: "right" }}>
        <thead><tr>{cryptoColumns.map(({ key, label }) => <th key={key} scope="col"
          style={{ textAlign: key === "base" ? "left" : "right", position: "sticky", top: 0, background: "var(--surface)" }}
          aria-sort={filters.sort.col === key ? filters.sort.dir === "asc" ? "ascending" : "descending" : "none"}>
          <Button variant="quiet" onClick={() => update({ cryptoSort: { col: key, dir: filters.sort.col === key && filters.sort.dir === "desc" ? "asc" : "desc" } })}>{label}</Button>
        </th>)}</tr></thead>
        <tbody>{rows.map(row => <tr key={row.symbol} onDoubleClick={() => linkGroups.focus(group ?? "green", row.symbol)}
          style={{ borderBottom: "1px solid var(--border)" }}>
          <td style={{ textAlign: "left" }}><Button variant="quiet" title={`Open ${row.base}/USDT on ${exchange}`}
            onClick={() => linkGroups.focus(group ?? "green", row.symbol)}>{row.base}/USDT</Button></td>
          <td>{price.format(row.last)}</td><td>{decimal(row.changePct)}</td>
          <td>{row.turnover === null ? "—" : turnover.format(row.turnover)}</td><td>{decimal(row.spreadBps)}</td>
        </tr>)}</tbody>
      </table>
    </div>
  </section>;
}
