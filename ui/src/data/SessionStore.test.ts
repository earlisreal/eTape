import { describe, it, expect, vi } from "vitest";
import { SessionStore } from "./SessionStore";
import type { SnapshotMsg } from "../wire/contract";

const snap = (mode: "live" | "replay", day?: string, speed?: number): SnapshotMsg => ({
  kind: "snapshot", topic: "sys.session", payload: { mode, day, speed },
});

describe("SessionStore", () => {
  // Defaults to "pending" (not "live") until the first sys.session snapshot
  // arrives — seeding to "live" would render a confident live posture for the
  // sub-frame before a replay/demo boot's real mode is known (see
  // OrderTicketPanel's pending badge, which is the visible half of this fix).
  it("defaults to pending and applies a replay snapshot, notifying subscribers", () => {
    const s = new SessionStore();
    expect(s.getSnapshot().mode).toBe("pending");
    const cb = vi.fn();
    s.subscribe(cb);
    s.apply(snap("replay", "2026-07-06", 4));
    expect(s.getSnapshot()).toEqual({ mode: "replay", day: "2026-07-06", speed: 4 });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("applies a live snapshot, resolving out of pending", () => {
    const s = new SessionStore();
    s.apply(snap("live"));
    expect(s.getSnapshot()).toEqual({ mode: "live" });
  });
});
