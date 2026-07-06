// Action templates: one saved recipe, two triggers (a hotkey binding and a ticket
// preset button), edited in one settings screen and stored engine-side under the
// config key `orderConfig`. (ui-design §Order entry & hotkeys.)
import type { Side, OrderType, TIF, VenueID } from "../../wire/contract";
import type { SizingSpec } from "./sizing";
import type { PriceSource } from "./priceSource";

export interface PlaceOrderTemplate {
  kind: "place";
  id: string; label: string;
  side: Side; type: OrderType; tif: TIF;
  priceSource: PriceSource; priceOffset: number;
  sizing: SizingSpec;
  hotkey?: string;   // normalized combo, e.g. "Ctrl+1" (see hotkeys.ts)
}
export type ManagementAction = "CancelLast" | "CancelAllFocused" | "CancelAllEverything" | "KillSwitch";
export interface ManagementTemplate { kind: "manage"; id: string; label: string; action: ManagementAction; hotkey?: string }
export type ActionTemplate = PlaceOrderTemplate | ManagementTemplate;

// The whole editable order-entry config; persisted as one blob (fewer round-trips).
export interface OrderConfig { templates: ActionTemplate[]; activeVenue: VenueID }
export const ORDER_CONFIG_KEY = "orderConfig";

export const DEFAULT_TEMPLATES: ActionTemplate[] = [
  { kind: "place", id: "buy-5k", label: "Buy $5k", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "Dollar", dollar: 5000 }, hotkey: "Ctrl+1" },
  { kind: "place", id: "buy-25pct", label: "Buy 25% BP", side: "BUY", type: "LIMIT", tif: "DAY", priceSource: "Ask", priceOffset: 0, sizing: { mode: "BuyingPowerPct", pct: 25 }, hotkey: "Ctrl+2" },
  { kind: "place", id: "sell-half", label: "Sell ½", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "half" }, hotkey: "Ctrl+3" },
  { kind: "place", id: "flatten", label: "Flatten", side: "SELL", type: "LIMIT", tif: "DAY", priceSource: "Bid", priceOffset: 0, sizing: { mode: "PositionFraction", fraction: "all" }, hotkey: "Ctrl+4" },
  { kind: "manage", id: "cancel-last", label: "Cancel Last", action: "CancelLast", hotkey: "Ctrl+Backspace" },
  { kind: "manage", id: "cancel-all", label: "Cancel All (focused)", action: "CancelAllFocused", hotkey: "Ctrl+Shift+Backspace" },
  { kind: "manage", id: "kill", label: "KILL", action: "KillSwitch", hotkey: "Ctrl+Shift+K" },
];

export const DEFAULT_ORDER_CONFIG: OrderConfig = { templates: DEFAULT_TEMPLATES, activeVenue: "" };
