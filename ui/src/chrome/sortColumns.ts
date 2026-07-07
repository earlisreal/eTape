export type SortDir = "asc" | "desc";
export type SortState = { col: string; dir: SortDir } | null;

export function toggleSort(state: SortState, col: string): SortState {
  if (!state || state.col !== col) return { col, dir: "desc" };
  return { col, dir: state.dir === "desc" ? "asc" : "desc" };
}

export function sortRows<T>(rows: T[], state: SortState, accessors: Record<string, (r: T) => number | string | null>): T[] {
  const copy = rows.slice();
  if (!state) return copy;
  const get = accessors[state.col];
  if (!get) return copy;
  const mul = state.dir === "asc" ? 1 : -1;
  return copy
    .map((r, i) => ({ r, i, v: get(r) }))
    .sort((a, b) => {
      if (a.v === null && b.v === null) return a.i - b.i;
      if (a.v === null) return 1;   // nulls last regardless of dir
      if (b.v === null) return -1;
      const c = typeof a.v === "number" && typeof b.v === "number" ? a.v - b.v : String(a.v).localeCompare(String(b.v));
      return c !== 0 ? c * mul : a.i - b.i; // stable tiebreak
    })
    .map((x) => x.r);
}

export function sortIndicator(state: SortState, col: string): "" | "▴" | "▾" {
  if (!state || state.col !== col) return "";
  return state.dir === "asc" ? "▴" : "▾";
}
