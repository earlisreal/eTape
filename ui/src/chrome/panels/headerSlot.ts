import { createContext } from "react";

// PanelFrame renders the per-panel "ledger header" row above every panel body. Some
// panel bodies (currently only ChartPanel) own controls that belong visually in that
// row (timeframe, indicators, screenshot, settings) but are stateful inside the body,
// not the frame. Rather than teaching PanelFrame the chart's control set, the frame
// exposes a DOM slot inside its own header via this context; the body portals its
// controls into it.
//
// Three states, not two:
//  - undefined: no provider above (e.g. a body-level test rendering ChartPanel
//    directly, without PanelFrame) — the body should render its controls inline.
//  - null: a provider is present but the slot node hasn't mounted yet (first paint,
//    before the ref callback runs) — render nothing for this one tick; the slot fills
//    in on the next render once the ref fires. Avoids a flash of inline-then-ported.
//  - HTMLElement: the live portal target inside PanelFrame's ledger header.
export const PanelHeaderSlotContext = createContext<HTMLElement | null | undefined>(undefined);
