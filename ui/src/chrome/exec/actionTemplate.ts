// Action templates: one saved recipe, two triggers (a hotkey binding and a ticket
// preset button), edited in one settings screen and stored engine-side under the
// config key `orderConfig`. (ui-design §Order entry & hotkeys.)
import type { Side, OrderType, TIF, OrderSession, VenueID } from "../../wire/contract";
import type { SizingSpec } from "./sizing";
import type { PriceSource, PriceOffsetUnit } from "./priceSource";

export type DeckColor = "auto" | "green" | "red" | "bronze" | "neutral" | "danger";
export type ChartBinding = "Ctrl" | "Alt" | "Shift" | "Ctrl+Alt" | "Ctrl+Shift" | "Alt+Shift";
export const CHART_BINDINGS: ChartBinding[] = ["Ctrl", "Alt", "Shift", "Ctrl+Alt", "Ctrl+Shift", "Alt+Shift"];

export interface HotkeyDeckConfig {
  rows: string[][];
  showHotkeyLabels: boolean;
}

export interface PlaceOrderTemplate {
  kind: "place";
  id: string; label: string;
  side: Side; type: OrderType; tif: TIF;
  session?: OrderSession;   // absent => "AUTO" (every persisted config is already valid)
  priceSource: PriceSource; priceOffset: number;
  priceOffsetUnit?: PriceOffsetUnit;   // absent => "$" (every persisted config is already valid)
  limitCushion?: number;               // STOP_LIMIT/LIT; absent => 0
  limitCushionUnit?: PriceOffsetUnit;  // STOP_LIMIT/LIT; absent => "$"
  chartBinding?: ChartBinding;         // conditional orders only; unique exact modifier+click binding
  sizing: SizingSpec;
  hotkey?: string;   // normalized combo, e.g. "Ctrl+1" (see hotkeys.ts)
  deck?: boolean;   // absent => hotkey-only, not shown in deck
  deckColor?: DeckColor;   // absent => "auto"
}
export type ManagementAction = "CancelLast" | "CancelAllFocused" | "CancelAllEverything" | "KillSwitch";
export interface ManagementTemplate { kind: "manage"; id: string; label: string; action: ManagementAction; hotkey?: string; deck?: boolean; deckColor?: DeckColor }
export type ActionTemplate = PlaceOrderTemplate | ManagementTemplate;

// The whole editable order-entry config; persisted as one blob (fewer round-trips).
export interface OrderConfig {
  templates: ActionTemplate[];
  activeVenue: VenueID;
  extHoursMarketBufferPct?: number;   // absent => 1.0; clamped [0.1, 10] in normalizeOrderConfig
  autoUnlockOnStartup?: boolean;   // absent => false (trading boots locked, the safe default)
  hotkeyDeck?: HotkeyDeckConfig;   // absent at the type boundary for legacy configs
}
export const ORDER_CONFIG_KEY = "orderConfig";

// Shared constants for ext-hours market buffer percentage editing, used by
// normalizeOrderConfig clamping logic and by UI components (OrderSettingsSection, etc.)
export const EXT_BUFFER_MIN = 0.1;
export const EXT_BUFFER_MAX = 10;
export const EXT_BUFFER_STEP = 0.1;

// Intentionally empty: eTape ships with NO default order templates or hotkeys.
// A fresh install (engine has no stored `orderConfig`) starts blank; the user
// builds templates/hotkeys in Settings → Orders & hotkeys. Do not re-seed.
export const DEFAULT_TEMPLATES: ActionTemplate[] = [];

export const DEFAULT_ORDER_CONFIG: OrderConfig = { templates: DEFAULT_TEMPLATES, activeVenue: "" };

// normalizeOrderConfig is the single migration point applied where a config
// enters the app (OrderConfigProvider on load, and to DEFAULT_ORDER_CONFIG).
// It converts legacy PositionFraction `fraction` to `pct`, defaults a missing
// price-offset unit to "$", defaults a missing session to "AUTO" (a
// config saved before this feature landed keeps today's clock-inferred
// submit behavior), and defaults a missing `extHoursMarketBufferPct` to 1.0
// (clamped [0.1, 10]). Idempotent; manage templates pass through.
function normalizeTemplate(t: ActionTemplate): ActionTemplate {
  if (t.kind !== "place") return t;
  let sizing = t.sizing;
  if (sizing.mode === "PositionFraction" && sizing.pct === undefined) {
    sizing = { ...sizing, pct: sizing.fraction === "half" ? 50 : 100 };
  }
  const base = { ...t };
  delete base.chartBinding;
  delete base.limitCushion;
  delete base.limitCushionUnit;
  if (t.type !== "STOP_LIMIT" && t.type !== "LIMIT_IF_TOUCHED") return { ...base, priceOffsetUnit: t.priceOffsetUnit ?? "$", session: t.session ?? "AUTO", sizing };
  const cushion = Number.isFinite(t.limitCushion) ? Math.max(0, t.limitCushion ?? 0) : 0;
  const binding = normalizeChartBinding(t.chartBinding);
  return {
    ...base,
    priceOffsetUnit: t.priceOffsetUnit ?? "$", session: t.session ?? "AUTO", sizing,
    limitCushion: cushion, limitCushionUnit: t.limitCushionUnit === "%" ? "%" : "$",
    ...(binding ? { chartBinding: binding } : {}),
  };
}

export function normalizeChartBinding(value: unknown): ChartBinding | undefined {
  if (typeof value !== "string") return undefined;
  const modifiers = value.split("+");
  if (modifiers.length < 1 || modifiers.length > 2 || modifiers.some((modifier) => !["Ctrl", "Alt", "Shift"].includes(modifier))) return undefined;
  if (new Set(modifiers).size !== modifiers.length) return undefined;
  const canonical = ["Ctrl", "Alt", "Shift"].filter((modifier) => modifiers.includes(modifier)).join("+");
  return CHART_BINDINGS.includes(canonical as ChartBinding) ? canonical as ChartBinding : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function normalizeHotkeyDeck(raw: unknown, templates: ActionTemplate[]): HotkeyDeckConfig {
  const explicit = raw !== undefined;
  const source = isRecord(raw) && Array.isArray(raw.rows) ? raw.rows : !explicit
    ? [templates.filter((t) => t.deck).map((t) => t.id)]
    : [];
  const templateIds = new Set(templates.map((t) => t.id));
  const placed = new Set<string>();
  const rows: string[][] = [];

  for (const row of source) {
    if (!Array.isArray(row)) continue;
    const next: string[] = [];
    for (const id of row) {
      if (typeof id !== "string" || !templateIds.has(id) || placed.has(id)) continue;
      placed.add(id);
      next.push(id);
    }
    if (next.length > 0) rows.push(next);
  }

  return { rows, showHotkeyLabels: isRecord(raw) && raw.showHotkeyLabels === true };
}

export function normalizeOrderConfig(config: OrderConfig): OrderConfig {
  const raw = config.extHoursMarketBufferPct;
  const extHoursMarketBufferPct = raw === undefined || Number.isNaN(raw) ? 1.0 : Math.min(EXT_BUFFER_MAX, Math.max(EXT_BUFFER_MIN, raw));
  const normalized = config.templates.map(normalizeTemplate);
  const usedChartBindings = new Set<ChartBinding>();
  const templates = normalized.map((template) => {
    if (template.kind !== "place" || !template.chartBinding) return template;
    if (usedChartBindings.has(template.chartBinding)) {
      const withoutBinding = { ...template };
      delete withoutBinding.chartBinding;
      return withoutBinding;
    }
    usedChartBindings.add(template.chartBinding);
    return template;
  });
  const hotkeyDeck = normalizeHotkeyDeck((config as { hotkeyDeck?: unknown }).hotkeyDeck, templates);
  const placed = new Set(hotkeyDeck.rows.flat());
  return {
    ...config,
    extHoursMarketBufferPct,
    templates: templates.map((t) => ({ ...t, deck: placed.has(t.id) })),
    hotkeyDeck,
  };
}
