import { describe, it, expect } from "vitest";
import { PerfMonitor, initPerfFromQuery, perf } from "./PerfMonitor";

// A controllable clock so frame-interval / rolling-window math is exercised
// deterministically instead of depending on real elapsed wall time.
function fakeClock(start = 0) {
  let t = start;
  return { now: () => t, advance: (ms: number) => { t += ms; } };
}

describe("PerfMonitor — disabled (default) behavior", () => {
  it("is disabled by default", () => {
    const p = new PerfMonitor();
    expect(p.enabled).toBe(false);
  });

  it("every recording method is a no-op while disabled", () => {
    const clock = fakeClock();
    const p = new PerfMonitor(clock.now);
    p.recordPaint("tape", 5);
    p.frameTick();
    clock.advance(50);
    p.frameTick();
    p.countMessage("md.tape");
    p.countTicks(10);
    p.recordScan("tape", 42);

    const snap = p.snapshot();
    expect(snap.enabled).toBe(false);
    expect(snap.paint).toEqual({});
    expect(snap.scan).toEqual({});
    expect(snap.frame).toEqual({ intervalMs: null, droppedFrames: 0 });
    expect(snap.wsMsgsPerSec).toBe(0);
    expect(snap.ticksPerSec).toBe(0);
  });
});

describe("PerfMonitor — enabled behavior", () => {
  it("enable()/disable() toggle the enabled flag", () => {
    const p = new PerfMonitor();
    expect(p.enabled).toBe(false);
    p.enable();
    expect(p.enabled).toBe(true);
    p.disable();
    expect(p.enabled).toBe(false);
  });

  it("recordPaint tracks last/max and an EWMA per surface id", () => {
    const p = new PerfMonitor();
    p.enable();
    p.recordPaint("tape", 5);
    p.recordPaint("tape", 15);
    p.recordPaint("tape", 3);
    const { paint } = p.snapshot();
    expect(paint.tape.last).toBe(3);
    expect(paint.tape.max).toBe(15);
    // EWMA is pulled toward each new sample but never jumps straight to it
    // (a smoothed trend line, not a running max/last restatement).
    expect(paint.tape.ewma).toBeGreaterThan(3);
    expect(paint.tape.ewma).toBeLessThan(15);
  });

  it("tracks independent stats per surface id", () => {
    const p = new PerfMonitor();
    p.enable();
    p.recordPaint("tape", 4);
    p.recordPaint("ladder", 9);
    const { paint } = p.snapshot();
    expect(paint.tape.last).toBe(4);
    expect(paint.ladder.last).toBe(9);
  });

  it("frameTick records the interval between consecutive frames", () => {
    const clock = fakeClock(0);
    const p = new PerfMonitor(clock.now);
    p.enable();
    p.frameTick(); // first tick — no prior frame to diff against
    expect(p.snapshot().frame.intervalMs).toBeNull();
    clock.advance(16);
    p.frameTick();
    expect(p.snapshot().frame.intervalMs).toBe(16);
  });

  it("counts a dropped frame when the interval exceeds ~1.5x the 16.7ms frame budget", () => {
    const clock = fakeClock(0);
    const p = new PerfMonitor(clock.now);
    p.enable();
    p.frameTick();
    clock.advance(16); // well under threshold
    p.frameTick();
    expect(p.snapshot().frame.droppedFrames).toBe(0);
    clock.advance(40); // > 1.5 * 16.7 (~25.05ms)
    p.frameTick();
    expect(p.snapshot().frame.droppedFrames).toBe(1);
    clock.advance(10); // back under threshold
    p.frameTick();
    expect(p.snapshot().frame.droppedFrames).toBe(1); // unchanged
  });

  it("countMessage rolls a per-second window into wsMsgsPerSec", () => {
    const clock = fakeClock(0);
    const p = new PerfMonitor(clock.now);
    p.enable();
    p.countMessage("md.tape");
    p.countMessage("md.quote");
    p.countMessage("md.tape");
    // Still inside the first window: the rate hasn't rolled over yet.
    expect(p.snapshot().wsMsgsPerSec).toBe(0);
    clock.advance(1000);
    // Reading the snapshot itself rolls a window whose clock has expired,
    // so a caller polling at a fixed interval sees a decayed rate even
    // without a fresh message arriving right at the boundary.
    expect(p.snapshot().wsMsgsPerSec).toBe(3);
  });

  it("countTicks rolls a per-second window into ticksPerSec, summing counts within the window", () => {
    const clock = fakeClock(0);
    const p = new PerfMonitor(clock.now);
    p.enable();
    p.countTicks(4);
    p.countTicks(6);
    clock.advance(1000);
    expect(p.snapshot().ticksPerSec).toBe(10);
  });

  it("idles back to zero after a full second with no messages", () => {
    const clock = fakeClock(0);
    const p = new PerfMonitor(clock.now);
    p.enable();
    p.countMessage("md.tape");
    clock.advance(1000);
    expect(p.snapshot().wsMsgsPerSec).toBe(1);
    clock.advance(1000);
    expect(p.snapshot().wsMsgsPerSec).toBe(0);
  });

  it("recordScan tracks last/max scan length per surface id", () => {
    const p = new PerfMonitor();
    p.enable();
    p.recordScan("tape:panel-1", 12);
    p.recordScan("tape:panel-1", 40);
    p.recordScan("tape:panel-1", 7);
    const { scan } = p.snapshot();
    expect(scan["tape:panel-1"]).toEqual({ last: 7, max: 40 });
  });

  it("disabling stops further recording but a prior snapshot's numbers are inert (no crash reading after disable)", () => {
    const p = new PerfMonitor();
    p.enable();
    p.recordPaint("tape", 5);
    p.disable();
    p.recordPaint("tape", 999); // no-op while disabled
    expect(p.snapshot().enabled).toBe(false);
    p.enable();
    // Re-enabling starts a clean measurement session rather than carrying
    // over stale numbers from a previous, unrelated enabled period.
    expect(p.snapshot().paint).toEqual({});
  });
});

describe("initPerfFromQuery", () => {
  it("enables the shared singleton when the query string has perf=1", () => {
    perf.disable();
    initPerfFromQuery("?perf=1");
    expect(perf.enabled).toBe(true);
    perf.disable(); // reset shared module state for other tests/files
  });

  it("leaves perf disabled for any other query string", () => {
    perf.disable();
    initPerfFromQuery("?other=1");
    expect(perf.enabled).toBe(false);
    initPerfFromQuery("");
    expect(perf.enabled).toBe(false);
  });
});
