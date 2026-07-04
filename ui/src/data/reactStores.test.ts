import { describe, it, expect } from "vitest";
import { ExecStore } from "./ExecStore";
import { ScannerStore } from "./ScannerStore";
import { NewsStore } from "./NewsStore";

describe("ExecStore", () => {
  it("replaces account on snapshot", () => {
    const s = new ExecStore();
    s.apply({ kind: "snapshot", topic: "exec.account", payload: { equity: 1000, armed: false } });
    expect(s.getSnapshot().account).toMatchObject({ equity: 1000, armed: false });
  });
});

describe("ScannerStore / NewsStore", () => {
  it("replace on snapshot and append on delta", () => {
    const sc = new ScannerStore();
    sc.apply({ kind: "snapshot", topic: "scanner.rank", payload: [{ symbol: "US.AAA" }] });
    sc.apply({ kind: "delta", topic: "scanner.rank", payload: { symbol: "US.BBB" } });
    expect(sc.getSnapshot().rows).toHaveLength(2);

    const n = new NewsStore();
    n.apply({ kind: "snapshot", topic: "news.item", payload: [] });
    n.apply({ kind: "delta", topic: "news.item", payload: { symbol: "US.AAA", headline: "x" } });
    expect(n.getSnapshot().items).toHaveLength(1);
  });
});
