// Action templates: one saved recipe, two triggers (a hotkey binding and a ticket
// preset button), edited in one settings screen and stored engine-side under the
// config key `orderConfig`. (ui-design §Order entry & hotkeys.)
import type { Side, OrderType, TIF, OrderSession, VenueID } from "../../wire/contract";
import type { SizingSpec } from "./sizing";
import type { PriceSource, PriceOffsetUnit } from "./priceSource";

export interface PlaceOrderTemplate {
  kind: "place";
  id: string; label: string;
  side: Side; type: OrderType; tif: TIF;
  session?: OrderSession;   // absent => "AUTO" (every persisted config is already valid)
  priceSource: PriceSource; priceOffset: number;
  priceOffsetUnit?: PriceOffsetUnit;   // absent => "$" (every persisted config is already valid)
  sizing: SizingSpec;
  hotkey?: string;   // normalized combo, e.g. "Ctrl+1" (see hotkeys.ts)
}
export type ManagementAction = "CancelLast" | "CancelAllFocused" | "CancelAllEverything" | "KillSwitch";
export interface ManagementTemplate { kind: "manage"; id: string; label: string; action: ManagementAction; hotkey?: string }
export type ActionTemplate = PlaceOrderTemplate | ManagementTemplate;

// The whole editable order-entry config; persisted as one blob (fewer round-trips).
export interface OrderConfig { templates: ActionTemplate[]; activeVenue: VenueID }
export const ORDER_CONFIG_KEY = "orderConfig";

// Intentionally empty: eTape ships with NO default order templates or hotkeys.
// A fresh install (engine has no stored `orderConfig`) starts blank; the user
// builds templates/hotkeys in Settings → Orders & hotkeys. Do not re-seed.
export const DEFAULT_TEMPLATES: ActionTemplate[] = [];

export const DEFAULT_ORDER_CONFIG: OrderConfig = { templates: DEFAULT_TEMPLATES, activeVenue: "" };

// normalizeOrderConfig is the single migration point applied where a config
// enters the app (OrderConfigProvider on load, and to DEFAULT_ORDER_CONFIG).
// It converts legacy PositionFraction `fraction` to `pct`, defaults a missing
// price-offset unit to "$", and defaults a missing session to "AUTO" (a
// config saved before this feature landed keeps today's clock-inferred
// submit behavior). Idempotent; manage templates pass through.
function normalizeTemplate(t: ActionTemplate): ActionTemplate {
  if (t.kind !== "place") return t;
  let sizing = t.sizing;
  if (sizing.mode === "PositionFraction" && sizing.pct === undefined) {
    sizing = { ...sizing, pct: sizing.fraction === "half" ? 50 : 100 };
  }
  return { ...t, priceOffsetUnit: t.priceOffsetUnit ?? "$", session: t.session ?? "AUTO", sizing };
}

export function normalizeOrderConfig(config: OrderConfig): OrderConfig {
  return { ...config, templates: config.templates.map(normalizeTemplate) };
}
