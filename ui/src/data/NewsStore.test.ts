import { describe, it, expect } from "vitest";
import { NewsStore } from "./NewsStore";
import type { NewsItem, SnapshotMsg, DeltaMsg } from "../wire/contract";

const item = (symbol: string, url: string, seen_at: string, headline = "h"): NewsItem =>
  ({ symbol, headline, source: "src", url, seen_at });
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
});
