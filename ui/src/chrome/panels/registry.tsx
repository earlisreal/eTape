import type { FC } from "react";
import type { AckMsg, TopicName } from "../../wire/contract";
import type { DemandProfile } from "../../wire/DemandRegistry";
import type { PanelConfig } from "../workspace";
import type { Stores } from "../../data/registry";
import type { Scheduler } from "../../render/Scheduler";
import type { LinkGroup, LinkGroups } from "../linkGroups";
import { ConnectionStatusPanel } from "./ConnectionStatusPanel";
import { SmokePainterPanel } from "./SmokePainterPanel";
import { ChartPanel } from "./ChartPanel";
import { LadderPanel } from "./LadderPanel";
import { TapePanel } from "./TapePanel";
import { ScannerPanel } from "./ScannerPanel";
import { NewsPanel } from "./NewsPanel";
import { AccountPanel } from "./AccountPanel";
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
  // Task 12: whether this panel is dockview's currently-active panel (drives the
  // bronze focus ring — .panel-focused) and the group-swatch picker's pick handler
  // (see GroupPicker). Both live entirely in PanelFrame's own ledger header, not in
  // any panel body — kept optional here (rather than required) so the many existing
  // Body-level tests that construct a PanelProps literal directly (ChartPanel,
  // LadderPanel, TapePanel, NewsPanel, ScannerPanel, AccountPanel,
  // OpenOrdersPanel, OrderTicketPanel) don't need touching for a header-only feature;
  // PanelFrame's own component signature (below) still requires and always supplies
  // them, since AppShell always has both.
  active?: boolean;
  onGroupChange?: (group: LinkGroup) => void;
}
export interface PanelDef {
  component: FC<PanelProps>;
  topics: TopicName[];
  title: string;
  glyph: string;
  description: string;
  symbolBearing: boolean;
  demand?: DemandProfile;
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
    demand: "watch",
  },
  "ladder": {
    component: LadderPanel,
    topics: ["md.book", "md.tape", "exec.orders"],
    title: "DOM Ladder",
    glyph: "≡",
    description: "10-level depth, working orders",
    symbolBearing: true,
    demand: "focused",
  },
  "tape": {
    component: TapePanel,
    topics: ["md.tape"],
    title: "Time & Sales",
    glyph: "⋮⋮",
    description: "Live prints, buy/sell colored",
    symbolBearing: true,
    demand: "watch",
  },
  "scanner": {
    component: (p) => <ScannerPanel {...p} variant="scanner" />,
    topics: ["scanner.rank", "scanner.hit"],
    title: "Scanner",
    glyph: "%",
    description: "Live gappers, all sessions, filters",
    symbolBearing: false,
  },
  "movers": {
    component: (p) => <ScannerPanel {...p} variant="movers" />,
    topics: ["scanner.rank", "scanner.hit"],
    title: "Movers",
    glyph: "↕",
    description: "Live % leaders, all sessions",
    symbolBearing: false,
  },
  "news": {
    component: NewsPanel,
    topics: ["news.item"],
    title: "News",
    glyph: "¶",
    description: "Headlines for focused symbol",
    symbolBearing: true,
    demand: "interest",
  },
  "account": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.status", "md.quote"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, positions, arm",
    symbolBearing: false,
  },
  // Back-compat aliases: a saved workspace doc referencing the pre-Task-19 ids
  // still resolves to the merged panel (a doc with a lone "positions" panel now
  // renders the full Account surface — acceptable per the Task 19 plan).
  "account-bar": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.status", "md.quote"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, positions, arm",
    symbolBearing: false,
  },
  "positions": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.status", "md.quote"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, positions, arm",
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
  "account", "open-orders", "order-ticket", "connection-status"];
// "account-bar" and "positions" stay registered in PANELS (above) as back-compat
// aliases for saved workspace docs, but are intentionally absent from the Add
// Panel catalog — only the merged "account" is offered going forward.

export const CATALOG = CATALOG_ORDER
  .filter((id) => PANELS[id])
  .map((id) => ({ panelId: id, title: PANELS[id].title, glyph: PANELS[id].glyph, description: PANELS[id].description }));
