import { describe, it, expect, vi } from "vitest";
import { SessionStore } from "./SessionStore";
import type { SnapshotMsg } from "../wire/contract";

const snap = (mode: "live" | "replay", day?: string, speed?: number): SnapshotMsg => ({
  kind: "snapshot", topic: "sys.session", payload: { mode, day, speed },
});

describe("SessionStore", () => {
  it("defaults to live and applies a replay snapshot, notifying subscribers", () => {
    const s = new SessionStore();
    expect(s.getSnapshot().mode).toBe("live");
    const cb = vi.fn();
    s.subscribe(cb);
    s.apply(snap("replay", "2026-07-06", 4));
    expect(s.getSnapshot()).toEqual({ mode: "replay", day: "2026-07-06", speed: 4 });
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
