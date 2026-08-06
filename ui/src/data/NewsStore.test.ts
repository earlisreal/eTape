import { describe, it, expect } from "vitest";
import { NewsStore } from "./NewsStore";
import type { NewsItem, SnapshotMsg, DeltaMsg } from "../wire/contract";

const item = (id: string, symbols: string[], seen_at: string, overrides: Partial<NewsItem> = {}): NewsItem =>
  ({ id, symbols, headline: id, source: "src", url: id, seen_at, published_at: "", published_precision: "unknown", view_count: 0, type: "news", catalyst_category: "other", catalyst_score: 0, catalyst_reasons: [], ...overrides });
const snap = (payload: NewsItem[]) => ({ kind: "snapshot", topic: "news.item", payload } as SnapshotMsg);
const delta = (payload: NewsItem | NewsItem[]) => ({ kind: "delta", topic: "news.item", payload } as DeltaMsg);

describe("NewsStore", () => {
  it("snapshot dedupes IDs retaining the last item", () => {
    const s = new NewsStore(); s.apply(snap([item("a", ["US.AAPL"], "t1"), item("a", ["US.NVDA"], "t2")]));
    expect(s.itemsFor("US.AAPL")).toEqual([]); expect(s.itemsFor("US.NVDA")).toHaveLength(1);
  });
  it("delta adds and replaces an ID, including expanded symbols", () => {
    const s = new NewsStore(); s.apply(snap([item("a", ["US.AAPL"], "t1")])); s.apply(delta(item("b", ["US.AAPL"], "t2"))); s.apply(delta(item("a", ["US.AAPL", "US.NVDA"], "t3")));
    expect(s.itemsFor("US.AAPL").map((x) => x.id)).toEqual(["a", "b"]); expect(s.itemsFor("US.NVDA").map((x) => x.id)).toEqual(["a"]);
  });
  it("caps after upserts and keeps known publication time ahead of unknown", () => {
    const capped = new NewsStore(2); capped.apply(snap([item("a", ["US.AAPL"], "z"), item("b", ["US.AAPL"], "zz"), item("c", ["US.AAPL"], "a")]));
    expect(capped.itemsFor("US.AAPL").map((x) => x.id)).toEqual(["b", "c"]);
    const s = new NewsStore(); s.apply(snap([item("known", ["US.AAPL"], "a", { published_at: "2026-01-01T01:00:00Z", published_precision: "second" }), item("unknown", ["US.AAPL"], "z")]));
    expect(s.itemsFor("US.AAPL").map((x) => x.id)).toEqual(["known", "unknown"]);
  });
  it("tolerates null and malformed payloads", () => {
    const s = new NewsStore(); expect(() => s.apply({ kind: "snapshot", topic: "news.item", payload: [null] } as unknown as SnapshotMsg)).not.toThrow(); expect(s.itemsFor("US.AAPL")).toEqual([]);
  });
});
