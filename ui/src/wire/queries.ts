import { EngineService, Side } from "../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/index.js";
import type * as Generated from "../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/models.js";
import type {
  CarriedPosition,
  Fill,
  LocateEligibility,
  LocateListResult,
  LocateQuoteResult,
  QueryChartWindowArgs,
  QueryCycleFillsArgs,
  QueryFillsArgs,
  QueryLocateArgs,
  QueryLocateEligibilityArgs,
  QueryLocateQuotesArgs,
  QueryLocatesArgs,
} from "./contract";

export type QueryChartWindowResult = Generated.QueryChartWindowResult;
export interface ExportQueryResult {
  csv: string;
  count: number;
  error?: string;
}

export interface QueryClient {
  QueryChartWindow(args: QueryChartWindowArgs): Promise<QueryChartWindowResult>;
  QueryFills(args: QueryFillsArgs): Promise<Fill[]>;
  QueryCycleFills(args: QueryCycleFillsArgs): Promise<{ cycleStartMs: number; carried: CarriedPosition[]; fills: Fill[] }>;
  ExportFills(args: { venue: string; preset: string; from?: string; to?: string }): Promise<ExportQueryResult>;
  QueryLocateEligibility(args: QueryLocateEligibilityArgs): Promise<LocateEligibility>;
  QueryLocateQuotes(args: QueryLocateQuotesArgs): Promise<LocateQuoteResult>;
  QueryLocates(args: QueryLocatesArgs): Promise<LocateListResult>;
  QueryLocate(args: QueryLocateArgs): Promise<Generated.LocateRecord>;
}

export type LegacyQuery = (name: string, args: unknown) => Promise<unknown>;

export function makeQueryClient(useWails: boolean, sendQuery: LegacyQuery): QueryClient {
  return useWails ? wailsQueries() : legacyQueries(sendQuery);
}

export function queryClient(commands: { queries?: QueryClient; sendQuery: LegacyQuery }): QueryClient {
  return commands.queries ?? legacyQueries(commands.sendQuery);
}

function wailsQueries(): QueryClient {
  return {
    QueryChartWindow: async (args) => EngineService.QueryChartWindow(args),
    QueryFills: async (args) => (await EngineService.QueryFills(args) ?? []).map(fillFromGenerated),
    QueryCycleFills: async (args) => {
      const result = await EngineService.QueryCycleFills(args);
      return {
        cycleStartMs: result.cycleStartMs,
        carried: result.carried ?? [],
        fills: (result.fills ?? []).map(fillFromGenerated),
      };
    },
    ExportFills: async (args) => EngineService.ExportFills(args),
    QueryLocateEligibility: async (args) => EngineService.QueryLocateEligibility(args),
    QueryLocateQuotes: async (args) => {
      const result = await EngineService.QueryLocateQuotes(args);
      return { quotes: result.quotes ?? [], errors: result.errors ?? [], error: result.error };
    },
    QueryLocates: async (args) => {
      const result = await EngineService.QueryLocates(args);
      return { locates: result.locates ?? [], nextPageToken: result.nextPageToken, error: result.error };
    },
    QueryLocate: async (args) => EngineService.QueryLocate(args),
  };
}

function legacyQueries(sendQuery: LegacyQuery): QueryClient {
  return {
    QueryChartWindow: (args) => sendQuery("QueryChartWindow", args).then(decodeChartWindow),
    QueryFills: (args) => sendQuery("QueryFills", args).then((value) => decodeFills(value).map(fillFromGenerated)),
    QueryCycleFills: (args) => sendQuery("QueryCycleFills", args).then(decodeCycleFills),
    ExportFills: (args) => sendQuery("ExportFills", args).then(decodeExport),
    QueryLocateEligibility: (args) => sendQuery("QueryLocateEligibility", args).then(decodeEligibility),
    QueryLocateQuotes: (args) => sendQuery("QueryLocateQuotes", args).then(decodeQuotes),
    QueryLocates: (args) => sendQuery("QueryLocates", args).then(decodeLocates),
    QueryLocate: (args) => sendQuery("QueryLocate", args).then(decodeRecord),
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function record(value: unknown, label: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(`${label} response was not an object`);
  return value;
}

function text(value: unknown): string { return typeof value === "string" ? value : ""; }
function integer(value: unknown): number { return typeof value === "number" && Number.isFinite(value) ? value : 0; }
function flag(value: unknown): boolean { return value === true; }
function list(value: unknown): unknown[] { return Array.isArray(value) ? value : []; }

function decodeChartWindow(value: unknown): Generated.QueryChartWindowResult {
  const raw = record(value, "QueryChartWindow");
  return {
    symbol: text(raw.symbol),
    timeframe: text(raw.timeframe),
    fromMs: integer(raw.fromMs),
    toMs: integer(raw.toMs),
    bars: list(raw.bars).map((item) => {
      const bar = record(item, "bar");
      return {
        symbol: text(bar.symbol), timeframe: text(bar.timeframe), bucketStart: text(bar.bucketStart),
        o: integer(bar.o), h: integer(bar.h), l: integer(bar.l), c: integer(bar.c), v: integer(bar.v),
        inProgress: flag(bar.inProgress), gap: bar.gap === true, volumeOnly: bar.volumeOnly === true,
      };
    }),
    indicators: list(raw.indicators).map((item) => {
      const series = record(item, "indicator series");
      return {
        seriesKey: text(series.seriesKey),
        points: list(series.points).map((point) => {
          const p = record(point, "indicator point");
          return { timeMs: integer(p.timeMs), value: integer(p.value) };
        }),
      };
    }),
    historyRevision: integer(raw.historyRevision),
  };
}

function decodeFills(value: unknown): Generated.Fill[] {
  return list(value).map((item) => {
    const raw = record(item, "fill");
    return {
      venue: text(raw.venue), orderId: text(raw.orderId), symbol: text(raw.symbol), side: generatedSide(raw.side),
      qty: integer(raw.qty), price: integer(raw.price), tsMs: integer(raw.tsMs),
    };
  });
}

function decodeCycleFills(value: unknown): { cycleStartMs: number; carried: CarriedPosition[]; fills: Fill[] } {
  const raw = record(value, "QueryCycleFills");
  const carried = list(raw.carried).map((item) => {
    const position = record(item, "carried position");
    return { symbol: text(position.symbol), qty: integer(position.qty) };
  });
  return { cycleStartMs: integer(raw.cycleStartMs), carried, fills: decodeFills(raw.fills).map(fillFromGenerated) };
}

function decodeExport(value: unknown): ExportQueryResult {
  const raw = record(value, "ExportFills");
  const result: ExportQueryResult = { csv: text(raw.csv), count: integer(raw.count) };
  if (typeof raw.error === "string" && raw.error !== "") result.error = raw.error;
  return result;
}

function decodeEligibility(value: unknown): LocateEligibility {
  const raw = record(value, "QueryLocateEligibility");
  return {
    supported: flag(raw.supported), found: flag(raw.found),
    borrowStatus: typeof raw.borrowStatus === "string" ? raw.borrowStatus : null,
    shortable: typeof raw.shortable === "boolean" ? raw.shortable : null,
    marginable: typeof raw.marginable === "boolean" ? raw.marginable : null,
    tradable: typeof raw.tradable === "boolean" ? raw.tradable : null,
    error: text(raw.error),
  };
}

function decodeQuotes(value: unknown): LocateQuoteResult {
  const raw = record(value, "QueryLocateQuotes");
  return {
    quotes: list(raw.quotes).map((item) => {
      const quote = record(item, "locate quote");
      return { symbol: text(quote.symbol), availableQty: integer(quote.availableQty), price: text(quote.price), quotedAt: text(quote.quotedAt) };
    }),
    errors: list(raw.errors).map((item) => {
      const error = record(item, "locate quote error");
      return { symbol: text(error.symbol), code: text(error.code), message: text(error.message) };
    }),
    error: text(raw.error),
  };
}

function decodeLocates(value: unknown): LocateListResult {
  const raw = record(value, "QueryLocates");
  return {
    locates: list(raw.locates).map(decodeRecord),
    nextPageToken: text(raw.nextPageToken),
    error: text(raw.error),
  };
}

function decodeRecord(value: unknown): Generated.LocateRecord {
  const raw = record(value, "QueryLocate");
  const result: Generated.LocateRecord = {
    id: text(raw.id), symbol: text(raw.symbol), requestedQty: integer(raw.requestedQty), limitPrice: text(raw.limitPrice),
    allOrNone: flag(raw.allOrNone), status: text(raw.status), createdAt: text(raw.createdAt), locatedQty: integer(raw.locatedQty),
    locatedPrice: text(raw.locatedPrice), totalFee: text(raw.totalFee), expiresAt: text(raw.expiresAt),
  };
  if (typeof raw.error === "string" && raw.error !== "") result.error = raw.error;
  return result;
}

function generatedSide(value: unknown): Side {
  switch (value) {
    case "BUY": return Side.SideBuy;
    case "SELL": return Side.SideSell;
    case "SHORT": return Side.SideShort;
    case "COVER": return Side.SideCover;
    default: throw new Error("fill response contained an unknown side");
  }
}

function fillFromGenerated(value: Generated.Fill): Fill {
  switch (String(value.side)) {
    case "BUY": return { venue: value.venue, orderId: value.orderId, symbol: value.symbol, side: "BUY", qty: value.qty, price: value.price, tsMs: value.tsMs };
    case "SELL": return { venue: value.venue, orderId: value.orderId, symbol: value.symbol, side: "SELL", qty: value.qty, price: value.price, tsMs: value.tsMs };
    case "SHORT": return { venue: value.venue, orderId: value.orderId, symbol: value.symbol, side: "SHORT", qty: value.qty, price: value.price, tsMs: value.tsMs };
    case "COVER": return { venue: value.venue, orderId: value.orderId, symbol: value.symbol, side: "COVER", qty: value.qty, price: value.price, tsMs: value.tsMs };
    default: throw new Error("fill response contained an unknown side");
  }
}
