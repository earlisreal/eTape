import { describe, it, expect } from "vitest";
import { axisDecimals, priceDecimals, formatPrice, formatSize, formatTapeTime } from "./format";

describe("axisDecimals (wickplot CandlestickChartMath port)", () => {
  it.each([
    [0.05, 2],
    [0.5, 1],
    [2.5, 1],
    [1, 0],
    [10, 0],
    [0.0001, 4],
    [0.25, 2],
  ])("%f needs %i fractional digits", (step, want) => {
    expect(axisDecimals(step)).toBe(want);
  });
});

describe("priceDecimals", () => {
  it("floors at 2 for whole-dollar US equity prices", () => {
    expect(priceDecimals([187, 190.5])).toBe(2);
  });
  it("expands to what sub-penny prices need", () => {
    expect(priceDecimals([0.1234, 3.5])).toBe(4);
  });
  it("caps at 4", () => {
    expect(priceDecimals([0.00001])).toBe(4);
  });
  it("defaults to 2 on empty input", () => {
    expect(priceDecimals([])).toBe(2);
  });
});

describe("formatPrice / formatSize", () => {
  it("prints a uniform decimal column", () => {
    expect(formatPrice(3.5, 2)).toBe("3.50");
  });
  it("absorbs float dust from level arithmetic", () => {
    expect(formatPrice(3.49 - 9 * 0.01, 2)).toBe("3.40"); // 3.3999999999999995
  });
  it("groups thousands", () => {
    expect(formatSize(12345)).toBe("12,345");
  });
});

describe("formatTapeTime", () => {
  it("renders the exchange timestamp as ET wall clock", () => {
    expect(formatTapeTime("2026-07-06T13:30:05Z")).toBe("09:30:05"); // EDT = UTC-4
  });
});
