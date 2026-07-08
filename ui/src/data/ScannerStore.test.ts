import { describe, it, expect, vi } from "vitest";
import { ScannerStore } from "./ScannerStore";
import type { ScannerRankPayload, ScanHitPayload, SnapshotMsg, DeltaMsg } from "../wire/contract";

const rank = (kind: "snapshot" | "delta", session: string, payload: ScannerRankPayload) =>
  ({ kind, topic: "scanner.rank", key: session, payload } as SnapshotMsg | DeltaMsg);
const hit = (session: string, payload: ScanHitPayload) =>
  ({ kind: "delta", topic: "scanner.hit", key: session, payload } as DeltaMsg);
const r = (symbol: string, changePct: number) =>
  ({ symbol, changePct, last: 1, floatShares: 1_000_000, volume: 1000 });

describe("ScannerStore", () => {
  it("snapshot seeds the baseline without flashing", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5), r("B", 3)] }));
    const v = s.view("premarket");
    expect(v.refreshedAt).toBe("t0");
    expect(v.rows.every((row) => !row.isNewHit && !row.muted)).toBe(true);
  });

  it("delta flashes newcomers and mutes carried-over rows", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6), r("B", 9)] }));
    const byId = Object.fromEntries(s.view("premarket").rows.map((row) => [row.symbol, row]));
    expect(byId.B.isNewHit).toBe(true);
    expect(byId.A.isNewHit).toBe(false);
    expect(byId.A.muted).toBe(true);
  });

  it("a second delta no longer flashes a now-seen symbol", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6), r("B", 9)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [r("A", 6), r("B", 9)] }));
    expect(s.view("premarket").rows.find((row) => row.symbol === "B")?.isNewHit).toBe(false);
  });

  it("scanner.hit force-flashes a symbol present in the current ranking", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(hit("premarket", { symbol: "A", at: "t0.5" }));
    expect(s.view("premarket").rows.find((row) => row.symbol === "A")?.isNewHit).toBe(true);
  });

  it("resetSeen silently re-baselines; the next delta does not flash carried-over rows", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6)] }));
    s.resetSeen("premarket");
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [r("A", 6)] })); // empty seen ⇒ silent baseline
    expect(s.view("premarket").rows[0].isNewHit).toBe(false);
  });

  it("sessions are isolated (independent seen-sets)", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("snapshot", "rth", { refreshedAt: "t1", rows: [r("A", 2)] })); // rth baseline (silent)
    s.apply(rank("delta", "rth", { refreshedAt: "t2", rows: [r("A", 2), r("B", 9)] }));
    const rth = Object.fromEntries(s.view("rth").rows.map((row) => [row.symbol, row]));
    expect(rth.B.isNewHit).toBe(true);  // new in rth
    expect(rth.A.isNewHit).toBe(false); // carried over in rth
    expect(s.view("premarket").rows[0].isNewHit).toBe(false); // premarket untouched
  });

  it("a reconnect re-snapshot is a clean baseline (no flash, no stale mute)", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6)] })); // A seen → muted
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t2", rows: [r("A", 6), r("B", 3)] })); // reconnect
    expect(s.view("premarket").rows.every((row) => !row.isNewHit && !row.muted)).toBe(true);
  });

  it("view of an unknown session is empty", () => {
    expect(new ScannerStore().view("afterhours")).toEqual({ rows: [], refreshedAt: null });
  });
});

// Distinct name to avoid colliding with the file's existing `rank(kind, session, payload)`.
const rankMsg = (kind: "snapshot" | "delta", symbols: string[]) => ({
  kind, topic: "scanner.rank" as const, key: "premarket",
  payload: { refreshedAt: "2026-07-06T13:00:00Z", rows: symbols.map((symbol) => ({ symbol, changePct: 5, last: 1, floatShares: 1, volume: 1 })) },
});

describe("ScannerStore.onNewHit", () => {
  it("first delta is a silent baseline; genuinely-new later rows fire", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("delta", ["AAA"]));        // empty seen ⇒ silent baseline
    s.apply(rankMsg("delta", ["AAA", "BBB"])); // BBB new ⇒ fires
    expect(cb.mock.calls.map((c) => c[0])).toEqual(["BBB"]);
  });

  it("is silent on snapshots and for already-seen symbols", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("snapshot", ["AAA"]));  // seeds silently
    s.apply(rankMsg("delta", ["AAA"]));     // already seen
    expect(cb).not.toHaveBeenCalled();
  });

  it("fires on a scanner.hit force-flash even for an already-seen symbol", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rankMsg("snapshot", ["AAA"]));  // AAA now seen, silent
    s.apply({ kind: "delta", topic: "scanner.hit", key: "premarket", payload: { symbol: "AAA", at: "2026-07-06T13:01:00Z" } });
    expect(cb).toHaveBeenCalledWith("AAA");
  });
});

describe("ScannerStore.currentView", () => {
  const iso = (h: number) => `2026-07-08T${String(h).padStart(2, "0")}:00:00.000Z`;

  it("returns null session when no data has arrived", () => {
    expect(new ScannerStore().currentView()).toEqual({ session: null, rows: [], refreshedAt: null });
  });

  it("returns the session with the freshest refreshedAt", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: iso(8), rows: [r("A", 5)] }));
    s.apply(rank("snapshot", "rth", { refreshedAt: iso(10), rows: [r("B", 9)] }));
    expect(s.currentView().session).toBe("rth");
    expect(s.currentView().rows[0].symbol).toBe("B");
  });

  it("follows the rollover as a newer session overtakes", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "rth", { refreshedAt: iso(10), rows: [r("B", 9)] }));
    expect(s.currentView().session).toBe("rth");
    s.apply(rank("snapshot", "afterhours", { refreshedAt: iso(17), rows: [r("C", 4)] }));
    expect(s.currentView().session).toBe("afterhours");
  });
});
