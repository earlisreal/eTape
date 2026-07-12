// Settings export/import: lets a user save their panel layout and/or order-
// template hotkeys as a single JSON file, and restore either (or both) later
// — same machine or a fresh one. Pure module, no React/DOM, so every rule
// here (envelope shape, id regeneration, activeVenue scrubbing) is directly
// unit-testable; BackupSection.tsx (Task 2) is a thin UI shell around this.
import type { Workspace } from "./workspace";
import { normalizeOrderConfig, type ActionTemplate, type OrderConfig } from "./exec/actionTemplate";

export const SETTINGS_EXPORT_VERSION = 1;

// `hotkeys` deliberately carries only `templates`, never `activeVenue` — the
// active venue is which broker *this* install currently has selected, not
// something that should follow a hotkey set to another machine (or come
// back on import and silently switch venues here). See plan's Global
// Constraints.
export interface SettingsExport {
  app: "eTape";
  kind: "settings-export";
  version: number;
  exportedAt: string;
  layout?: Workspace;
  hotkeys?: { templates: ActionTemplate[] };
}

export function buildExport(
  sel: { layout: boolean; hotkeys: boolean },
  src: { workspace: Workspace; orderConfig: OrderConfig },
): SettingsExport {
  const out: SettingsExport = {
    app: "eTape",
    kind: "settings-export",
    version: SETTINGS_EXPORT_VERSION,
    exportedAt: new Date().toISOString(),
  };
  if (sel.layout) out.layout = src.workspace;
  if (sel.hotkeys) out.hotkeys = { templates: src.orderConfig.templates };
  return out;
}

// Never throws: JSON.parse is guarded, and every shape check below is a
// plain comparison. Callers (the settings UI) render `error` directly, so it
// always describes what was wrong, not just "invalid".
export function parseImport(text: string): { ok: true; data: SettingsExport } | { ok: false; error: string } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { ok: false, error: "That file isn't valid JSON." };
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { ok: false, error: "That file isn't a settings export (expected a JSON object)." };
  }
  const obj = parsed as Record<string, unknown>;
  if (obj.kind !== "settings-export") {
    return { ok: false, error: `That file isn't a settings export (kind was ${JSON.stringify(obj.kind ?? null)}).` };
  }
  if (obj.app !== "eTape") {
    return { ok: false, error: `That file wasn't exported from eTape (app was ${JSON.stringify(obj.app ?? null)}).` };
  }
  return { ok: true, data: obj as unknown as SettingsExport };
}

// The imported doc's own `name` is whatever workspace it happened to be
// exported from — importing must always land under the *current* workspace
// (so `?workspace=foo` keeps saving as `workspace.foo`), never rename it.
// Also reconciles the imported workspace against its own grid: if the file's
// `panels` exceed what's actually placed in `layout`, the extras are dropped,
// so already-ghosted exports are healed on import.
export function prepareImportedWorkspace(imported: Workspace, currentName: string): Workspace {
  return reconcileToGrid({ ...imported, name: currentName }, imported.layout);
}

// Collect every panel id referenced by dockview's serialized grid. `layout` is
// the SerializedDockview object stored in Workspace.layout ({ grid, panels, ... }).
// Authoritative "what will render" = the grid tree's leaf `data.views` — `layout.panels`
// (a separate id->metadata map) is NOT checked here; a leaf's views list is the only
// thing that actually places a panel on screen. Defensive at every step so a
// hand-edited/truncated file can't throw.
export function collectPanelIds(layout: unknown): Set<string> {
  const ids = new Set<string>();
  const grid = (layout as { grid?: { root?: unknown } } | null)?.grid;
  const walk = (node: unknown): void => {
    if (!node || typeof node !== "object") return;
    const n = node as { type?: string; data?: unknown };
    if (n.type === "leaf") {
      const views = (n.data as { views?: unknown })?.views;
      if (Array.isArray(views)) for (const v of views) if (typeof v === "string") ids.add(v);
    } else if (n.type === "branch" && Array.isArray(n.data)) {
      for (const child of n.data) walk(child);
    }
  };
  walk(grid?.root);
  return ids;
}

// Pin `base`'s panel list to exactly what `layout`'s grid actually displays — the
// single reconciliation point used by both export (against the LIVE dockview grid)
// and import (against the FILE's grid), so a panel with no grid placement ("ghost")
// never survives either path. No-op if `layout` isn't a real grid (nothing to
// reconcile against; don't destroy `base.panels` on an absent/malformed layout).
export function reconcileToGrid(base: Workspace, layout: unknown): Workspace {
  if (!isPresentLayout(layout as SettingsExport["layout"])) return base;
  // Only reconcile if there's actually a grid — without one, there's nothing
  // to reconcile against, so return base unchanged.
  const hasGrid = typeof (layout as Record<string, unknown>).grid === "object"
    && (layout as Record<string, unknown>).grid !== null;
  if (!hasGrid) return base;
  const ids = collectPanelIds(layout);
  return { ...base, layout, panels: base.panels.filter((p) => ids.has(p.id)) };
}

// parseImport only validates the top-level envelope (kind/app) — it does NOT
// check that `layout` has the right inner shape. A hand-edited or partially-
// truncated file can carry a `layout` that isn't an object; that must be
// treated as "no layout present" rather than crashing a caller's downstream
// spread/`.map` calls. Exported (moved from BackupSection.tsx) so both the
// Settings import path and the empty-workspace import path share one guard.
export function isPresentLayout(layout: SettingsExport["layout"]): layout is Workspace {
  return typeof layout === "object" && layout !== null && !Array.isArray(layout);
}

// Every imported template gets a freshly minted id. Regenerating (rather
// than keeping the exported id) matters because imported templates land
// alongside whatever already exists on this machine — OrderSettingsSection's
// own `uid()` is length-based and would collide once these are appended to
// the current array. crypto.randomUUID() is already how the rest of the app
// mints ids (AppShell panel ids, chart drawing ids, venue credential ids),
// so reuse it here instead of inventing another scheme. `activeVenue` always
// comes from the running app, never the imported file (it's machine-
// specific). normalizeOrderConfig is the single migration point for
// templates entering the app from anywhere, so imported ones go through it
// too.
export function prepareImportedOrderConfig(
  imported: { templates: ActionTemplate[] },
  current: OrderConfig,
): OrderConfig {
  const templates = imported.templates.map((t) => ({ ...t, id: crypto.randomUUID() }));
  return normalizeOrderConfig({ templates, activeVenue: current.activeVenue });
}

// Mirrors the dupes/isDup/hasConflict shape in OrderSettingsSection.tsx —
// same "which hotkey combos are claimed more than once" question, asked here
// against an about-to-be-imported template set instead of the live editor's.
export function detectHotkeyConflicts(templates: ActionTemplate[]): string[] {
  const combos = templates.map((t) => t.hotkey ?? "").filter((c) => c !== "");
  return [...new Set(combos.filter((c, i) => combos.indexOf(c) !== i))];
}
