import { describe, it, expect } from "vitest";
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

  it("resetSeen re-flashes everything on the next delta (ET-midnight behavior)", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "premarket", { refreshedAt: "t1", rows: [r("A", 6)] })); // A now seen → muted
    s.resetSeen("premarket");
    s.apply(rank("delta", "premarket", { refreshedAt: "t2", rows: [r("A", 6)] }));
    expect(s.view("premarket").rows[0].isNewHit).toBe(true);
  });

  it("sessions are isolated", () => {
    const s = new ScannerStore();
    s.apply(rank("snapshot", "premarket", { refreshedAt: "t0", rows: [r("A", 5)] }));
    s.apply(rank("delta", "rth", { refreshedAt: "t1", rows: [r("A", 2)] })); // A never seen in rth
    expect(s.view("rth").rows[0].isNewHit).toBe(true);
    expect(s.view("premarket").rows[0].isNewHit).toBe(false);
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
