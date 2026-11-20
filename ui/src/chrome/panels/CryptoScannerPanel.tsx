import { useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { soundEngine } from "../../sound/SoundEngine";
import { Button } from "../controls/Button";
import { useDismissibleLayer } from "../controls/useDismissibleLayer";
import { CryptoScannerContext, cryptoColumns, cryptoExchange, cryptoExchanges, cryptoExchangeLabel, cryptoModes, followAccountExchange, rankCryptoScanner, readCryptoScanner, resolveScannerExchange } from "../cryptoScanner";
import { venueMarket } from "../linkGroups";
import { sortIndicator, toggleSort } from "../sortColumns";
import { PanelHeaderSlotContext } from "./headerSlot";
import type { PanelProps } from "./registry";
import { ScannerHeader, ScannerSyncControl } from "./ScannerControls";

const price = new Intl.NumberFormat(undefined, { maximumSignificantDigits: 9 });
const turnover = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 2 });
const filterFields = [
  ["minMove", "Min 5m move (%)"],
  ["minTurnover", "Min 24h turnover (USDT)"],
  ["maxSpread", "Max spread (bps)"],
] as const;
type FilterDraft = ReturnType<typeof readCryptoScanner>;

function formatMove(value: number | null): string {
  if (value === null) return "—";
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return `${sign}${Math.abs(value).toFixed(2)}%`;
}

export function CryptoScannerPanel({ config, scannerSync, group: groupProp, linkGroups, onConfigChange }: PanelProps): JSX.Element {
  const headerSlot = useContext(PanelHeaderSlotContext);
  const [settings, setSettings] = useState(config.settings);
  const [draft, setDraft] = useState<FilterDraft | null>(null);
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);
  const filtersRef = useRef<HTMLDivElement>(null);
  const filterTriggerRef = useRef<HTMLButtonElement>(null);
  useDismissibleLayer(filtersRef, () => setDraft(null), { enabled: draft !== null, anchor: filterTriggerRef.current });

  const { defaultExchange, configuredExchanges, byExchange } = useContext(CryptoScannerContext);
  const filters = useMemo(() => readCryptoScanner(settings), [settings]);
  const group = groupProp === undefined ? config.group : groupProp;
  const groupMarket = useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.marketFor(group));
  const followsAccount = filters.exchange === followAccountExchange;
  const exchange = resolveScannerExchange(filters.exchange, groupMarket, defaultExchange, configuredExchanges);
  const data = exchange ? byExchange[exchange] : undefined;
  const snapshot = data?.snapshot;
  const exchangeLabel = exchange ? cryptoExchangeLabel[exchange] : undefined;
  const groupLabel = group ? group.charAt(0).toUpperCase() + group.slice(1) : "";
  const context = [exchangeLabel, followsAccount && (group ? `Follows ${groupLabel}` : "Follow Account")].filter(Boolean).join(" · ");
  const muted = settings.scannerSoundMuted === true;
  const rows = useMemo(() => rankCryptoScanner(snapshot?.rows ?? [], filters), [snapshot, filters]);
  const warming = snapshot?.warming && filters.mode !== "Most Active";
  const previous = useRef<{ settings: Record<string, unknown>; exchange: typeof exchange; symbols: Set<string> } | null>(null);
  const lastSound = useRef(0);
  useEffect(() => {
    if (!snapshot || warming) {
      previous.current = null;
      return;
    }
    const prior = previous.current;
    if (!muted && prior?.settings === settings && prior.exchange === exchange &&
      rows.some(row => !prior.symbols.has(row.symbol)) && Date.now() - lastSound.current >= 30_000) {
      lastSound.current = Date.now();
      soundEngine.scannerHit();
    }
    previous.current = { settings, exchange, symbols: new Set(rows.map(row => row.symbol)) };
  }, [exchange, muted, rows, settings, snapshot, warming]);

  const update = (patch: Record<string, unknown>) => {
    setSettings(current => ({ ...current, ...patch }));
    onConfigChange(patch);
  };
  const applyFilters = (next: FilterDraft) => {
    update({
      cryptoExchange: next.exchange ?? "",
      cryptoMode: next.mode,
      minMove: next.minMove,
      minTurnover: next.minTurnover,
      maxSpread: next.maxSpread,
      ...(next.mode !== filters.mode && { cryptoSort: null }),
    });
    setDraft(null);
    filterTriggerRef.current?.focus();
  };
  const openMarket = (symbol: string) => {
    if (followsAccount && (!group || linkGroups.marketFor(group) !== venueMarket(symbol))) return;
    linkGroups.focus(group ?? "green", symbol);
  };
  const summary = [
    filters.mode,
    filters.minMove > 0 && `move ≥ ${filters.minMove}%`,
    filters.minTurnover > 0 && `turnover ≥ ${turnover.format(filters.minTurnover)} USDT`,
    filters.maxSpread > 0 && `spread ≤ ${filters.maxSpread} bps`,
  ].filter(Boolean).join(" · ");
  const status = !exchange
    ? !group ? "Choose a Link Group to follow Account"
      : groupMarket === "stocks" ? `Paused while ${groupLabel} is on Stocks`
      : "Choose a configured crypto account in Account"
    : data?.error || (!snapshot ? "Loading markets…" : warming ? "Collecting a 5-minute baseline…" : null);

  return (
    <section className="scanner-panel">
      <ScannerHeader title="Crypto Scanner" context={context} muted={muted} onMute={() => update({ scannerSoundMuted: !muted })}
        filtersOpen={draft !== null} onFilters={() => setDraft(draft ? null : filters)} filterTriggerRef={filterTriggerRef} />
      {draft && (
        <div ref={filtersRef} className="popover crypto-scanner-filters"
          style={{ top: headerSlot === undefined ? 30 : 6, right: 8, maxHeight: headerSlot === undefined ? "calc(100% - 36px)" : "calc(100% - 12px)" }}>
          <form onSubmit={event => { event.preventDefault(); applyFilters(draft); }}>
            <label>Exchange
              <select aria-label="Crypto Scanner exchange" value={draft.exchange ?? ""}
                onChange={event => {
                  const selected = event.target.value;
                  setDraft({ ...draft, exchange: selected === followAccountExchange ? followAccountExchange : cryptoExchange(selected) });
                }}>
                <option value="">App default ({cryptoExchangeLabel[defaultExchange]})</option>
                <option value={followAccountExchange} disabled={!group}>Follow Account</option>
                {cryptoExchanges.map(value => <option key={value} value={value}>{cryptoExchangeLabel[value]}</option>)}
              </select>
            </label>
            <label>Rank
              <select aria-label="Crypto Scanner ranking" value={draft.mode}
                onChange={event => setDraft({ ...draft, mode: cryptoModes.find(mode => mode === event.target.value) ?? draft.mode })}>
                {cryptoModes.map(mode => <option key={mode}>{mode}</option>)}
              </select>
            </label>
            {filterFields.map(([key, label]) =>
              <label key={key}>{label}
                <input type="number" min={0} step="any" value={draft[key] || ""} placeholder="Any"
                  onChange={event => setDraft({ ...draft, [key]: Math.max(0, Number(event.target.value) || 0) })} />
              </label>
            )}
            <div className="crypto-scanner-filter-actions">
              <Button onClick={() => setDraft(readCryptoScanner({}))}>Reset defaults</Button>
              <Button type="submit" variant="primary">Apply</Button>
            </div>
          </form>
        </div>
      )}
      {scannerSync && <ScannerSyncControl sync={scannerSync} sourceLabel="Crypto Monitoring source" />}
      <div className="mono scanner-summary scanner-truncate" data-testid="scanner-filter-summary" title={summary}>{summary}</div>
      {status && <div className="scanner-status" role="status">{status}</div>}
      {exchange && <div className="scanner-results">
        <table aria-label={`${exchangeLabel} crypto Scanner results`} className="scanner-table">
          <thead><tr>{cryptoColumns.map(({ key, label, title }) => {
            const sorted = filters.sort.col === key;
            return <th key={key} scope="col" className={`col-head sortable${sorted ? " sort-active" : ""}`}
              aria-sort={sorted ? filters.sort.dir === "asc" ? "ascending" : "descending" : undefined}>
              <button type="button" className="scanner-column-button" title={title} aria-label={title}
                onClick={() => update({ cryptoSort: toggleSort(filters.sort, key) })}>
                {label} <span aria-hidden="true">{sortIndicator(filters.sort, key)}</span>
              </button>
            </th>;
          })}</tr></thead>
          <tbody>
            {rows.map(row => (
              <tr key={row.symbol} className={row.symbol === selectedSymbol ? "scanner-selected" : undefined}
                onClick={() => setSelectedSymbol(row.symbol)} onDoubleClick={() => openMarket(row.symbol)}>
                <td className="mono scanner-symbol">
                  <button type="button" className="scanner-symbol-button" title={`Double-click to open ${row.base}/USDT on ${exchangeLabel}`}
                    onClick={event => { if (event.detail === 0) openMarket(row.symbol); }}>{row.base}/USDT</button>
                </td>
                <td className="mono scanner-change" data-direction={row.changePct === null ? "missing" : row.changePct > 0 ? "up" : row.changePct < 0 ? "down" : undefined}>
                  {formatMove(row.changePct)}
                </td>
                <td className="mono">{price.format(row.last)}</td>
                <td className="mono">{row.turnover === null ? "—" : turnover.format(row.turnover)}</td>
                <td className="mono">{row.spreadBps === null ? "—" : row.spreadBps.toFixed(2)}</td>
              </tr>
            ))}
            {!status && rows.length === 0 && (
              <tr className="scanner-empty"><td colSpan={cryptoColumns.length} role="status">No markets match your filters.</td></tr>
            )}
          </tbody>
        </table>
      </div>}
    </section>
  );
}
