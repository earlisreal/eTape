import { describe, it, expect } from "vitest";
import { formatChangePct, formatCompactShares, msUntilEtMidnight } from "./format";

describe("formatChangePct — 3-digit-safe, never fabricates 0%", () => {
  it("signs and rounds to one decimal", () => {
    expect(formatChangePct(4.23)).toBe("+4.2%");
    expect(formatChangePct(-12.35)).toBe("−12.3%"); // U+2212 minus
    expect(formatChangePct(234.5)).toBe("+234.5%"); // 3-digit
  });
  it("renders null (no print yet) as em dash, not 0%", () => {
    expect(formatChangePct(null)).toBe("—");
    expect(formatChangePct(NaN)).toBe("—");
  });
  it("zero is unsigned", () => {
    expect(formatChangePct(0)).toBe("0.0%");
  });
});

describe("formatCompactShares", () => {
  it("compacts K/M/B", () => {
    expect(formatCompactShares(2_100_000)).toBe("2.1M");
    expect(formatCompactShares(950_000)).toBe("950K");
    expect(formatCompactShares(3_200_000_000)).toBe("3.2B");
    expect(formatCompactShares(640)).toBe("640");
  });
  it("compacts trillion-scale values (market cap) at two decimals", () => {
    expect(formatCompactShares(3_210_000_000_000)).toBe("3.21T");
  });
  it("null (unknown) is em dash, but 0 is a real 0", () => {
    expect(formatCompactShares(null)).toBe("—");
    expect(formatCompactShares(0)).toBe("0");
  });
});

describe("msUntilEtMidnight (deterministic in EDT/July, UTC−4)", () => {
  it("09:30 ET → 14.5h remaining", () => {
    // 2026-07-06T13:30:00Z == 09:30:00 America/New_York (EDT)
    expect(msUntilEtMidnight(new Date("2026-07-06T13:30:00Z"))).toBe(52_200_000);
  });
  it("23:59:59 ET → 1s remaining", () => {
    // 2026-07-06T03:59:59Z == 2026-07-05 23:59:59 America/New_York
    expect(msUntilEtMidnight(new Date("2026-07-06T03:59:59Z"))).toBe(1000);
  });
});
