import { describe, it, expect } from "vitest";
import { toggleSort, sortRows, sortIndicator, type SortState } from "./sortColumns";

const rows = [{ s: "B", n: 2 }, { s: "A", n: null }, { s: "C", n: 1 }];
const acc = { sym: (r: typeof rows[number]) => r.s, val: (r: typeof rows[number]) => r.n };

describe("toggleSort", () => {
  it("unset → desc → asc → desc on same col", () => {
    let st: SortState = null;
    st = toggleSort(st, "val"); expect(st).toEqual({ col: "val", dir: "desc" });
    st = toggleSort(st, "val"); expect(st).toEqual({ col: "val", dir: "asc" });
    st = toggleSort(st, "val"); expect(st).toEqual({ col: "val", dir: "desc" });
  });
  it("new col starts at desc", () => expect(toggleSort({ col: "val", dir: "asc" }, "sym")).toEqual({ col: "sym", dir: "desc" }));
});

describe("sortRows", () => {
  it("nulls sort last in both directions; stable otherwise", () => {
    const desc = sortRows(rows, { col: "val", dir: "desc" }, acc).map((r) => r.s);
    expect(desc).toEqual(["B", "C", "A"]); // 2,1, then null
    const asc = sortRows(rows, { col: "val", dir: "asc" }, acc).map((r) => r.s);
    expect(asc).toEqual(["C", "B", "A"]); // 1,2, then null
  });
  it("null state returns input order (copy)", () => {
    const out = sortRows(rows, null, acc);
    expect(out).toEqual(rows); expect(out).not.toBe(rows);
  });
});

describe("sortIndicator", () => {
  it("marks only the active column", () => {
    expect(sortIndicator({ col: "val", dir: "asc" }, "val")).toBe("▴");
    expect(sortIndicator({ col: "val", dir: "desc" }, "val")).toBe("▾");
    expect(sortIndicator({ col: "val", dir: "desc" }, "sym")).toBe("");
  });
});
