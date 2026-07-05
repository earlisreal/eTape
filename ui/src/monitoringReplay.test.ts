import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { makeStores, routeToStore } from "./data/registry";
import type { SnapshotMsg, DeltaMsg } from "./wire/contract";

const here = dirname(fileURLToPath(import.meta.url));
const fx = JSON.parse(readFileSync(join(here, "..", "fixtures", "monitoring.json"), "utf8")) as {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ topic: string; key?: string; payload: unknown }>;
};

function replay() {
  const stores = makeStores();
  const asMsg = (kind: "snapshot" | "delta", e: { topic: string; key?: string; payload: unknown }) =>
    ({ kind, topic: e.topic, key: e.key, payload: e.payload } as SnapshotMsg | DeltaMsg);
  for (const s of fx.snapshots) routeToStore(stores, asMsg("snapshot", s));
  for (const d of fx.deltas) routeToStore(stores, asMsg("delta", d));
  return stores;
}

describe("monitoring replay invariant", () => {
  it("scanner premarket: newcomer flashes, carried-over rows mute, scanner.hit re-flashes", () => {
    const v = replay().scanner.view("premarket");
    const byId = Object.fromEntries(v.rows.map((r) => [r.symbol, r]));
    expect(v.refreshedAt).toBe("2026-07-06T08:30:02Z");
    expect(byId["US.GHIJ"].isNewHit).toBe(true);  // introduced in the delta
    expect(byId["US.KO"].isNewHit).toBe(true);     // forced by scanner.hit
    expect(byId["US.DJT"].isNewHit).toBe(false);
    expect(byId["US.DJT"].muted).toBe(true);
    expect(byId["US.WXYZ"]).toBeUndefined();       // fell off the ranking
  });

  it("scanner rth is isolated: its delta newcomer flashes", () => {
    const byId = Object.fromEntries(replay().scanner.view("rth").rows.map((r) => [r.symbol, r]));
    expect(byId["US.AMD"].isNewHit).toBe(true);
    expect(byId["US.NVDA"].muted).toBe(true);
  });

  it("news: itemsFor(AAPL) is deduped and newest-first by seen_at", () => {
    const urls = replay().news.itemsFor("US.AAPL").map((i) => i.url);
    expect(urls).toEqual(["https://ex.com/a3", "https://ex.com/a1", "https://ex.com/a2"]);
  });
});
