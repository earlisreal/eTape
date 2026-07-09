import { describe, it, expect } from "vitest";
import { NewsStore } from "./NewsStore";
import type { NewsItem, SnapshotMsg, DeltaMsg } from "../wire/contract";

const item = (symbol: string, url: string, seen_at: string, headline = "h", published_at = ""): NewsItem =>
  ({ symbol, headline, source: "src", url, seen_at, published_at, view_count: 0, type: "news" });
const snap = (payload: NewsItem[]) => ({ kind: "snapshot", topic: "news.item", payload } as SnapshotMsg);
const delta = (payload: NewsItem | NewsItem[]) => ({ kind: "delta", topic: "news.item", payload } as DeltaMsg);

describe("NewsStore", () => {
  it("snapshot replaces and dedupes by url", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t2"), item("US.AAPL", "u1", "t2")])); // dup url
    expect(s.itemsFor("US.AAPL")).toHaveLength(1);
  });

  it("delta appends a single item or an array, skipping already-seen urls", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1")]));
    s.apply(delta(item("US.AAPL", "u2", "t2")));
    s.apply(delta([item("US.AAPL", "u2", "t2"), item("US.AAPL", "u3", "t3")])); // u2 dup
    expect(s.itemsFor("US.AAPL").map((i) => i.url)).toEqual(["u3", "u2", "u1"]); // newest seen_at first
  });

  it("sorts by the effective timestamp, preferring published_at over seen_at when they differ", () => {
    const s = new NewsStore();
    s.apply(snap([
      // u1's seen_at (engine fetch time) is later than u2's, but u1's real
      // published_at is earlier — the sort must prefer published_at and put
      // u2 first once both effective timestamps are compared.
      item("US.AAPL", "u1", "t9-seen-late", "h1", "t1-published-early"),
      item("US.AAPL", "u2", "t2-seen", "h2", "t5-published-late"),
    ]));
    expect(s.itemsFor("US.AAPL").map((i) => i.url)).toEqual(["u2", "u1"]);
  });

  it("itemsFor filters by symbol", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1"), item("US.NVDA", "n1", "t2")]));
    expect(s.itemsFor("US.NVDA").map((i) => i.url)).toEqual(["n1"]);
    expect(s.itemsFor("US.TSLA")).toEqual([]);
  });

  it("a reconnect snapshot rebuilds rather than doubling", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1")]));
    s.apply(delta(item("US.AAPL", "u2", "t2")));
    s.apply(snap([item("US.AAPL", "u1", "t1")])); // reconnect re-snapshot
    expect(s.itemsFor("US.AAPL").map((i) => i.url)).toEqual(["u1"]);
  });

  it("tolerates a null payload without throwing (empty snapshot serializes to JSON null)", () => {
    const s = new NewsStore();
    // The engine sends `payload: null` for an empty news snapshot (a nil Go slice
    // marshals to null). apply() must treat it as "no items", not crash in keyOf.
    expect(() => s.apply({ kind: "snapshot", topic: "news.item", payload: null } as unknown as SnapshotMsg)).not.toThrow();
    expect(() => s.apply({ kind: "delta", topic: "news.item", payload: null } as unknown as DeltaMsg)).not.toThrow();
    expect(s.itemsFor("US.AAPL")).toEqual([]);
  });

  it("skips null / non-object entries inside a payload array", () => {
    const s = new NewsStore();
    s.apply(snap([item("US.AAPL", "u1", "t1"), null as unknown as NewsItem, item("US.AAPL", "u2", "t2")]));
    expect(s.itemsFor("US.AAPL").map((i) => i.url)).toEqual(["u2", "u1"]);
  });
});
