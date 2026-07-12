import type { PanelConfig, Workspace } from "./workspace";
import type { LinkGroup } from "./linkGroups";

// Symbol written back into a panel/group slot when reverting demo mode without
// a pre-demo snapshot to restore verbatim — never leaves a fictional demo
// symbol wedged in the real workspace doc.
const DEFAULT_SEED_SYMBOL = "US.AAPL";

// The four fixed link-group focus entries planDemoEntry rewrites, in order,
// over the sorted universe's first four symbols.
const FIXED_GROUPS: Exclude<LinkGroup, null>[] = ["green", "red", "blue", "yellow"];

export interface DemoContext {
  snapshot: Workspace | null;
  universe: string[];
}

/**
 * Deterministically rewrite `current` for demo mode:
 *  - the four fixed link groups (green/red/blue/yellow) focus the sorted
 *    universe's first four symbols (window-agnostic — every window computes
 *    the same map from the same universe), guarding a short universe by
 *    skipping any entry whose index is out of range;
 *  - the remaining universe symbols (`sorted.slice(4)`) cycle across "pinned"
 *    symbol-bearing panels — `panel.group === null` (a grouped panel resolves
 *    via the groups rewrite above, so it is skipped here to avoid double
 *    assignment) and `isSymbolBearing(panel.panelId)` — in stable panel-id
 *    order, wrapping around if there are more such panels than remaining
 *    symbols.
 *
 * Pure: never mutates `current`; returns a fresh `Workspace`.
 */
export function planDemoEntry(
  current: Workspace,
  universe: string[],
  isSymbolBearing: (panelId: string) => boolean,
): Workspace {
  const sorted = [...universe].sort();

  const groups: Partial<Record<Exclude<LinkGroup, null>, string>> = { ...current.groups };
  FIXED_GROUPS.forEach((group, i) => {
    if (i < sorted.length) groups[group] = sorted[i];
  });

  const remaining = sorted.slice(4);

  const pinnedIds = current.panels
    .filter((p) => p.group === null && isSymbolBearing(p.panelId))
    .map((p) => p.id)
    .sort();
  const indexById = new Map<string, number>(pinnedIds.map((id, i) => [id, i]));

  const panels: PanelConfig[] = current.panels.map((p) => {
    const idx = indexById.get(p.id);
    if (idx === undefined || remaining.length === 0) {
      return { ...p, settings: { ...p.settings } };
    }
    const symbol = remaining[idx % remaining.length];
    return { ...p, settings: { ...p.settings, symbol } };
  });

  return { ...current, groups, panels };
}

/**
 * Undo demo mode: if a pre-demo snapshot was captured, restore it verbatim
 * (exact pre-demo doc, no re-derivation). Otherwise, fall back to patching
 * `current`: any panel symbol or group focus that is a member of the demo
 * universe gets reset to the default seed symbol, leaving everything else
 * (real, non-demo state) untouched.
 *
 * Pure: never mutates `ctx.snapshot` or `current`.
 */
export function planDemoRevert(ctx: DemoContext, current: Workspace): Workspace {
  if (ctx.snapshot !== null) return ctx.snapshot;

  const universe = new Set(ctx.universe);
  const next = structuredClone(current);

  next.panels = next.panels.map((p) => {
    const symbol = p.settings.symbol;
    if (typeof symbol === "string" && universe.has(symbol)) {
      return { ...p, settings: { ...p.settings, symbol: DEFAULT_SEED_SYMBOL } };
    }
    return p;
  });

  if (next.groups) {
    const groups: Partial<Record<Exclude<LinkGroup, null>, string>> = { ...next.groups };
    for (const [group, symbol] of Object.entries(groups) as [Exclude<LinkGroup, null>, string][]) {
      if (universe.has(symbol)) groups[group] = DEFAULT_SEED_SYMBOL;
    }
    next.groups = groups;
  }

  return next;
}
