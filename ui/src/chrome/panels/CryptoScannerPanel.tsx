import { useContext, useMemo, useState, useSyncExternalStore } from "react";
import { CryptoListContext, cryptoColumns, cryptoExchangeLabel, cryptoExchanges, followAccountExchange, marketSearchKey, rankCryptoList, readCryptoList, resolveListExchange } from "../cryptoScanner";
import { sortIndicator, toggleSort } from "../sortColumns";
import { useToasts } from "../Toast";
import type { PanelProps } from "./registry";
import { ScannerSyncControl } from "./ScannerControls";

const price = new Intl.NumberFormat(undefined, { maximumSignificantDigits: 9 });
const turnover = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 2 });

function formatMove(value: number | null): string {
  if (value === null) return "—";
  const sign = value > 0 ? "+" : value < 0 ? "−" : "";
  return `${sign}${Math.abs(value).toFixed(2)}%`;
}

export function CryptoListPanel({ config, scannerSync, group: groupProp, linkGroups, onConfigChange }: PanelProps): JSX.Element {
  const [settings, setSettings] = useState(config.settings);
  const [query, setQuery] = useState("");
  const [selectedSymbol, setSelectedSymbol] = useState<string | null>(null);
  const toast = useToasts();

  const { defaultExchange, configuredExchanges, byExchange } = useContext(CryptoListContext);
  const saved = useMemo(() => readCryptoList(settings), [settings]);
  const group = groupProp === undefined ? config.group : groupProp;
  const groupMarket = useSyncExternalStore(cb => linkGroups.subscribe(cb), () => linkGroups.marketFor(group));
  const exchange = resolveListExchange(saved.exchange, groupMarket, defaultExchange, configuredExchanges);
  const data = exchange ? byExchange[exchange] : undefined;
  const exchangeLabel = exchange ? cryptoExchangeLabel[exchange] : undefined;
  const rows = useMemo(() => rankCryptoList(data, saved.sort), [data, saved.sort]);
  const searchIndex = useMemo(() => rows.map(row => ({
    row, pair: marketSearchKey(row.market.base + row.market.settlement), id: marketSearchKey(row.market.symbol),
  })), [rows]);
  const search = marketSearchKey(query);
  const matches = search ? searchIndex.filter(entry => entry.pair.includes(search) || entry.id.includes(search)).map(entry => entry.row) : rows;

  const update = (patch: Record<string, unknown>) => {
    setSettings(current => ({ ...current, ...patch }));
    onConfigChange(patch);
  };
  const openMarket = async (symbol: string) => {
    if (!group || (saved.exchange === followAccountExchange && linkGroups.marketFor(group) !== exchange)) return;
    try {
      const result = await linkGroups.focusChecked(group, symbol);
      if (!result.ok) toast.push({ level: "danger", text: result.reason });
    } catch (err) {
      toast.push({ level: "danger", text: err instanceof Error ? err.message : "Could not load market." });
    }
  };
  const status = exchange
    ? data?.catalogError || (!data?.markets ? "Loading markets…" : data.quoteError || (!data.snapshot ? "Waiting for crypto quotes…" : null))
    : !group ? "Choose a Link Group to follow Account"
      : groupMarket === "stocks" ? "Paused while the Link Group is on Stocks"
      : "Choose a configured crypto account in Account";
  const changeTitle = exchange === "MEXC" ? "MEXC-reported change (UTC+8 reference)" : "Exchange-reported rolling 24-hour change";

  return (
    <section className="scanner-panel">
      <form className="crypto-list-controls" onSubmit={event => {
        event.preventDefault();
        if (matches.length === 1) void openMarket(matches[0].market.symbol);
      }}>
        <select className="field" aria-label="Crypto List exchange" value={saved.exchange ?? ""}
          onChange={event => update({ cryptoExchange: event.target.value })}>
          <option value="">App default ({cryptoExchangeLabel[defaultExchange]})</option>
          <option value={followAccountExchange} disabled={!group}>Follow Account</option>
          {cryptoExchanges.map(value => <option key={value} value={value}>{cryptoExchangeLabel[value]}</option>)}
        </select>
        <input className="field" type="search" aria-label="Search USDT perpetuals" placeholder="Search coin or pair"
          value={query} onChange={event => setQuery(event.target.value)} />
      </form>
      {scannerSync && <ScannerSyncControl sync={scannerSync} sourceLabel="Crypto Monitoring source" sourceName="List" />}
      {!group && exchange && <div className="scanner-status">Choose a Link Group color to load symbols.</div>}
      {status && <div className="scanner-status" role="status">{status}</div>}
      {exchange && <div className="scanner-results">
        <table aria-label={`${exchangeLabel} Crypto List`} className="scanner-table">
          <thead><tr>{cryptoColumns.map(({ key, label }) => {
            const sorted = saved.sort.col === key;
            return <th key={key} scope="col" className={`col-head sortable${sorted ? " sort-active" : ""}`}
              aria-sort={sorted ? saved.sort.dir === "asc" ? "ascending" : "descending" : undefined}>
              <button type="button" className="scanner-column-button" title={key === "reportedChangePct" ? changeTitle : label}
                onClick={() => update({ cryptoSort: toggleSort(saved.sort, key) })}>
                {label} <span aria-hidden="true">{sortIndicator(saved.sort, key)}</span>
              </button>
            </th>;
          })}</tr></thead>
          <tbody>
            {matches.map(({ market, quote }) => {
              const last = quote?.last ?? null;
              const change = quote?.reportedChangePct ?? null;
              const amount = quote?.turnover ?? null;
              return (
                <tr key={market.symbol} className={market.symbol === selectedSymbol ? "scanner-selected" : undefined}
                  onClick={() => setSelectedSymbol(market.symbol)} onDoubleClick={() => void openMarket(market.symbol)}>
                  <td className="mono scanner-symbol">
                    <button type="button" className="scanner-symbol-button" disabled={!group}
                      title={group ? `Load ${market.symbol} in the ${group} Link Group` : "Choose a Link Group color to load symbols"}
                      onClick={event => { if (event.detail === 0) void openMarket(market.symbol); }}>{market.base}/{market.settlement}</button>
                  </td>
                  <td className="mono">{last === null ? "—" : price.format(last)}</td>
                  <td className="mono scanner-change" data-direction={change === null ? "missing" : change > 0 ? "up" : change < 0 ? "down" : undefined}>
                    {formatMove(change)}
                  </td>
                  <td className="mono">{amount === null ? "—" : turnover.format(amount)}</td>
                </tr>
              );
            })}
            {data?.markets && matches.length === 0 && (
              <tr className="scanner-empty"><td colSpan={cryptoColumns.length} role="status">
                {search ? "No matching markets." : "No markets available."}
              </td></tr>
            )}
          </tbody>
        </table>
      </div>}
    </section>
  );
}
