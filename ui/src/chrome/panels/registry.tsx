import type { FC } from "react";
import type { TopicName } from "../../wire/contract";
import type { PanelConfig } from "../workspace";
import type { Stores } from "../../data/registry";
import type { Scheduler } from "../../render/Scheduler";
import { ConnectionStatusPanel } from "./ConnectionStatusPanel";
import { SmokePainterPanel } from "./SmokePainterPanel";

export interface PanelProps {
  config: PanelConfig;
  stores: Stores;
  scheduler: Scheduler;
  width: number;
  height: number;
}
export interface PanelDef { component: FC<PanelProps>; topics: TopicName[] }

// Plan 1 registers the two panels needed to prove the stack. Plans 2–5 register
// chart / ladder / tape / scanner / movers / news / account-bar / positions /
// open-orders / order-ticket here.
export const PANELS: Record<string, PanelDef> = {
  "connection-status": {
    component: ({ stores }) => <ConnectionStatusPanel health={stores.health} />,
    topics: ["sys.health", "sys.events"],
  },
  "smoke-painter": {
    component: SmokePainterPanel,
    topics: ["md.quote"],
  },
};
