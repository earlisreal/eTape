import { describe, it, expect, vi } from "vitest";
import { Scheduler } from "./Scheduler";
import { FakeRaf } from "../../test/fakes";
import type { Surface } from "./surface";
import { BarStore } from "../data/BarStore";
import type { SnapshotMsg } from "../wire/contract";
import { PerfMonitor } from "../perf/PerfMonitor";

function surf(id: string, dirty: () => boolean, paint: () => void): Surface {
  return { id, isDirty: dirty, paint };
}

// Registers a Surface that tracks its OWN last-seen revision cursor against a
// shared store, mirroring ChartPanel.tsx's fix for the shared-dirty-flag bug
// (Finding 1): a shared consume-and-reset boolean would let only the
// first-visited surface each frame observe a change, starving every other
// surface sharing the same store instance.
function revSurf(id: string, store: { getRev(): number }, paint: () => void): Surface {
  let lastRev = -1;
  return {
    id,
    isDirty: () => {
      const rev = store.getRev();
      const changed = rev !== lastRev;
      lastRev = rev;
      return changed;
    },
    paint,
  };
}

describe("Scheduler", () => {
  it("paints only dirty surfaces, once per frame", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    let dirtyA = true;
    const paintA = vi.fn(() => { dirtyA = false; });
    const paintB = vi.fn();
    sched.register(surf("a", () => dirtyA, paintA));
    sched.register(surf("b", () => false, paintB));
    sched.start();
    raf.tick();
    expect(paintA).toHaveBeenCalledTimes(1);
    expect(paintB).not.toHaveBeenCalled();
    raf.tick();
    expect(paintA).toHaveBeenCalledTimes(1); // no longer dirty
  });

  it("unregisters a painter that keeps throwing every frame and reports it, others survive", () => {
    const raf = new FakeRaf();
    const onErr = vi.fn();
    const sched = new Scheduler(raf, onErr);
    const good = vi.fn();
    sched.register(surf("bad", () => true, () => { throw new Error("boom"); }));
    sched.register(surf("good", () => true, good));
    sched.start();
    for (let i = 0; i < 10; i++) raf.tick(); // MAX_CONSECUTIVE_FAILURES worth of frames
    expect(onErr).toHaveBeenCalledTimes(10);
    expect(onErr).toHaveBeenCalledWith("bad", expect.any(Error));
    expect(good).toHaveBeenCalledTimes(10);
    raf.tick();
    expect(good).toHaveBeenCalledTimes(11); // bad no longer scheduled; good keeps painting
  });

  it("survives a single transient throw and keeps painting on the next dirty frame", () => {
    const raf = new FakeRaf();
    const onErr = vi.fn();
    const sched = new Scheduler(raf, onErr);
    let shouldThrow = true;
    const paint = vi.fn(() => {
      if (shouldThrow) { shouldThrow = false; throw new Error("transient"); }
    });
    sched.register(surf("flaky", () => true, paint));
    sched.start();
    raf.tick(); // throws once
    expect(onErr).toHaveBeenCalledTimes(1);
    raf.tick(); // recovers — still registered, paints successfully
    expect(paint).toHaveBeenCalledTimes(2);
    raf.tick(); // a later, unrelated failure starts counting from zero again
    expect(onErr).toHaveBeenCalledTimes(1);
  });

  it("paints all surfaces sharing one store when each tracks its own rev cursor (regression: shared consumeDirty() starved every panel but the first)", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    const bars = new BarStore(); // one store, shared by two independent "chart panel" surfaces
    const paintA = vi.fn();
    const paintB = vi.fn();
    sched.register(revSurf("panelA", bars, paintA));
    sched.register(revSurf("panelB", bars, paintB));
    sched.start();

    const snapshot: SnapshotMsg = {
      kind: "snapshot", topic: "md.bars", key: "US.AAPL:1m",
      payload: [{ symbol: "US.AAPL", timeframe: "1m", bucketStart: "t0",
        o: 1, h: 1, l: 1, c: 1, v: 1, inProgress: false }],
    };
    bars.apply(snapshot); // a single update to the shared store

    raf.tick();
    // Both surfaces must observe and paint the single shared-store update —
    // under the old shared-boolean-flag bug, only the first-registered surface
    // ever saw isDirty() === true and the second was starved forever.
    expect(paintA).toHaveBeenCalledTimes(1);
    expect(paintB).toHaveBeenCalledTimes(1);

    raf.tick();
    // No further update: neither surface repaints spuriously.
    expect(paintA).toHaveBeenCalledTimes(1);
    expect(paintB).toHaveBeenCalledTimes(1);
  });

  it("stops requesting frames after stop()", () => {
    const raf = new FakeRaf();
    const sched = new Scheduler(raf, () => {});
    const paint = vi.fn();
    sched.register(surf("a", () => true, paint));
    sched.start();
    raf.tick();
    sched.stop();
    raf.tick();
    expect(paint).toHaveBeenCalledTimes(1);
  });

  it("reports nothing into an injected PerfMonitor while it's disabled, and paint timing + frame interval once enabled", () => {
    const raf = new FakeRaf();
    const perfMon = new PerfMonitor(() => 0); // fixed clock — interval math stays deterministic
    const sched = new Scheduler(raf, () => {}, perfMon);
    sched.register(surf("a", () => true, () => {}));
    sched.start();

    raf.tick(); // perfMon.enabled is false (the default) — must be a complete no-op
    expect(perfMon.snapshot()).toEqual({
      enabled: false, paint: {}, scan: {},
      frame: { intervalMs: null, droppedFrames: 0 }, wsMsgsPerSec: 0, ticksPerSec: 0,
    });

    perfMon.enable();
    raf.tick(); // first frame after enabling: no prior frame to diff against yet
    raf.tick(); // second frame: interval now computable
    const snap = perfMon.snapshot();
    expect(snap.paint.a).toBeDefined();
    expect(snap.frame.intervalMs).toBe(0); // fixed clock — same instant every tick
  });
});
