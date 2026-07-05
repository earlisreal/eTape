import type { FC } from "react";
import type { TopicName } from "../../wire/contract";
import type { PanelConfig } from "../workspace";
import type { Stores } from "../../data/registry";
import type { Scheduler } from "../../render/Scheduler";
import type { LinkGroups } from "../linkGroups";
import { ConnectionStatusPanel } from "./ConnectionStatusPanel";
import { SmokePainterPanel } from "./SmokePainterPanel";
import { ChartPanel } from "./ChartPanel";
import { LadderPanel } from "./LadderPanel";
import { TapePanel } from "./TapePanel";

export interface PanelProps {
  config: PanelConfig;
  stores: Stores;
  scheduler: Scheduler;
  width: number;
  height: number;
  linkGroups: LinkGroups;
  commands: { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> };
  // Persist a panel's own settings (timeframe, indicators, …). AppShell updates the
  // workspace doc's matching panel entry and debounce-saves via WorkspaceStore.
  onConfigChange: (settings: Record<string, unknown>) => void;
}
export interface PanelDef { component: FC<PanelProps>; topics: TopicName[] }

// Plan 1 registered the two panels needed to prove the stack; Plan 2 adds the
// real chart panel. Plans 3–5 still owe ladder / tape / scanner / movers / news /
// account-bar / positions / open-orders / order-ticket here.
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
};
