import { createContext, useEffect, useState } from "react";
import type { CryptoScannerRow, CryptoScannerSnapshot } from "../gen/wsmsg";
import type { CommandClient } from "./catalogs";

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

export const cryptoModes = ["5m Gainers", "5m Losers", "Most Active"] as const;
export type CryptoMode = typeof cryptoModes[number];
type CryptoSortKey = "base" | "last" | "changePct" | "turnover" | "spreadBps";
type CryptoSort = { col: CryptoSortKey; dir: "asc" | "desc" };
export const cryptoColumns: { key: CryptoSortKey; label: string; title?: string }[] = [
  { key: "base", label: "Market" },
  { key: "changePct", label: "5m %" },
  { key: "last", label: "Last" },
  { key: "turnover", label: "24h Turnover", title: "24-hour turnover (USDT)" },
  { key: "spreadBps", label: "Spread", title: "Spread (bps)" },
];

export function readCryptoScanner(settings: Record<string, unknown>) {
  const mode = cryptoModes.find(mode => settings.cryptoMode === mode) ?? cryptoModes[0];
  const numeric = (value: unknown) => typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0;
  const saved = settings.cryptoSort as Partial<CryptoSort> | undefined;
  const sort: CryptoSort = saved?.col && cryptoColumns.some(column => column.key === saved.col) &&
    (saved.dir === "asc" || saved.dir === "desc") ? { col: saved.col, dir: saved.dir } :
    mode === "Most Active" ? { col: "turnover", dir: "desc" } :
      { col: "changePct", dir: mode === "5m Losers" ? "asc" : "desc" };
  const exchange: CryptoExchange | typeof followAccountExchange | undefined =
    settings.cryptoExchange === followAccountExchange ? followAccountExchange : cryptoExchange(settings.cryptoExchange);
  return {
    mode, sort, exchange,
    minMove: numeric(settings.minMove), minTurnover: numeric(settings.minTurnover), maxSpread: numeric(settings.maxSpread),
  };
}

export function resolveScannerExchange(
  selection: ReturnType<typeof readCryptoScanner>["exchange"],
  groupMarket: string | undefined,
  appDefault: CryptoExchange,
): CryptoExchange | undefined {
  return selection === followAccountExchange ? cryptoExchange(groupMarket) : selection ?? appDefault;
}

export function rankCryptoScanner(rows: readonly CryptoScannerRow[], settings: ReturnType<typeof readCryptoScanner>): CryptoScannerRow[] {
  return rows.filter(row => {
    if (settings.minTurnover > 0 && (row.turnover === null || row.turnover < settings.minTurnover)) return false;
    if (settings.maxSpread > 0 && (row.spreadBps === null || row.spreadBps > settings.maxSpread)) return false;
    if (settings.mode === "Most Active" && settings.minMove === 0) return row.turnover !== null;
    const move = row.changePct;
    if (move === null || Math.abs(move) < settings.minMove) return false;
    return settings.mode === "Most Active" ? row.turnover !== null :
      settings.mode === "5m Gainers" ? move > 0 : move < 0;
  }).sort((a, b) => {
    const left = a[settings.sort.col], right = b[settings.sort.col];
    if (left === null) return right === null ? a.symbol.localeCompare(b.symbol) : 1;
    if (right === null) return -1;
    const result = typeof left === "number" && typeof right === "number" ? left - right : String(left).localeCompare(String(right));
    return (settings.sort.dir === "asc" ? result : -result) || a.symbol.localeCompare(b.symbol);
  });
}

export interface CryptoScannerData {
  exchange: CryptoExchange;
  snapshot?: CryptoScannerSnapshot;
  error?: string;
}

export interface CryptoScannerBoards {
  defaultExchange: CryptoExchange;
  byExchange: Record<CryptoExchange, CryptoScannerData>;
}

export const CryptoScannerContext = createContext<CryptoScannerBoards>({
  defaultExchange: defaultCryptoExchange,
  byExchange: {
    MEXC: { exchange: "MEXC" },
    BINANCE: { exchange: "BINANCE" },
    [binanceDemoExchange]: { exchange: binanceDemoExchange },
    BINGX: { exchange: "BINGX" },
  },
});

export function useCryptoScannerData(commands: CommandClient, connected: boolean, needed: boolean, exchange: CryptoExchange): CryptoScannerData {
  const [data, setData] = useState<CryptoScannerData>({ exchange });
  useEffect(() => {
    if (!needed || !connected) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const ack = await commands.sendCommand("GetCryptoScanner", { exchange });
        if (!active) return;
        if (ack.status !== "accepted" || !ack.value) throw new Error(ack.reason || "Waiting for crypto data.");
        const snapshot = ack.value as CryptoScannerSnapshot;
        setData(Date.now() - snapshot.asOfMs <= 16_000 ? { exchange, snapshot } : { exchange, error: "Waiting for fresh crypto data." });
      } catch (err) {
        if (active) setData({ exchange, error: err instanceof Error ? err.message : "Waiting for crypto data." });
      } finally {
        if (active) timer = setTimeout(() => void refresh(), 5_000);
      }
    };
    void refresh();
    const check = setInterval(() => setData(previous =>
      previous.snapshot && Date.now() - previous.snapshot.asOfMs > 16_000
        ? { exchange, error: "Waiting for fresh crypto data." } : previous), 1_000);
    return () => { active = false; clearTimeout(timer); clearInterval(check); };
  }, [commands, connected, needed, exchange]);
  if (!needed) return { exchange };
  if (!connected) return { exchange, error: "Waiting for engine connection." };
  if (data.snapshot && Date.now() - data.snapshot.asOfMs > 16_000) return { exchange, error: "Waiting for fresh crypto data." };
  return data.exchange === exchange ? data : { exchange };
}
