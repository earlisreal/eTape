import type { Workspace } from "../chrome/workspace";

// Seed documents. Panel ids match the registry in chrome/panels/registry.tsx.
// Plan 1 registered only connection-status and smoke-painter; Plan 2 adds the
// real chart panel used below. Remaining panelIds (ladder/tape/scanner/movers/
// news/account-bar/positions/open-orders/order-ticket) still render the
// "coming soon" placeholder frame rather than crashing (see PanelFrame).
const chart = (id: string, symbol: string, timeframe: string, group: NonNullable<Workspace["panels"][number]["group"]>): Workspace["panels"][number] =>
  ({ id, panelId: "chart", group, settings: { symbol, timeframe } });

export const SEED_WORKSPACES: Record<"monitoring" | "trading", Workspace> = {
  monitoring: {
    name: "Monitoring",
    panels: [
      chart("m-c1", "US.AAPL", "1m", "green"),
      chart("m-c2", "US.NVDA", "1m", "blue"),
      chart("m-c3", "US.TSLA", "1m", "red"),
      chart("m-c4", "US.SPY", "1m", "yellow"),
      { id: "m-scanner", panelId: "scanner", group: null, settings: {} },
      { id: "m-movers", panelId: "movers", group: null, settings: {} },
      { id: "m-news", panelId: "news", group: "green", settings: {} },
      { id: "m-conn", panelId: "connection-status", group: null, settings: {} },
      { id: "m-smoke", panelId: "smoke-painter", group: null, settings: {} },
    ],
    layout: { grid: "seed-monitoring" },
  },
  trading: {
    name: "Trading",
    panels: [
      chart("t-c1", "US.AAPL", "1m", "green"),
      chart("t-c2", "US.AAPL", "10s", "green"),
      chart("t-c3", "US.AAPL", "5m", "green"),
      chart("t-c4", "US.AAPL", "60m", "green"),
      { id: "t-ladder", panelId: "ladder", group: "green", settings: { symbol: "US.AAPL" } },
      { id: "t-tape", panelId: "tape", group: "green", settings: {} },
      { id: "t-account", panelId: "account-bar", group: null, settings: {} },
      { id: "t-positions", panelId: "positions", group: null, settings: {} },
      { id: "t-orders", panelId: "open-orders", group: null, settings: {} },
      { id: "t-ticket", panelId: "order-ticket", group: "green", settings: {} },
    ],
    layout: { grid: "seed-trading" },
  },
};
