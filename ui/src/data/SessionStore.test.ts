import { describe, it, expect, vi } from "vitest";
import { SessionStore } from "./SessionStore";
import type { SnapshotMsg } from "../wire/contract";

const snap = (mode: "live" | "demo"): SnapshotMsg => ({
  kind: "snapshot", topic: "sys.session", payload: { mode },
});

describe("SessionStore", () => {
  // Defaults to "pending" (not "live") until the first sys.session snapshot
  // arrives — seeding to "live" would render a confident live posture for the
  // sub-frame before a demo boot's real mode is known (see
  // OrderTicketPanel's pending badge, which is the visible half of this fix).
  it("defaults to pending and applies a demo snapshot, notifying subscribers", () => {
    const s = new SessionStore();
    expect(s.getSnapshot().mode).toBe("pending");
    const cb = vi.fn();
    s.subscribe(cb);
    s.apply(snap("demo"));
    expect(s.getSnapshot()).toEqual({ mode: "demo" });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("applies a live snapshot, resolving out of pending", () => {
    const s = new SessionStore();
    s.apply(snap("live"));
    expect(s.getSnapshot()).toEqual({ mode: "live" });
  });
});
