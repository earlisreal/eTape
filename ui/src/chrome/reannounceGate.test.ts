import { afterEach, describe, expect, it, vi } from "vitest";
import { ReannounceGate } from "./reannounceGate";

afterEach(() => {
  vi.useRealTimers();
});

describe("ReannounceGate", () => {
  it("unchanged mode resolves on the next session snapshot", async () => {
    const g = new ReannounceGate({ timeoutMs: 5000, initialMode: "live" });
    const p = g.gate();
    g.onSessionMode("live"); // unchanged
    await expect(p).resolves.toBeUndefined();
  });

  it("changed mode waits for transition-applied", async () => {
    const g = new ReannounceGate({ timeoutMs: 5000, initialMode: "live" });
    const p = g.gate();
    g.onSessionMode("demo"); // changed → must wait
    let resolved = false;
    void p.then(() => (resolved = true));
    await Promise.resolve();
    expect(resolved).toBe(false);
    g.onTransitionApplied();
    await expect(p).resolves.toBeUndefined();
  });

  it("changed mode times out if transition never signals", async () => {
    vi.useFakeTimers();
    const g = new ReannounceGate({ timeoutMs: 5000, initialMode: "live" });
    const p = g.gate();
    g.onSessionMode("demo");
    vi.advanceTimersByTime(5000);
    await expect(p).resolves.toBeUndefined();
  });
});
