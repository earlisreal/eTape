import { describe, expect, it, vi } from "vitest";
import { ScannerStore } from "./ScannerStore";
import type { ScannerRankPayload, ScannerRow, SnapshotMsg, DeltaMsg } from "../wire/contract";

const row = (symbol: string, changePct: number | null, alertSeq = 0, changeStatus?: ScannerRow["changeStatus"]): ScannerRow => ({
  symbol, shortSellRestricted: false, changePct, ...(changeStatus ? { changeStatus } : {}), alertSeq, last: 1, floatShares: 1_000_000,
  volume: 1000, relativeVolume: null, shortInterest: null, shortInterestAsOf: null,
});

const rank = (kind: "snapshot" | "delta", session: string, payload: ScannerRankPayload) =>
  ({ kind, topic: "scanner.rank", key: session, payload } as SnapshotMsg | DeltaMsg);

describe("ScannerStore", () => {
  it("normalizes legacy rows without enrichment fields", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [row("US.LEGACY", 1)] }));
    const got = s.view("premarket").rows[0];
    expect(got.relativeVolume).toBeNull();
    expect(got.shortInterest).toBeNull();
    expect(got.shortInterestAsOf).toBeNull();
    expect(got.changeStatus).toBe("ready");
  });

  it("keeps snapshots and baseline deltas silent while seeding revisions", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [row("A", 5, 3)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", baseline: true, rows: [row("A", 5, 4), row("B", null, 1, "warming")] }));
    expect(cb).not.toHaveBeenCalled();
    expect(s.view("premarket").rows.every((r) => !r.isUnseen && !r.isNewHit)).toBe(true);
  });

  it("uses a current snapshot as the silent baseline after an engine restart", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rank("snapshot", "rth", { refreshedAt: "t1", rows: [row("A", 6, 4)] }));
    s.apply(rank("snapshot", "rth", { refreshedAt: "t2", rows: [row("A", 6, 1)] }));
    s.apply(rank("delta", "rth", { refreshedAt: "t3", rows: [row("A", 7, 2)] }));
    expect(cb).toHaveBeenCalledTimes(1);
    expect(s.view("rth").rows[0]?.alertSeq).toBe(2);
  });

  it("fires once when an alert revision advances and ignores duplicates or replays", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [row("A", 5, 3)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [row("A", 6, 4)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [row("A", 6, 4)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t3", rows: [row("A", 6, 3)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t4", rows: [row("A", 7, 5)] }));
    expect(cb.mock.calls.map(([symbol]) => symbol)).toEqual(["A", "A"]);
    expect(s.view("premarket").rows[0].isUnseen).toBe(true);
  });

  it("ignores scanner.hit because rank alertSeq is authoritative", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [row("A", 5)] }));
    s.apply({ kind: "delta", topic: "scanner.hit", key: "premarket", payload: { symbol: "A", at: "t1" } } as DeltaMsg);
    expect(cb).not.toHaveBeenCalled();
    expect(s.view("premarket").rows[0].isUnseen).toBe(false);
  });

  it("clears unread without resetting the revision watermark", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [row("A", 5, 1)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [row("A", 6, 2)] }));
    s.markSeen("premarket", "A");
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [row("A", 6, 2)] }));
    expect(s.view("premarket").rows[0].isUnseen).toBe(false);
    expect(cb).toHaveBeenCalledTimes(1);
    s.apply(rank("delta", "premarket", { refreshedAt: "t3", rows: [row("A", 7, 3)] }));
    expect(cb).toHaveBeenCalledTimes(2);
  });

  it("keeps unread state per session but shares symbol revision watermarks", () => {
    const s = new ScannerStore();
    const cb = vi.fn();
    s.onNewHit(cb);
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [row("A", 5, 4)] }));
    s.apply(rank("delta", "rth", { refreshedAt: "t1", rows: [row("A", 5, 4)] }));
    expect(cb).not.toHaveBeenCalled();
    s.apply(rank("delta", "rth", { refreshedAt: "t2", rows: [row("A", 6, 5)] }));
    expect(cb).toHaveBeenCalledWith("A");
    expect(s.view("premarket").rows[0].isUnseen).toBe(false);
    expect(s.view("rth").rows[0].isUnseen).toBe(true);
  });

  it("tracks warming counts and renders unavailable legacy values", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", {
      refreshedAt: "t0", warmingCount: 2,
      rows: [row("WARM", null, 0, "warming"), row("DOWN", null, 0, "unavailable")],
    }));
    expect(s.view("premarket").warmingCount).toBe(2);
    expect(s.view("premarket").rows.map((r) => r.changeStatus)).toEqual(["warming", "unavailable"]);
  });

  it("returns empty views with the complete state shape", () => {
    const s = new ScannerStore();
    expect(s.view("afterhours")).toEqual({ rows: [], refreshedAt: null, filters: null, warmingCount: 0 });
    expect(s.currentView()).toEqual({ session: null, rows: [], refreshedAt: null, filters: null, warmingCount: 0 });
  });
});

describe("ScannerStore.currentView", () => {
  const iso = (h: number) => `2026-07-08T${String(h).padStart(2, "0")}:00:00.000Z`;

  it("returns the session with the freshest refreshedAt", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: iso(8), rows: [row("A", 5)] }));
    s.apply(rank("snapshot", "rth", { refreshedAt: iso(10), rows: [row("B", 9)] }));
    expect(s.currentView().session).toBe("rth");
    expect(s.currentView().rows[0].symbol).toBe("B");
  });

  it("follows the rollover as a newer session overtakes", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "rth", { refreshedAt: iso(10), rows: [row("B", 9)] }));
    s.apply(rank("snapshot", "afterhours", { refreshedAt: iso(17), rows: [row("C", 4)] }));
    expect(s.currentView().session).toBe("afterhours");
  });
});
