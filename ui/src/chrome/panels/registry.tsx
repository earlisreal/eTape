import type { FC } from "react";
import type { AckMsg, TopicName } from "../../wire/contract";
import type { PanelConfig } from "../workspace";
import type { Stores } from "../../data/registry";
import type { Scheduler } from "../../render/Scheduler";
import type { LinkGroups } from "../linkGroups";
import { ConnectionStatusPanel } from "./ConnectionStatusPanel";
import { SmokePainterPanel } from "./SmokePainterPanel";
import { ChartPanel } from "./ChartPanel";
import { LadderPanel } from "./LadderPanel";
import { TapePanel } from "./TapePanel";
import { ScannerPanel } from "./ScannerPanel";
import { NewsPanel } from "./NewsPanel";
import { AccountBarPanel } from "./AccountBarPanel";
import { PositionsPanel } from "./PositionsPanel";
import { OpenOrdersPanel } from "./OpenOrdersPanel";
import { OrderTicketPanel } from "./OrderTicketPanel";

export interface PanelProps {
  config: PanelConfig;
  stores: Stores;
  scheduler: Scheduler;
  width: number;
  height: number;
  linkGroups: LinkGroups;
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg>; sendQuery(name: string, args: unknown): Promise<unknown> };
  // Persist a panel's own settings (timeframe, indicators, …). AppShell updates the
  // workspace doc's matching panel entry and debounce-saves via WorkspaceStore.
  onConfigChange: (settings: Record<string, unknown>) => void;
}
export interface PanelDef {
  component: FC<PanelProps>;
  topics: TopicName[];
  title: string;
  glyph: string;
  description: string;
  symbolBearing: boolean;
}

// Plan 1 registered the two stack-proving panels; Plan 2 added the chart panel;
// Plan 3 added the L2 ladder + time & sales; Plan 4 added scanner / movers / news;
// Plan 5 adds the execution surfaces (account-bar / positions / open-orders /
// order-ticket). Plan 6 owns Playwright smoke E2E + ui/dist static serving.
export const PANELS: Record<string, PanelDef> = {
  "connection-status": {
    component: ({ stores }) => <ConnectionStatusPanel health={stores.health} />,
    topics: ["sys.health", "sys.events"],
    title: "Connection",
    glyph: "⇄",
    description: "Link latency, event log",
    symbolBearing: false,
  },
  "smoke-painter": {
    component: SmokePainterPanel,
    topics: ["md.quote"],
    title: "Smoke",
    glyph: "•",
    description: "Dev-only painter probe",
    symbolBearing: false,
  },
  "chart": {
    component: ChartPanel,
    topics: ["md.bars", "md.indicator"],
    title: "Chart",
    glyph: "▁▃▅▇",
    description: "Candles, volume, indicators",
    symbolBearing: true,
  },
  "ladder": {
    component: LadderPanel,
    topics: ["md.book", "md.tape", "exec.orders"],
    title: "DOM Ladder",
    glyph: "≡",
    description: "10-level depth, working orders",
    symbolBearing: true,
  },
  "tape": {
    component: TapePanel,
    topics: ["md.tape"],
    title: "Time & Sales",
    glyph: "⋮⋮",
    description: "Live prints, buy/sell colored",
    symbolBearing: true,
  },
  "scanner": {
    component: (p) => <ScannerPanel {...p} session="premarket" />,
    topics: ["scanner.rank", "scanner.hit"],
    title: "Scanner",
    glyph: "%",
    description: "Pre-market gappers, filters",
    symbolBearing: false,
  },
  "movers": {
    component: (p) => <ScannerPanel {...p} session="rth" />,
    topics: ["scanner.rank", "scanner.hit"],
    title: "Movers",
    glyph: "↕",
    description: "RTH % leaders",
    symbolBearing: false,
  },
  "news": {
    component: NewsPanel,
    topics: ["news.item"],
    title: "News",
    glyph: "¶",
    description: "Headlines for focused symbol",
    symbolBearing: true,
  },
  "account-bar": {
    component: AccountBarPanel,
    topics: ["exec.account", "exec.status"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, arm",
    symbolBearing: false,
  },
  "positions": {
    component: PositionsPanel,
    topics: ["exec.positions", "md.quote"],
    title: "Positions",
    glyph: "□",
    description: "Live P&L, flatten per row",
    symbolBearing: false,
  },
  "open-orders": {
    component: OpenOrdersPanel,
    topics: ["exec.orders", "exec.status"],
    title: "Open Orders",
    glyph: "◷",
    description: "Lifecycle, cancel, cancel-all",
    symbolBearing: false,
  },
  "order-ticket": {
    component: OrderTicketPanel,
    topics: ["md.quote", "exec.account", "exec.positions", "exec.status"],
    title: "Order Ticket",
    glyph: "$",
    description: "Presets, sizing, kill switch",
    symbolBearing: false,
  },
};

export const DEV_PANELS = new Set(["smoke-painter"]);
export const isDevPanel = (panelId: string): boolean => DEV_PANELS.has(panelId);

const CATALOG_ORDER = ["chart", "ladder", "tape", "scanner", "movers", "news",
  "account-bar", "positions", "open-orders", "order-ticket", "connection-status"];
// Task 19 replaces "account-bar","positions" here with the single merged "account".

export const CATALOG = CATALOG_ORDER
  .filter((id) => PANELS[id])
  .map((id) => ({ panelId: id, title: PANELS[id].title, glyph: PANELS[id].glyph, description: PANELS[id].description }));
