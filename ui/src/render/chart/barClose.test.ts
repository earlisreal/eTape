import { describe, it, expect } from "vitest";
import { remainingToBarCloseMs, isIntradayTimeframe, formatCountdown } from "./barClose";

const at = (iso: string) => Date.parse(iso);

describe("remainingToBarCloseMs", () => {
  it("returns time remaining until the current bar closes at 10s timeframe", () => {
    // 2026-07-06T13:30:07Z is 7 seconds into the 10s bucket starting at 13:30:00Z
    // Bar closes at 13:30:10Z, so remaining is 3 seconds = 3000 ms
    const remaining = remainingToBarCloseMs("10s", at("2026-07-06T13:30:07Z"));
    expect(remaining).toBe(3000);
  });

  it("returns full duration at exactly the bucket boundary (start of next bar)", () => {
    // At 13:30:10Z, we're at the START of the next 10s bar (which runs until 13:30:20Z)
    // So remaining = 10 seconds
    const remaining = remainingToBarCloseMs("10s", at("2026-07-06T13:30:10Z"));
    expect(remaining).toBe(10000);
  });

  it("returns near-zero when 1 second before close", () => {
    // At 13:30:09Z, 1 second remains until the bar (13:30:00-13:30:10) closes
    const remaining = remainingToBarCloseMs("10s", at("2026-07-06T13:30:09Z"));
    expect(remaining).toBe(1000);
  });

  it("returns full timeframe duration at bucket start", () => {
    // At 13:30:00Z (start of a 10s bar), 10 seconds remain
    const remaining = remainingToBarCloseMs("10s", at("2026-07-06T13:30:00Z"));
    expect(remaining).toBe(10000);
  });

  it("handles 1m timeframe correctly", () => {
    // At 13:30:45Z, in a 1m bar starting at 13:30:00Z
    // 15 seconds remain until 13:31:00Z
    const remaining = remainingToBarCloseMs("1m", at("2026-07-06T13:30:45Z"));
    expect(remaining).toBe(15000);
  });

  it("handles 5m timeframe with session anchor (09:30 ET)", () => {
    // At 13:32:00Z (09:32 ET, 2min into a 5m bar starting at 09:30 ET)
    // 3 minutes remain until 09:35 ET = 13:35:00Z
    const remaining = remainingToBarCloseMs("5m", at("2026-07-06T13:32:00Z"));
    expect(remaining).toBe(3 * 60 * 1000);
  });

  it("handles D (daily) timeframe", () => {
    // At 13:30:00Z on 2026-07-06 (day bucket starts at 04:00Z == 00:00 ET)
    // Day closes at 04:00Z next day = 2026-07-07T04:00:00Z
    const remaining = remainingToBarCloseMs("D", at("2026-07-06T13:30:00Z"));
    const expected = at("2026-07-07T04:00:00Z") - at("2026-07-06T13:30:00Z");
    expect(remaining).toBe(expected);
  });

  it("handles W (weekly) timeframe", () => {
    // At 13:30:00Z on 2026-07-06 (Monday, week bucket starts at 04:00Z Monday)
    // Week closes at 04:00Z Monday next week = 2026-07-13T04:00:00Z
    const remaining = remainingToBarCloseMs("W", at("2026-07-06T13:30:00Z"));
    const expected = at("2026-07-13T04:00:00Z") - at("2026-07-06T13:30:00Z");
    expect(remaining).toBe(expected);
  });

  it("handles M (monthly) timeframe with 30-day approximation", () => {
    // timeframeToMs("M") returns a 30-day approximation (per geometry.ts comment)
    // Month starts at 00:00 ET on the 1st = 2026-07-01T04:00:00Z
    // Remaining = (start + 30days) - now
    const remaining = remainingToBarCloseMs("M", at("2026-07-06T13:30:00Z"));
    const monthStartMs = at("2026-07-01T04:00:00Z");
    const expected = monthStartMs + 30 * 24 * 3600 * 1000 - at("2026-07-06T13:30:00Z");
    expect(remaining).toBe(expected);
  });
});

describe("isIntradayTimeframe", () => {
  it("returns true for 10s", () => {
    expect(isIntradayTimeframe("10s")).toBe(true);
  });

  it("returns true for 1m", () => {
    expect(isIntradayTimeframe("1m")).toBe(true);
  });

  it("returns true for 5m", () => {
    expect(isIntradayTimeframe("5m")).toBe(true);
  });

  it("returns true for 15m", () => {
    expect(isIntradayTimeframe("15m")).toBe(true);
  });

  it("returns true for 30m", () => {
    expect(isIntradayTimeframe("30m")).toBe(true);
  });

  it("returns true for 60m", () => {
    expect(isIntradayTimeframe("60m")).toBe(true);
  });

  it("returns false for D", () => {
    expect(isIntradayTimeframe("D")).toBe(false);
  });

  it("returns false for W", () => {
    expect(isIntradayTimeframe("W")).toBe(false);
  });

  it("returns false for M", () => {
    expect(isIntradayTimeframe("M")).toBe(false);
  });
});

describe("formatCountdown", () => {
  it("clamps negative values to 0", () => {
    expect(formatCountdown(-5000)).toBe("0:00");
  });

  it("formats zero as 0:00", () => {
    expect(formatCountdown(0)).toBe("0:00");
  });

  it("formats sub-second as 0:00", () => {
    expect(formatCountdown(500)).toBe("0:00");
  });

  it("formats 4 seconds as 0:04", () => {
    expect(formatCountdown(4000)).toBe("0:04");
  });

  it("formats 9 seconds as 0:09", () => {
    expect(formatCountdown(9000)).toBe("0:09");
  });

  it("formats 59 seconds as 0:59", () => {
    expect(formatCountdown(59000)).toBe("0:59");
  });

  it("formats 1 minute 7 seconds as 1:07", () => {
    expect(formatCountdown(67000)).toBe("1:07");
  });

  it("formats 59 minutes 59 seconds as 59:59", () => {
    expect(formatCountdown(3599000)).toBe("59:59");
  });

  it("formats 1 hour exactly as 1:00:00", () => {
    expect(formatCountdown(3600000)).toBe("1:00:00");
  });

  it("formats 1 hour 4 seconds as 1:00:04", () => {
    expect(formatCountdown(3604000)).toBe("1:00:04");
  });

  it("formats 1 hour 2 minutes 7 seconds as 1:02:07", () => {
    expect(formatCountdown(3727000)).toBe("1:02:07");
  });

  it("formats 2 hours 30 minutes 45 seconds as 2:30:45", () => {
    expect(formatCountdown(9045000)).toBe("2:30:45");
  });

  it("uses Math.floor semantics (truncates fractional seconds)", () => {
    // 4999 ms should floor to 4 seconds, not round
    expect(formatCountdown(4999)).toBe("0:04");
    // 3603999 ms should floor to 1:00:03, not 1:00:04
    expect(formatCountdown(3603999)).toBe("1:00:03");
  });
});
