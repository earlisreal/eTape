import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { makeStores, routeToStore } from "./data/registry";
import type { SnapshotMsg, DeltaMsg } from "./wire/contract";

// UI twin of the engine's replay(log) == state invariant: feed the recorded
// snapshot + deltas (including the mid-stream reconnect re-snapshot) and assert
// the final store state. A reconnect re-snapshot must rebuild, not double-apply.
const here = dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(readFileSync(join(here, "..", "fixtures", "session-basic.json"), "utf8")) as {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ topic: string; key?: string; payload: unknown }>;
};

describe("store replay invariant", () => {
  it("reaches a deterministic final state from snapshot + deltas", () => {
    const stores = makeStores();
    const asMsg = (kind: "snapshot" | "delta", e: { topic: string; key?: string; payload: unknown }) =>
      ({ kind, topic: e.topic, key: e.key, payload: e.payload } as SnapshotMsg | DeltaMsg);
    for (const s of fixture.snapshots) routeToStore(stores, asMsg("snapshot", s));
    for (const d of fixture.deltas) routeToStore(stores, asMsg("delta", d));
    // last md.quote delta in the fixture is last=3.52
    expect(stores.quote.get("US.AAPL")?.last).toBe(3.52);
    // sys.events accumulated (boot snapshot + reconnect delta)
    expect(stores.health.getSnapshot().events.map((e) => e.kind)).toEqual(["boot", "reconnect"]);
  });

  it("a re-snapshot rebuilds rather than doubling", () => {
    const stores = makeStores();
    routeToStore(stores, { kind: "snapshot", topic: "md.tape", key: "US.AAPL",
      payload: [{ symbol: "US.AAPL", price: 1, size: 1, direction: "BUY", ts: "t1" }] });
    routeToStore(stores, { kind: "delta", topic: "md.tape", key: "US.AAPL",
      payload: [{ symbol: "US.AAPL", price: 2, size: 1, direction: "SELL", ts: "t2" }] });
    routeToStore(stores, { kind: "snapshot", topic: "md.tape", key: "US.AAPL",
      payload: [{ symbol: "US.AAPL", price: 9, size: 1, direction: "BUY", ts: "t9" }] });
    const src = stores.tape.source("US.AAPL");
    expect(src.lastSeq()).toBe(1);
    expect(src.tickBySeq(1)?.price).toBe(9);
  });
});
