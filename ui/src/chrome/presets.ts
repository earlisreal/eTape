import type { PanelConfig } from "./workspace";
import type { SerializedDockview } from "dockview";

export interface Preset {
  id: string;
  name: string;
  description: string;
  thumb: "monitoring" | "trading";
  build(): { panels: PanelConfig[]; layout: SerializedDockview };
}

const chart = (id: string, symbol: string, timeframe: string, group: PanelConfig["group"]): PanelConfig =>
  ({ id, panelId: "chart", group, settings: { symbol, timeframe } });

// NOTE: `layout` below is real dockview serialized JSON (SerializedDockview),
// structurally validated against dockview-core's `fromJSON`/`toJSON` (built
// headlessly in a jsdom vitest environment via `createDockview` + `addPanel`,
// then hand-tuned for the mockup proportions and round-tripped through a
// fresh `DockviewApi.fromJSON` to confirm it doesn't throw and that
// `toJSON().panels` keys are unchanged — see `presets.dockview.test.ts`, added
// in the Trade History plan's Task 10, for the actual round-trip check).
//
// Grid shape (matches docs mockups presets.html): outer horizontal split
// [chart wall, right rail] sized 2:1; chart wall is a 2x2 (two horizontal
// rows stacked vertically); rail is a flat vertical stack of 3 rows sized
// [1.2, 1, 0.9].
const MONITORING_LAYOUT: SerializedDockview = {
  grid: {
    root: {
      type: "branch",
      data: [
        {
          type: "branch",
          size: 1067,
          data: [
            {
              type: "branch",
              size: 450,
              data: [
                { type: "leaf", size: 534, data: { id: "m-chart-red", views: ["m-chart-red"], activeView: "m-chart-red" } },
                { type: "leaf", size: 533, data: { id: "m-chart-green", views: ["m-chart-green"], activeView: "m-chart-green" } },
              ],
            },
            {
              type: "branch",
              size: 450,
              data: [
                { type: "leaf", size: 534, data: { id: "m-chart-blue", views: ["m-chart-blue"], activeView: "m-chart-blue" } },
                { type: "leaf", size: 533, data: { id: "m-chart-yellow", views: ["m-chart-yellow"], activeView: "m-chart-yellow" } },
              ],
            },
          ],
        },
        {
          type: "branch",
          size: 533,
          data: [
            { type: "leaf", size: 348, data: { id: "m-scanner", views: ["m-scanner"], activeView: "m-scanner" } },
            { type: "leaf", size: 290, data: { id: "m-movers", views: ["m-movers"], activeView: "m-movers" } },
            { type: "leaf", size: 262, data: { id: "m-news", views: ["m-news"], activeView: "m-news" } },
          ],
        },
      ],
    },
    height: 900,
    width: 1600,
    orientation: "HORIZONTAL",
  },
  panels: {
    "m-chart-red": { id: "m-chart-red", contentComponent: "m-chart-red", title: "chart" },
    "m-chart-green": { id: "m-chart-green", contentComponent: "m-chart-green", title: "chart" },
    "m-chart-blue": { id: "m-chart-blue", contentComponent: "m-chart-blue", title: "chart" },
    "m-chart-yellow": { id: "m-chart-yellow", contentComponent: "m-chart-yellow", title: "chart" },
    "m-scanner": { id: "m-scanner", contentComponent: "m-scanner", title: "scanner" },
    "m-movers": { id: "m-movers", contentComponent: "m-movers", title: "movers" },
    "m-news": { id: "m-news", contentComponent: "m-news", title: "news" },
  },
  activeGroup: "m-chart-red",
} as SerializedDockview;

// Sourced verbatim from Earl's own saved `main` workspace export
// (etape-layout-2026-07-13.json, exported 2026-07-12T22:05:32Z) — his actual
// working layout, not a hand-built mockup like MONITORING_LAYOUT above. Grid
// leaf `id`s intentionally don't match the panel ids they hold (e.g. leaf
// "t-ticket" holds view "t-dom") — that's real dockview state from panels
// being rearranged after the groups were first created, kept as exported.
export const TRADING_LAYOUT: SerializedDockview = {
  grid: {
    root: {
      type: "branch",
      data: [
        {
          type: "branch",
          data: [
            {
              type: "branch",
              data: [
                { type: "leaf", data: { views: ["chart-977336c7"], activeView: "chart-977336c7", id: "2", hideHeader: true }, size: 670 },
                { type: "leaf", data: { views: ["t-chart-1m"], activeView: "t-chart-1m", id: "t-chart-1m", hideHeader: true }, size: 719 },
              ],
              size: 480,
            },
            {
              type: "branch",
              data: [
                { type: "leaf", data: { views: ["watchlist-75d05981"], activeView: "watchlist-75d05981", id: "3", hideHeader: true }, size: 305 },
                { type: "leaf", data: { views: ["movers-51fd77fe", "news-eb65ba23"], activeView: "news-eb65ba23", id: "4" }, size: 365 },
                { type: "leaf", data: { views: ["t-chart-10s"], activeView: "t-chart-10s", id: "t-chart-10s", hideHeader: true }, size: 719 },
              ],
              size: 446,
            },
          ],
          size: 1389,
        },
        {
          type: "branch",
          data: [
            {
              type: "branch",
              data: [
                { type: "leaf", data: { views: ["t-dom"], activeView: "t-dom", id: "t-ticket", hideHeader: true }, size: 294 },
                { type: "leaf", data: { views: ["t-tape"], activeView: "t-tape", id: "t-tape", hideHeader: true }, size: 237 },
              ],
              size: 308,
            },
            { type: "leaf", data: { views: ["t-ticket"], activeView: "t-ticket", id: "t-account", hideHeader: true }, size: 195 },
            { type: "leaf", data: { views: ["t-account"], activeView: "t-account", id: "t-orders", hideHeader: true }, size: 423 },
          ],
          size: 531,
        },
      ],
      size: 926,
    },
    width: 1920,
    height: 926,
    orientation: "HORIZONTAL",
  },
  panels: {
    "chart-977336c7": { id: "chart-977336c7", contentComponent: "chart-977336c7", title: "Chart" },
    "t-chart-1m": { id: "t-chart-1m", contentComponent: "t-chart-1m", title: "chart" },
    "watchlist-75d05981": { id: "watchlist-75d05981", contentComponent: "watchlist-75d05981", title: "Watchlist" },
    "movers-51fd77fe": { id: "movers-51fd77fe", contentComponent: "movers-51fd77fe", title: "Movers" },
    "news-eb65ba23": { id: "news-eb65ba23", contentComponent: "news-eb65ba23", title: "News" },
    "t-chart-10s": { id: "t-chart-10s", contentComponent: "t-chart-10s", title: "chart" },
    "t-dom": { id: "t-dom", contentComponent: "t-dom", title: "ladder" },
    "t-tape": { id: "t-tape", contentComponent: "t-tape", title: "tape" },
    "t-ticket": { id: "t-ticket", contentComponent: "t-ticket", title: "order-ticket" },
    "t-account": { id: "t-account", contentComponent: "t-account", title: "account" },
  },
  activeGroup: "2",
} as SerializedDockview;

export const PRESETS: Preset[] = [
  {
    id: "monitoring", name: "Monitoring", thumb: "monitoring",
    description: "Chart wall + scanner, movers, news. Watching the market, not trading it.",
    build: () => ({
      panels: [
        chart("m-chart-red", "US.TSLA", "1m", "red"),
        chart("m-chart-green", "US.NVDA", "1m", "green"),
        chart("m-chart-blue", "US.AAPL", "1m", "blue"),
        chart("m-chart-yellow", "US.SPY", "1m", "yellow"),
        { id: "m-scanner", panelId: "scanner", group: null, settings: { thresholds: { minChangePct: 10, floatCapShares: 20_000_000, minVolume: 100_000 } } },
        { id: "m-movers", panelId: "movers", group: null, settings: { thresholds: { minChangePct: 5, floatCapShares: null, minVolume: 500_000 } } },
        { id: "m-news", panelId: "news", group: "blue", settings: {} },
      ],
      layout: MONITORING_LAYOUT,
    }),
  },
  {
    id: "trading", name: "Trading", thumb: "trading",
    description: "Focused charts + DOM, tape, ticket, positions, watchlist, movers, news. The execution seat.",
    build: () => ({
      panels: [
        {
          id: "t-chart-1m", panelId: "chart", group: "blue",
          settings: {
            symbol: "US.AAPL", timeframe: "10s",
            indicators: [
              { instanceId: "t-chart-1m:VWAP-0", type: "VWAP", params: {}, hidden: false, styles: { line: { color: "#089981" } } },
            ],
            chartType: "candle", hideAllDrawings: false,
            chartSettings: { sessionShading: true, grid: true, volume: true, watermark: false },
            drawingRailPos: { x: 0, y: 445.9375 },
          },
        },
        {
          id: "t-chart-10s", panelId: "chart", group: "blue",
          settings: {
            symbol: "US.AAPL", timeframe: "D",
            indicators: [
              { instanceId: "t-chart-10s:SMA-0", type: "SMA", params: { period: 200 }, styles: { line: { color: "#F23645" } }, hidden: false },
            ],
            hideAllDrawings: false,
            drawingRailPos: { x: 114.125, y: 304.9375 },
            chartSettings: { sessionShading: true, grid: true, volume: true, watermark: false },
          },
        },
        { id: "t-dom", panelId: "ladder", group: "blue", settings: { symbol: "US.AAPL" } },
        { id: "t-tape", panelId: "tape", group: "blue", settings: { symbol: "US.AAPL", minSize: 10 } },
        { id: "t-ticket", panelId: "order-ticket", group: "blue", settings: {} },
        {
          id: "t-account", panelId: "account", group: "blue",
          settings: { ordersHeight: 119, tab: "positions", ordersSort: { col: "side", dir: "desc" } },
        },
        {
          id: "chart-977336c7", panelId: "chart", group: "blue",
          settings: {
            symbol: "US.AAPL", timeframe: "1m",
            indicators: [
              { instanceId: "chart-977336c7:MACD-0", type: "MACD", params: { fast: 12, slow: 26, signal: 9 }, styles: { hist: { hidden: true } }, collapsed: true },
              { instanceId: "chart-977336c7:VWAP-0", type: "VWAP", params: {}, styles: { line: { color: "#089981" } } },
            ],
            chartType: "candle", hideAllDrawings: false,
            chartSettings: { sessionShading: true, grid: true, volume: true, watermark: false },
            drawingRailPos: { x: 298.375, y: 448 },
          },
        },
        { id: "movers-51fd77fe", panelId: "movers", group: "blue", settings: {} },
        { id: "news-eb65ba23", panelId: "news", group: "blue", settings: {} },
        { id: "watchlist-75d05981", panelId: "watchlist", group: "blue", settings: {} },
      ],
      layout: TRADING_LAYOUT,
    }),
  },
];
