import type { ScannerFilters } from "../wire/contract";
import type { ScannerRowView } from "../data/ScannerStore";
import { sortRows, type SortState } from "./sortColumns";

export type ScannerSyncStatusKind = "disabled" | "paused" | "incomplete" | "following";

export interface ScannerSyncStatus {
  kind: ScannerSyncStatusKind;
  availableCount: number;
  targetCount: number;
  reason?: "source" | "targets" | "rows";
}

export interface ScannerSyncSlot {
  id: string;
  symbol?: string | undefined;
}

export interface ScannerSyncPlan {
  patches: Array<{ slotId: string; symbol: string }>;
  status: ScannerSyncStatus;
}

export interface ScannerSyncPlanInput {
  slots: readonly ScannerSyncSlot[];
  rankedSymbols: readonly string[];
  enabled: boolean;
  sourceAvailable: boolean;
}

export const DEFAULT_SCANNER_SORT: SortState = { col: "changePct", dir: "desc" };

export const scannerSortAccessors: Record<string, (row: ScannerRowView) => number | string | null> = {
  sym: (row) => row.symbol,
  changePct: (row) => row.changePct,
  last: (row) => row.last,
  float: (row) => row.floatShares,
  vol: (row) => row.volume,
};

export function scannerModeSort(mode: ScannerFilters["mode"]): SortState {
  return mode === "most_active"
    ? { col: "vol", dir: "desc" }
    : { col: "changePct", dir: mode === "losers" ? "asc" : "desc" };
}

export function readScannerSort(settings: Record<string, unknown>): SortState {
  const raw = settings.sort as { col?: unknown; dir?: unknown } | undefined;
  return raw && typeof raw.col === "string" && (raw.dir === "asc" || raw.dir === "desc")
    ? { col: raw.col, dir: raw.dir }
    : DEFAULT_SCANNER_SORT;
}

export function rankScannerRows(rows: readonly ScannerRowView[], sort: SortState): ScannerRowView[] {
  return sortRows([...rows], sort, scannerSortAccessors);
}

export function scannerSyncStatusText(status: ScannerSyncStatus): string {
  if (status.kind === "disabled") return "Sync off";
  if (status.kind === "following" || status.kind === "incomplete") {
    return `Following ${status.availableCount}/${status.targetCount}`;
  }
  if (status.reason === "targets") return "Paused — add a pinned Chart Panel";
  if (status.reason === "rows") return "Paused — waiting for Scanner rows";
  return "Paused — Scanner Source unavailable";
}

function usableSymbols(symbols: readonly string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const symbol of symbols) {
    const value = symbol.trim();
    if (!value || seen.has(value)) continue;
    seen.add(value);
    result.push(value);
  }
  return result;
}

export function planScannerSync(input: ScannerSyncPlanInput): ScannerSyncPlan {
  const targetCount = input.slots.length;
  if (!input.enabled) {
    return { patches: [], status: { kind: "disabled", availableCount: 0, targetCount } };
  }
  if (!input.sourceAvailable) {
    return { patches: [], status: { kind: "paused", availableCount: 0, targetCount, reason: "source" } };
  }
  if (targetCount === 0) {
    return { patches: [], status: { kind: "paused", availableCount: 0, targetCount, reason: "targets" } };
  }

  const ranked = usableSymbols(input.rankedSymbols);
  if (ranked.length === 0) {
    return { patches: [], status: { kind: "paused", availableCount: 0, targetCount, reason: "rows" } };
  }

  const relevant = ranked.slice(0, targetCount);
  const relevantSet = new Set(relevant);
  const retained = new Set<string>();
  const open: number[] = [];
  for (let i = 0; i < input.slots.length; i++) {
    const symbol = input.slots[i].symbol?.trim();
    if (symbol && relevantSet.has(symbol)) retained.add(symbol);
    else open.push(i);
  }

  const patches: Array<{ slotId: string; symbol: string }> = [];
  for (const symbol of relevant) {
    if (retained.has(symbol)) continue;
    const index = open.shift();
    if (index === undefined) break;
    if (input.slots[index].symbol !== symbol) patches.push({ slotId: input.slots[index].id, symbol });
    retained.add(symbol);
  }

  return {
    patches,
    status: {
      kind: ranked.length < targetCount ? "incomplete" : "following",
      availableCount: Math.min(ranked.length, targetCount),
      targetCount,
    },
  };
}

export interface ScannerSyncPanelState {
  selected: boolean;
  enabled: boolean;
  status: ScannerSyncStatus;
  statusVisible?: boolean;
  onSelect: () => void;
  onToggle: () => void;
}

export class ScannerSyncRuntime {
  private states = new Map<string, ScannerSyncPanelState>();
  private readonly listeners = new Map<string, Set<() => void>>();

  get(panelId: string): ScannerSyncPanelState | undefined { return this.states.get(panelId); }

  subscribe(panelId: string, cb: () => void): () => void {
    let set = this.listeners.get(panelId);
    if (!set) { set = new Set(); this.listeners.set(panelId, set); }
    set.add(cb);
    return () => { set!.delete(cb); };
  }

  replace(next: ReadonlyMap<string, ScannerSyncPanelState>): void {
    const ids = new Set([...this.states.keys(), ...next.keys()]);
    const previous = this.states;
    this.states = new Map(next);
    for (const id of ids) {
      if (previous.get(id) !== this.states.get(id)) this.listeners.get(id)?.forEach((cb) => cb());
    }
  }
}

export class PanelSymbolRuntime {
  private readonly symbols = new Map<string, string>();
  private readonly listeners = new Map<string, Set<() => void>>();

  get(panelId: string): string | undefined { return this.symbols.get(panelId); }

  set(panelId: string, symbol: string): void {
    if (this.symbols.get(panelId) === symbol) return;
    this.symbols.set(panelId, symbol);
    this.listeners.get(panelId)?.forEach((cb) => cb());
  }

  clear(panelId: string): void {
    if (!this.symbols.delete(panelId)) return;
    this.listeners.get(panelId)?.forEach((cb) => cb());
  }

  subscribe(panelId: string, cb: () => void): () => void {
    let set = this.listeners.get(panelId);
    if (!set) { set = new Set(); this.listeners.set(panelId, set); }
    set.add(cb);
    return () => { set!.delete(cb); };
  }
}
