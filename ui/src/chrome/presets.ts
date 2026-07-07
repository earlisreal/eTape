import type { PanelConfig } from "./workspace";
import type { DockviewApi, SerializedDockview } from "dockview";

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
// `toJSON().panels` keys are unchanged — see task-7-report.md for detail).
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

// Grid shape: outer horizontal split into 3 flat columns sized [1.7, 1,
// 1.05] (left charts, center DOM/tape, right execution rail); left column
// rows [1, 1]; center column rows [1.15, 1]; right column rows [auto, 1, 1]
// (order ticket is naturally compact, approximated here as a slightly
// smaller fixed weight since dockview's serialized grid sizes are plain
// numbers — there's no literal "auto" in the schema).
const TRADING_LAYOUT: SerializedDockview = {
  grid: {
    root: {
      type: "branch",
      data: [
        {
          type: "branch",
          size: 725,
          data: [
            { type: "leaf", size: 450, data: { id: "t-chart-1m", views: ["t-chart-1m"], activeView: "t-chart-1m" } },
            { type: "leaf", size: 450, data: { id: "t-chart-10s", views: ["t-chart-10s"], activeView: "t-chart-10s" } },
          ],
        },
        {
          type: "branch",
          size: 427,
          data: [
            { type: "leaf", size: 481, data: { id: "t-dom", views: ["t-dom"], activeView: "t-dom" } },
            { type: "leaf", size: 419, data: { id: "t-tape", views: ["t-tape"], activeView: "t-tape" } },
          ],
        },
        {
          type: "branch",
          size: 448,
          data: [
            { type: "leaf", size: 258, data: { id: "t-ticket", views: ["t-ticket"], activeView: "t-ticket" } },
            { type: "leaf", size: 321, data: { id: "t-account", views: ["t-account"], activeView: "t-account" } },
            { type: "leaf", size: 321, data: { id: "t-orders", views: ["t-orders"], activeView: "t-orders" } },
          ],
        },
      ],
    },
    height: 900,
    width: 1600,
    orientation: "HORIZONTAL",
  },
  panels: {
    "t-chart-1m": { id: "t-chart-1m", contentComponent: "t-chart-1m", title: "chart" },
    "t-chart-10s": { id: "t-chart-10s", contentComponent: "t-chart-10s", title: "chart" },
    "t-dom": { id: "t-dom", contentComponent: "t-dom", title: "ladder" },
    "t-tape": { id: "t-tape", contentComponent: "t-tape", title: "tape" },
    "t-ticket": { id: "t-ticket", contentComponent: "t-ticket", title: "order-ticket" },
    "t-account": { id: "t-account", contentComponent: "t-account", title: "account-bar" },
    "t-orders": { id: "t-orders", contentComponent: "t-orders", title: "open-orders" },
  },
  activeGroup: "t-chart-1m",
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
    description: "Focused charts + DOM, tape, ticket, positions. The execution seat.",
    build: () => ({
      panels: [
        chart("t-chart-1m", "US.AAPL", "1m", "blue"),
        chart("t-chart-10s", "US.AAPL", "10s", "blue"),
        { id: "t-dom", panelId: "ladder", group: "blue", settings: { symbol: "US.AAPL" } },
        { id: "t-tape", panelId: "tape", group: "blue", settings: { symbol: "US.AAPL", minSize: 0 } },
        { id: "t-ticket", panelId: "order-ticket", group: "blue", settings: {} },
        // t-account uses account-bar until Task 19 swaps this panelId to the merged "account"
        { id: "t-account", panelId: "account-bar", group: null, settings: {} },
        { id: "t-orders", panelId: "open-orders", group: null, settings: {} },
      ],
      layout: TRADING_LAYOUT,
    }),
  },
];

/** Replace the current dockview layout with the preset's. Caller writes ws.panels/layout first. */
export function applyPreset(api: DockviewApi, preset: Preset): void {
  const { layout } = preset.build();
  api.clear();
  api.fromJSON(layout);
}
