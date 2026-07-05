import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const fx = JSON.parse(readFileSync(join(here, "monitoring.json"), "utf8")) as {
  snapshots: Array<{ topic: string; key?: string; payload: unknown }>;
  deltas: Array<{ afterMs: number; topic: string; key?: string; payload: unknown }>;
};

const isNum = (v: unknown) => typeof v === "number";
const isNumOrNull = (v: unknown) => v === null || typeof v === "number";

describe("monitoring fixture conforms to the contract", () => {
  const all = [...fx.snapshots, ...fx.deltas];

  it("has both scanner sessions, a scanner.hit, and news", () => {
    const keys = new Set(all.filter((e) => e.topic === "scanner.rank").map((e) => e.key));
    expect(keys.has("premarket")).toBe(true);
    expect(keys.has("rth")).toBe(true);
    expect(all.some((e) => e.topic === "scanner.hit")).toBe(true);
    expect(all.some((e) => e.topic === "news.item")).toBe(true);
  });

  it("scanner.rank rows are typed correctly (nullable change/last/float, numeric volume)", () => {
    for (const e of all.filter((x) => x.topic === "scanner.rank")) {
      const p = e.payload as { refreshedAt: string; rows: Record<string, unknown>[] };
      expect(typeof p.refreshedAt).toBe("string");
      for (const row of p.rows) {
        expect(typeof row.symbol).toBe("string");
        expect(isNumOrNull(row.changePct)).toBe(true);
        expect(isNumOrNull(row.last)).toBe(true);
        expect(isNumOrNull(row.floatShares)).toBe(true);
        expect(isNum(row.volume)).toBe(true);
      }
    }
  });

  it("news items carry all five string fields", () => {
    for (const e of all.filter((x) => x.topic === "news.item")) {
      const items = Array.isArray(e.payload) ? e.payload : [e.payload];
      for (const it of items as Record<string, unknown>[]) {
        for (const f of ["symbol", "headline", "source", "url", "seen_at"]) expect(typeof it[f]).toBe("string");
      }
    }
  });

  it("scanner.hit carries string symbol + at", () => {
    for (const e of all.filter((x) => x.topic === "scanner.hit")) {
      const p = e.payload as Record<string, unknown>;
      expect(typeof p.symbol).toBe("string");
      expect(typeof p.at).toBe("string");
    }
  });
});
