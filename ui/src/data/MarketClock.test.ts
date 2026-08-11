import { describe, expect, it } from "vitest";
import { MARKET_CLOCK_STALE_MS, MarketClock } from "./MarketClock";

describe("MarketClock", () => {
  it("falls back to browser time until a valid sample arrives", () => {
    const now = 10_000;
    const clock = new MarketClock(() => now);
    expect(clock.nowMs()).toBe(10_000);
    expect(clock.snapshot()).toMatchObject({ synchronized: false, stale: false, offsetMs: 0 });

    clock.update({
      effectiveOffsetMs: 2_000,
      browserToEngineOffsetMs: 10,
      marketOffsetMs: 1_990,
      engineTimeMs: 10_005,
      browserRttMs: 20,
      marketSampleAgeMs: 100,
      marketSampleRttMs: 30,
    });
    expect(clock.nowMs()).toBe(12_000);
    expect(clock.snapshot()).toMatchObject({ synchronized: true, offsetMs: 2_000, marketSampleAgeMs: 100 });
  });

  it("ages a sample without changing its offset and marks it stale", () => {
    let now = 10_000;
    const clock = new MarketClock(() => now);
    clock.update({
      effectiveOffsetMs: -1_500,
      browserToEngineOffsetMs: -500,
      marketOffsetMs: -1_000,
      engineTimeMs: 9_500,
      browserRttMs: 10,
      marketSampleAgeMs: 200,
      marketSampleRttMs: 40,
    });
    now += MARKET_CLOCK_STALE_MS + 1_000;
    expect(clock.nowMs()).toBe(now - 1_500);
    expect(clock.snapshot()).toMatchObject({ synchronized: true, stale: true, offsetMs: -1_500 });
    expect(clock.snapshot().marketSampleAgeMs).toBe(MARKET_CLOCK_STALE_MS + 1_200);
  });

  it("ignores malformed or implausibly large offsets", () => {
    const now = 10_000;
    const clock = new MarketClock(() => now);
    clock.update({
      effectiveOffsetMs: Number.NaN,
      browserToEngineOffsetMs: 0,
      marketOffsetMs: 0,
      engineTimeMs: 0,
      browserRttMs: 0,
      marketSampleAgeMs: 0,
      marketSampleRttMs: 0,
    });
    clock.update({
      effectiveOffsetMs: 25 * 60 * 60 * 1000,
      browserToEngineOffsetMs: 0,
      marketOffsetMs: 0,
      engineTimeMs: 0,
      browserRttMs: 0,
      marketSampleAgeMs: 0,
      marketSampleRttMs: 0,
    });
    expect(clock.snapshot()).toMatchObject({ synchronized: false, offsetMs: 0 });
  });
});
