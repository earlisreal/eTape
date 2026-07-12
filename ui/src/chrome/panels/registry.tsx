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
import { WatchlistPanel } from "./WatchlistPanel";
import { StockInfoPanel } from "./StockInfoPanel";
import { AccountPanel } from "./AccountPanel";
import { OrderTicketPanel } from "./OrderTicketPanel";

export interface PanelProps {
  config: PanelConfig;
  stores: Stores;
  scheduler: Scheduler;
  width: number;
  height: number;
  linkGroups: LinkGroups;
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg>; sendQuery(name: string, args: unknown): Promise<unknown> };
  // Persist a PATCH of this panel's settings (send only the keys being changed —
  // e.g. { timeframe } or { indicators }). AppShell merges the patch into the
  // workspace doc's matching panel entry and debounce-saves via WorkspaceStore.
  // Never spread config.settings into the patch: config is frozen at panel
  // creation, so a full rewrite reverts every setting persisted since mount.
  onConfigChange: (patch: Record<string, unknown>) => void;
  // Task 12: whether this panel is dockview's currently-active panel (drives the
  // bronze focus ring — .panel-focused) and the group-swatch picker's pick handler
  // (see GroupPicker). Both live entirely in PanelFrame's own ledger header, not in
  // any panel body — kept optional here (rather than required) so the many existing
  // Body-level tests that construct a PanelProps literal directly (ChartPanel,
  // LadderPanel, TapePanel, StockInfoPanel, ScannerPanel, AccountPanel,
  // OpenOrdersPanel, OrderTicketPanel) don't need touching for a header-only feature;
  // PanelFrame's own component signature (below) still requires and always supplies
  // them, since AppShell always has both.
  active?: boolean;
  onGroupChange?: (group: LinkGroup) => void;
  // The panel's CURRENT link group, live (unlike config.group, which is frozen —
  // dockview captures each panel's factory once at creation and never re-invokes
  // it with a fresh `config` on later re-assignment). PanelFrame keeps its own
  // local `group` state (updated by the GroupPicker) and threads it through here
  // so a symbol-bearing panel body can react to a group re-pick and re-follow the
  // new group's symbol, not just the group it was created with. Optional (like
  // active/onGroupChange above) so Body-level tests constructing a PanelProps
  // literal directly don't need touching; panel bodies fall back to config.group.
  group?: LinkGroup;
}
export interface PanelDef {
  component: FC<PanelProps>;
  topics: TopicName[];
  title: string;
  glyph: string;
  description: string;
  symbolBearing: boolean;
  demand?: DemandProfile;
  // This panel's body portals its own controls into PanelFrame's ledger-header slot
  // (see headerSlot.ts) instead of rendering a second control strip in its body.
  // PanelFrame uses this to open the slot and to suppress its own title text (the
  // panel's controls already make its identity obvious without a "Chart" label).
  headerControls?: boolean;
  // This panel's body portals a single small action (e.g. a settings gear) into
  // PanelFrame's ledger-header actions slot, immediately left of the close button
  // (see headerSlot.ts's PanelHeaderActionsSlotContext). Unlike headerControls,
  // this does NOT suppress the panel's title — it's for one icon, not a toolbar.
  headerActions?: boolean;
}

// Plan 1 registered the two stack-proving panels; Plan 2 added the chart panel;
// Plan 3 added the L2 ladder + time & sales; Plan 4 added scanner / movers / news;
// Plan 5 adds the execution surfaces (account-bar / positions / open-orders /
// order-ticket). Plan 6 owns Playwright smoke E2E + ui/dist static serving.
export const PANELS: Record<string, PanelDef> = {
  "connection-status": {
    component: ({ stores }) => <ConnectionStatusPanel health={stores.health} />,
    topics: ["sys.health", "sys.events", "sys.session", "sys.boot"],
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
    topics: ["md.bars", "md.indicator", "exec.fills"],
    title: "Chart",
    glyph: "▁▃▅▇",
    description: "Candles, volume, indicators",
    symbolBearing: true,
    demand: "watch",
    headerControls: true,
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
    headerActions: true,
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
  "watchlist": {
    component: WatchlistPanel,
    topics: ["watchlist.rows"],
    title: "Watchlist",
    glyph: "★",
    description: "Your pinned symbols, quote snapshots",
    symbolBearing: false,
  },
  "news": {
    component: StockInfoPanel,
    topics: ["news.item", "stock.detail"],
    title: "Stock Info",
    glyph: "¶",
    description: "Fundamentals + news for focused symbol",
    symbolBearing: true,
    demand: "interest",
  },
  "account": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.orders", "exec.trades", "exec.status", "md.quote"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, positions, arm",
    symbolBearing: false,
    headerActions: true,
  },
  // Back-compat aliases: a saved workspace doc referencing the pre-Task-19 ids
  // (account-bar, positions) or the pre-Task-8 standalone open-orders id still
  // resolves to the merged panel (a doc with a lone "positions" or "open-orders"
  // panel now renders the full Account surface — acceptable per the Task 19 plan
  // and extended by Task 8 when open-orders folded into it). All four entries
  // below share the identical topics array so orders/trades data reaches the
  // merged panel regardless of which legacy id a saved doc references.
  "account-bar": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.orders", "exec.trades", "exec.status", "md.quote"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, positions, arm",
    symbolBearing: false,
    headerActions: true,
  },
  "positions": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.orders", "exec.trades", "exec.status", "md.quote"],
    title: "Account",
    glyph: "Σ",
    description: "Equity, BP, day P&L, positions, arm",
    symbolBearing: false,
    headerActions: true,
  },
  "open-orders": {
    component: AccountPanel,
    topics: ["exec.account", "exec.positions", "exec.orders", "exec.trades", "exec.status", "md.quote"],
    title: "Open Orders",
    glyph: "◷",
    description: "Lifecycle, cancel, cancel-all",
    symbolBearing: false,
    headerActions: true,
  },
  "order-ticket": {
    component: OrderTicketPanel,
    topics: ["md.quote", "exec.account", "exec.positions", "exec.status"],
    title: "Order Ticket",
    glyph: "$",
    description: "Compact entry, presets, sizing",
    symbolBearing: false,
    headerActions: true,
  },
};

export const DEV_PANELS = new Set(["smoke-painter"]);
export const isDevPanel = (panelId: string): boolean => DEV_PANELS.has(panelId);

const CATALOG_ORDER = ["chart", "ladder", "tape", "scanner", "movers", "watchlist", "news",
  "account", "order-ticket", "connection-status"];
// "account-bar", "positions", and (as of Task 8) "open-orders" all stay
// registered in PANELS (above) as back-compat aliases for saved workspace docs,
// but are intentionally absent from the Add Panel catalog — only the merged
// "account" is offered going forward. There is deliberately no standalone
// "trade-history" catalog entry either: Trade History is a tab inside the
// merged Account panel (Task 7), not a separate dockview panel.

export const CATALOG = CATALOG_ORDER
  .filter((id) => PANELS[id])
  .map((id) => ({ panelId: id, title: PANELS[id].title, glyph: PANELS[id].glyph, description: PANELS[id].description }));
