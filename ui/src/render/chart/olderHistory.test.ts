// ui/src/render/chart/olderHistory.test.ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OlderHistoryController } from "./olderHistory";

const range = (from: number, to: number) => ({ from, to });

// vi.fn().mockResolvedValue()'s own resolution plus this module's .then()/
// .catch() chain take a few real microtask ticks to settle (measured
// empirically at 3 under this vitest/mock-fn version) — flush with margin
// rather than hard-coding an exact hop count that could drift with the
// library version.
async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

// OlderHistoryController schedules a real setTimeout as its 30s lost-ack
// safety net; fake timers keep that deterministic and stop it from leaking a
// live 30s timer past any test that never resolves its `load` promise.
beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("OlderHistoryController", () => {
  it("fires when fewer than 1.5 screens remain to the left", () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 100, exhausted: false } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    // screens = to-from = 100; remaining = from - LEFT_PAD_BARS = 10 - 4 = 6 < 150
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledWith(false);
  });

  it("does not fire when far from the left edge", () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 0, exhausted: false } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(1000, 1100), true); // remaining 996 > 150
    expect(load).not.toHaveBeenCalled();
  });

  it("suppresses a second request while one is in flight", () => {
    const load = vi.fn().mockReturnValue(new Promise(() => {})); // never resolves
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("stops asking after an exhausted ack (per kind, independent of the other kind)", async () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 0, exhausted: true } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    await flush(); // ack lands: intraday is no longer in flight, but is now exhausted
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledTimes(1); // blocked by `exhausted`, not a lingering in-flight flag
    // daily kind is independent — still allowed
    c.maybeTrigger(range(10, 110), false);
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("applies a cooldown after a blocked ack", async () => {
    const load = vi.fn().mockResolvedValue({ status: "blocked", reason: "not ready" });
    let t = 0;
    const c = new OlderHistoryController({ load, now: () => t });
    c.maybeTrigger(range(10, 110), true);
    await flush();
    t = 1000;
    c.maybeTrigger(range(10, 110), true); // within 5s cooldown
    expect(load).toHaveBeenCalledTimes(1);
    t = 6000;
    c.maybeTrigger(range(10, 110), true); // cooldown elapsed
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("reset() re-enables an exhausted kind", async () => {
    const load = vi.fn().mockResolvedValue({ status: "accepted", value: { added: 0, exhausted: true } });
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    await flush();
    c.maybeTrigger(range(10, 110), true); // exhausted — must not fire again
    expect(load).toHaveBeenCalledTimes(1);
    c.reset();
    c.maybeTrigger(range(10, 110), true); // reset cleared the exhausted flag — fires again
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("clears the in-flight flag after a 30s timeout when no ack ever arrives, without marking exhausted", () => {
    const load = vi.fn().mockReturnValue(new Promise(() => {})); // ack lost, e.g. across a reconnect
    const c = new OlderHistoryController({ load, now: () => 0 });
    c.maybeTrigger(range(10, 110), true);
    expect(load).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(29_999);
    c.maybeTrigger(range(10, 110), true); // still in flight, suppressed
    expect(load).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(2); // crosses the 30s mark
    c.maybeTrigger(range(10, 110), true); // in-flight cleared, kind not exhausted -> fires again
    expect(load).toHaveBeenCalledTimes(2);
  });
});
