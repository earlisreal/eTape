import { describe, it, expect, vi } from "vitest";
import { HealthStore } from "./HealthStore";
import type { SnapshotMsg, DeltaMsg } from "../wire/contract";

const healthSnap = (): SnapshotMsg => ({
  kind: "snapshot", topic: "sys.health",
  payload: { links: [{ link: "ui-engine", ms: 1, min: 1, avg: 1, max: 1, status: "ok" }] },
});
const eventDelta = (seq: number): DeltaMsg => ({
  kind: "delta", topic: "sys.events",
  payload: { seq, ts: `t${seq}`, kind: "reconnect", detail: `attempt ${seq}` },
});

describe("HealthStore", () => {
  it("routes health links and appends events, notifying subscribers", () => {
    const s = new HealthStore();
    const cb = vi.fn();
    s.subscribe(cb);
    s.apply(healthSnap());
    s.apply(eventDelta(1));
    s.apply(eventDelta(2));
    const snap = s.getSnapshot();
    expect(snap.links[0].link).toBe("ui-engine");
    expect(snap.events.map((e) => e.seq)).toEqual([1, 2]);
    expect(cb).toHaveBeenCalledTimes(3);
  });

  it("caps the event log at 500 entries", () => {
    const s = new HealthStore();
    for (let i = 0; i < 600; i++) s.apply(eventDelta(i));
    const snap = s.getSnapshot();
    expect(snap.events).toHaveLength(500);
    expect(snap.events[0].seq).toBe(100); // oldest 100 dropped
  });
});
