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

  it("normalizes a null links payload (pre-first-poll zero-value snapshot) to an empty array", () => {
    const s = new HealthStore();
    const nullLinksSnap = {
      kind: "snapshot",
      topic: "sys.health",
      payload: { links: null },
    } as unknown as SnapshotMsg;
    expect(() => s.apply(nullLinksSnap)).not.toThrow();
    const snap = s.getSnapshot();
    expect(snap.links).toEqual([]);
  });

  it("ignores a null sys.events payload (nil Go slice marshaled as JSON null) instead of appending a null entry", () => {
    const s = new HealthStore();
    const nullEventsMsg = {
      kind: "delta",
      topic: "sys.events",
      payload: null,
    } as unknown as DeltaMsg;
    expect(() => s.apply(nullEventsMsg)).not.toThrow();
    const snap = s.getSnapshot();
    expect(snap.events).toEqual([]);
  });

  it("setUiEngine overrides the ui-engine entry reported down by the engine", () => {
    const s = new HealthStore();
    s.apply({
      kind: "snapshot",
      topic: "sys.health",
      payload: { links: [{ link: "ui-engine", ms: null, min: null, avg: null, max: null, status: "down" }] },
    });
    s.setUiEngine({ link: "ui-engine", ms: 5, min: 3, avg: 4, max: 6, status: "ok" });
    const snap = s.getSnapshot();
    expect(snap.links).toEqual([{ link: "ui-engine", ms: 5, min: 3, avg: 4, max: 6, status: "ok" }]);
  });

  it("inserts a ui-engine entry when the engine's snapshot omits that link entirely", () => {
    const s = new HealthStore();
    s.apply({
      kind: "snapshot",
      topic: "sys.health",
      payload: { links: [{ link: "engine-moomoo", ms: 10, min: 8, avg: 9, max: 12, status: "ok" }] },
    });
    s.setUiEngine({ link: "ui-engine", ms: 2, min: 1, avg: 2, max: 3, status: "ok" });
    const snap = s.getSnapshot();
    expect(snap.links[0]).toEqual({ link: "ui-engine", ms: 2, min: 1, avg: 2, max: 3, status: "ok" });
    expect(snap.links.map((l) => l.link)).toEqual(["ui-engine", "engine-moomoo"]);
  });

  it("keeps the uiEngine override across a later sys.health snapshot, while other links update", () => {
    const s = new HealthStore();
    s.apply({
      kind: "snapshot",
      topic: "sys.health",
      payload: {
        links: [
          { link: "ui-engine", ms: null, min: null, avg: null, max: null, status: "down" },
          { link: "engine-moomoo", ms: 10, min: 8, avg: 9, max: 12, status: "ok" },
        ],
      },
    });
    s.setUiEngine({ link: "ui-engine", ms: 5, min: 3, avg: 4, max: 6, status: "ok" });
    s.apply({
      kind: "delta",
      topic: "sys.health",
      payload: {
        links: [
          { link: "ui-engine", ms: null, min: null, avg: null, max: null, status: "down" },
          { link: "engine-moomoo", ms: 20, min: 15, avg: 18, max: 25, status: "degraded" },
        ],
      },
    });
    const snap = s.getSnapshot();
    expect(snap.links).toEqual([
      { link: "ui-engine", ms: 5, min: 3, avg: 4, max: 6, status: "ok" },
      { link: "engine-moomoo", ms: 20, min: 15, avg: 18, max: 25, status: "degraded" },
    ]);
  });

  it("setUiEngine(null) falls back to whatever the engine's snapshot reports", () => {
    const s = new HealthStore();
    s.apply({
      kind: "snapshot",
      topic: "sys.health",
      payload: { links: [{ link: "ui-engine", ms: null, min: null, avg: null, max: null, status: "down" }] },
    });
    s.setUiEngine({ link: "ui-engine", ms: 5, min: 3, avg: 4, max: 6, status: "ok" });
    s.setUiEngine(null);
    const snap = s.getSnapshot();
    expect(snap.links).toEqual([{ link: "ui-engine", ms: null, min: null, avg: null, max: null, status: "down" }]);
  });

  it("stores the quota snapshot from sys.health", () => {
    const s = new HealthStore();
    s.apply({ kind: "snapshot", topic: "sys.health",
      payload: { links: [], quota: { subUsed: 62, subRemain: 38, subOwn: 47, subForeign: 15,
        histUsed: 41, histRemain: 59, state: "foreign", histState: "ok" } } });
    expect(s.getSnapshot().quota?.subForeign).toBe(15);
    expect(s.getSnapshot().quota?.state).toBe("foreign");
  });
});
