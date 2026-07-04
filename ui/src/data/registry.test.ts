import { describe, it, expect } from "vitest";
import { makeStores, routeToStore, connectStores } from "./registry";
import type { SnapshotMsg, DeltaMsg, TopicName } from "../wire/contract";

describe("routeToStore", () => {
  it("dispatches each topic to its store", () => {
    const stores = makeStores();
    routeToStore(stores, { kind: "snapshot", topic: "md.quote", key: "US.AAPL",
      payload: { symbol: "US.AAPL", bid: 1, ask: 2, last: 1.5, ts: "t" } });
    expect(stores.quote.get("US.AAPL")?.last).toBe(1.5);

    routeToStore(stores, { kind: "snapshot", topic: "sys.health",
      payload: { links: [{ link: "ui-engine", ms: 1, min: 1, avg: 1, max: 1, status: "ok" }] } });
    expect(stores.health.getSnapshot().links).toHaveLength(1);
  });

  it("routes md.indicator to the IndicatorStore keyed by instanceId", () => {
    const stores = makeStores();
    routeToStore(stores, { kind: "snapshot", topic: "md.indicator", key: "vwap-1",
      payload: [{ timeMs: 1000, value: 10 }] });
    expect(stores.indicators.series("vwap-1")).toHaveLength(1);
  });
});

describe("connectStores", () => {
  it("subscribes requested topics and routes their messages", () => {
    const stores = makeStores();
    const handlers = new Map<string, (m: SnapshotMsg | DeltaMsg) => void>();
    const fakeClient = {
      subscribe(topic: TopicName, cb: (m: SnapshotMsg | DeltaMsg) => void) {
        handlers.set(topic, cb);
        return () => handlers.delete(topic);
      },
    };
    const dispose = connectStores(fakeClient, stores, ["md.quote", "sys.health"]);
    expect([...handlers.keys()].sort()).toEqual(["md.quote", "sys.health"]);

    handlers.get("md.quote")!({ kind: "snapshot", topic: "md.quote", key: "US.X",
      payload: { symbol: "US.X", bid: 1, ask: 2, last: 3, ts: "t" } });
    expect(stores.quote.get("US.X")?.last).toBe(3);

    dispose();
    expect(handlers.size).toBe(0);
  });
});
