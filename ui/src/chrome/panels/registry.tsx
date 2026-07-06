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
  commands: { sendCommand(name: string, args: unknown): Promise<AckMsg> };
  // Persist a panel's own settings (timeframe, indicators, …). AppShell updates the
  // workspace doc's matching panel entry and debounce-saves via WorkspaceStore.
  onConfigChange: (settings: Record<string, unknown>) => void;
}
export interface PanelDef { component: FC<PanelProps>; topics: TopicName[] }

// Plan 1 registered the two stack-proving panels; Plan 2 added the chart panel;
// Plan 3 added the L2 ladder + time & sales; Plan 4 added scanner / movers / news;
// Plan 5 adds account-bar, positions, open-orders, order-ticket below.
export const PANELS: Record<string, PanelDef> = {
  "connection-status": {
    component: ({ stores }) => <ConnectionStatusPanel health={stores.health} />,
    topics: ["sys.health", "sys.events"],
  },
  "smoke-painter": {
    component: SmokePainterPanel,
    topics: ["md.quote"],
  },
  "chart": {
    component: ChartPanel,
    topics: ["md.bars", "md.indicator"],
  },
  "ladder": {
    component: LadderPanel,
    topics: ["md.book", "md.tape", "exec.orders"],
  },
  "tape": {
    component: TapePanel,
    topics: ["md.tape"],
  },
  "scanner": {
    component: (p) => <ScannerPanel {...p} session="premarket" />,
    topics: ["scanner.rank", "scanner.hit"],
  },
  "movers": {
    component: (p) => <ScannerPanel {...p} session="rth" />,
    topics: ["scanner.rank", "scanner.hit"],
  },
  "news": {
    component: NewsPanel,
    topics: ["news.item"],
  },
  "account-bar": {
    component: AccountBarPanel,
    topics: ["exec.account", "exec.status"],
  },
  "positions": {
    component: PositionsPanel,
    topics: ["exec.positions", "md.quote"],
  },
  "open-orders": {
    component: OpenOrdersPanel,
    topics: ["exec.orders", "exec.status"],
  },
  "order-ticket": {
    component: OrderTicketPanel,
    topics: ["md.quote", "exec.account", "exec.positions", "exec.status"],
  },
};
