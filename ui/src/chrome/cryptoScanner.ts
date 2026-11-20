import { createContext, useEffect, useMemo, useState } from "react";
import type { CryptoScannerRow, CryptoScannerSnapshot, ListPerpetualMarketsArgs, PerpetualMarket } from "../gen/wsmsg";
import type { CommandClient } from "./catalogs";
import { sortRows } from "./sortColumns";

export const binanceDemoExchange = "BINANCE-DEMO";
export const cryptoExchanges = ["MEXC", "BINANCE", binanceDemoExchange, "BINGX"] as const;
export type CryptoExchange = typeof cryptoExchanges[number];
export const defaultCryptoExchange = cryptoExchanges[0];
export const cryptoExchangeLabel: Record<CryptoExchange, string> = {
  MEXC: "MEXC", BINANCE: "Binance", BINGX: "BingX", [binanceDemoExchange]: "Binance Futures Demo",
};

export const cryptoExchangeKey = "crypto.exchange.v1";
export const preferredCryptoAccountKey = "crypto.account.v1";
export const followAccountExchange = "follow-account";

export function cryptoExchange(value: unknown): CryptoExchange | undefined {
  return cryptoExchanges.find(exchange => exchange === value);
}

export function useCryptoPreference(commands: CommandClient, key: string, enabled = true) {
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    const read = () => { void commands.sendCommand("GetConfig", { key }).then(ack => {
      if (active && ack.status === "accepted") setValue(typeof ack.value === "string" ? ack.value : "");
    }).catch(() => {}); };
    const channel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel(key);
    if (channel) channel.onmessage = read;
    window.addEventListener(key, read);
    read();
    return () => { active = false; channel?.close(); window.removeEventListener(key, read); };
  }, [commands, key, enabled]);
  const save = async (next: string) => {
    setError("");
    try {
      const ack = await commands.sendCommand("SetConfig", { key, value: next });
      if (ack.status !== "accepted") throw new Error(ack.reason || "Could not save preference.");
      setValue(next);
      window.dispatchEvent(new Event(key));
      if (typeof BroadcastChannel !== "undefined") {
        const channel = new BroadcastChannel(key);
        channel.postMessage(null);
        channel.close();
      }
    } catch (err) { setError(err instanceof Error ? err.message : "Could not save preference."); }
  };
  return { value, error, save };
}

export function cryptoBrokerExchange(broker: string | undefined): CryptoExchange | undefined {
  if (broker === "binance-demo") return binanceDemoExchange;
  if (broker === "bingx-vst" || broker === "bingx-live") return "BINGX";
  return broker === "mexc-sim" || broker === "mexc" ? "MEXC" : undefined;
}

type CryptoSortKey = "base" | "last" | "reportedChangePct" | "turnover";
type ExchangeSelection = CryptoExchange | typeof followAccountExchange | undefined;
type CryptoSort = { col: CryptoSortKey; dir: "asc" | "desc" };

export const cryptoColumns: { key: CryptoSortKey; label: string }[] = [
  { key: "base", label: "Market" },
  { key: "last", label: "Last" },
  { key: "reportedChangePct", label: "Change %" },
  { key: "turnover", label: "24h Turnover (USDT)" },
];

export function readCryptoList(settings: Record<string, unknown>) {
  const saved = settings.cryptoSort as Partial<CryptoSort> | undefined;
  const sort: CryptoSort = saved?.col && cryptoColumns.some(column => column.key === saved.col) &&
    (saved.dir === "asc" || saved.dir === "desc") ? { col: saved.col, dir: saved.dir } : { col: "turnover", dir: "desc" };
  const exchange: ExchangeSelection = settings.cryptoExchange === followAccountExchange ? followAccountExchange : cryptoExchange(settings.cryptoExchange);
  return { sort, exchange };
}

export function resolveListExchange(
  selection: ExchangeSelection,
  groupMarket: string | undefined,
  appDefault: CryptoExchange,
  configuredExchanges: ReadonlySet<CryptoExchange>,
): CryptoExchange | undefined {
  if (selection !== followAccountExchange) return selection ?? appDefault;
  const exchange = cryptoExchange(groupMarket);
  return exchange && configuredExchanges.has(exchange) ? exchange : undefined;
}

interface CryptoListRow {
  market: PerpetualMarket;
  quote: CryptoScannerRow | undefined;
}

const sortAccessors = {
  base: (row: CryptoListRow) => row.market.base,
  last: (row: CryptoListRow) => row.quote?.last ?? null,
  reportedChangePct: (row: CryptoListRow) => row.quote?.reportedChangePct ?? null,
  turnover: (row: CryptoListRow) => row.quote?.turnover ?? null,
};

export function rankCryptoList(data: CryptoListData | undefined, sort: CryptoSort, following = false): CryptoListRow[] {
  if (!data?.markets || (following && !data.snapshot)) return [];
  const quotes = new Map(data.snapshot?.rows.map(quote => [quote.symbol, quote]));
  const rows = data.markets.map(market => ({ market, quote: quotes.get(market.symbol) }));
  return sortRows(following ? rows.filter(row => row.quote && sortAccessors[sort.col](row) !== null) : rows, sort, sortAccessors);
}

export const marketSearchKey = (value: string) => value.toUpperCase().replace(/[^A-Z0-9]/g, "");

interface CryptoListData {
  markets?: readonly PerpetualMarket[];
  catalogError?: string;
  snapshot?: CryptoScannerSnapshot;
  quoteError?: string;
}

export const CryptoListContext = createContext<{
  defaultExchange: CryptoExchange;
  configuredExchanges: ReadonlySet<CryptoExchange>;
  byExchange: Record<CryptoExchange, CryptoListData>;
}>({
  defaultExchange: defaultCryptoExchange,
  configuredExchanges: new Set(),
  byExchange: { MEXC: {}, BINANCE: {}, [binanceDemoExchange]: {}, BINGX: {} },
});

// Each workspace shares a catalog and a ticker poll across its lists for an exchange.
export function useCryptoListData(commands: CommandClient, connected: boolean, needed: boolean, exchange: CryptoExchange): CryptoListData {
  const [catalog, setCatalog] = useState<Pick<CryptoListData, "markets" | "catalogError">>({});
  const [quotes, setQuotes] = useState<Pick<CryptoListData, "snapshot" | "quoteError">>({});
  useEffect(() => {
    if (!needed || !connected) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      let delay = 5_000;
      try {
        const ack = await commands.sendCommand("ListPerpetualMarkets", { exchange } satisfies ListPerpetualMarketsArgs);
        if (!active) return;
        if (ack.status !== "accepted" || !Array.isArray(ack.value)) throw new Error(ack.reason || "Could not load markets.");
        const markets = (ack.value as PerpetualMarket[]).filter(market => market.exchange === exchange);
        setCatalog({ markets });
        delay = 60_000;
      } catch (err) {
        if (active) setCatalog(previous => ({ ...previous, catalogError: err instanceof Error ? err.message : "Could not load markets." }));
      } finally {
        if (active) timer = setTimeout(() => void refresh(), delay);
      }
    };
    void refresh();
    return () => { active = false; clearTimeout(timer); };
  }, [commands, connected, needed, exchange]);

  useEffect(() => {
    if (!needed || !connected) {
      setQuotes({});
      return;
    }
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const ack = await commands.sendCommand("GetCryptoScanner", { exchange });
        if (!active) return;
        if (ack.status !== "accepted" || !ack.value) throw new Error(ack.reason || "Waiting for crypto quotes.");
        const snapshot = ack.value as CryptoScannerSnapshot;
        setQuotes(Date.now() - snapshot.asOfMs <= 16_000 ? { snapshot } : { quoteError: "Waiting for fresh crypto quotes." });
      } catch (err) {
        if (active) setQuotes({ quoteError: err instanceof Error ? err.message : "Waiting for crypto quotes." });
      } finally {
        if (active) timer = setTimeout(() => void refresh(), 5_000);
      }
    };
    void refresh();
    const check = setInterval(() => setQuotes(previous =>
      previous.snapshot && Date.now() - previous.snapshot.asOfMs > 16_000
        ? { quoteError: "Waiting for fresh crypto quotes." } : previous), 1_000);
    return () => { active = false; clearTimeout(timer); clearInterval(check); };
  }, [commands, connected, needed, exchange]);
  return useMemo(() => {
    if (!connected) return { ...catalog, quoteError: "Waiting for engine connection." };
    return needed ? { ...catalog, ...quotes } : catalog;
  }, [catalog, connected, needed, quotes]);
}
