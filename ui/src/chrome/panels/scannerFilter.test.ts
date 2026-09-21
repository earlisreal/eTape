import { describe, it, expect } from "vitest";
import type { ScannerRow } from "../../wire/contract";
import { applyScannerFilters, sortByChangeDesc, formatFilterSummary, type ScannerThresholds } from "./scannerFilter";

const row = (symbol: string, changePct: number | null, floatShares: number | null, volume: number): ScannerRow =>
  ({ symbol, shortSellRestricted: false, changePct, last: 1, floatShares, volume, sessionVolume: volume, turnover: null, relativeVolume: null, shortInterest: null, shortInterestAsOf: null });

const OFF: ScannerThresholds = { minChangePct: 0, floatCapShares: null, minVolume: 0, minSessionVolume: 0, minTurnover: 0, minRelativeVolume: 0, minPrice: 0, maxPrice: 0 };

describe("applyScannerFilters", () => {
  const rows: ScannerRow[] = [
    row("A", 12, 5_000_000, 800_000),
    row("B", 3, 200_000_000, 50_000),
    row("C", null, 5_000_000, 0),      // no print yet
    row("D", -8, 5_000_000, 900_000),
  ];

  it("passes everything when thresholds are off", () => {
    expect(applyScannerFilters(rows, OFF).map((r) => r.symbol)).toEqual(["A", "B", "C", "D"]);
  });
  it("min %-change filters by magnitude and drops no-print rows", () => {
    expect(applyScannerFilters(rows, { ...OFF, minChangePct: 5 }).map((r) => r.symbol)).toEqual(["A", "D"]);
  });
  it("float cap excludes above the cap but keeps unknown-float rows", () => {
    const withNullFloat = [...rows, row("E", 20, null, 100_000)];
    expect(applyScannerFilters(withNullFloat, { ...OFF, floatCapShares: 10_000_000 }).map((r) => r.symbol))
      .toEqual(["A", "C", "D", "E"]); // B (200M) dropped; E (null float) kept
  });
  it("volume floor excludes below the floor", () => {
    expect(applyScannerFilters(rows, { ...OFF, minVolume: 100_000 }).map((r) => r.symbol)).toEqual(["A", "D"]);
  });
  it("session volume floor excludes below and unavailable values", () => {
    const unknown = { ...rows[0], symbol: "UNKNOWN", sessionVolume: null };
    expect(applyScannerFilters([...rows, unknown], { ...OFF, minSessionVolume: 100_000 }).map((r) => r.symbol)).toEqual(["A", "D"]);
    expect(applyScannerFilters([unknown], OFF)).toEqual([unknown]);
  });
  it("positive volume floor excludes an unavailable daily volume", () => {
    const unknown = { ...rows[0], symbol: "UNKNOWN", volume: null };
    expect(applyScannerFilters([...rows, unknown], { ...OFF, minVolume: 1 }).map((r) => r.symbol)).not.toContain("UNKNOWN");
    expect(applyScannerFilters([unknown], OFF)).toEqual([unknown]);
  });
  it("REL VOL floor keeps equality and excludes unavailable values", () => {
    const withRatios = rows.map((r, i) => ({ ...r, relativeVolume: i === 0 ? 2 : i === 1 ? 1.99 : null }));
    expect(applyScannerFilters(withRatios, { ...OFF, minRelativeVolume: 2 }).map((r) => r.symbol)).toEqual(["A"]);
    expect(applyScannerFilters(rows, OFF).map((r) => r.symbol)).toEqual(["A", "B", "C", "D"]);
  });
  it("Dollar Turnover floor keeps equality and excludes unavailable values", () => {
    const withTurnover = rows.map((r, i) => ({ ...r, turnover: i === 0 ? 2_000_000 : i === 1 ? 1_999_999 : null }));
    expect(applyScannerFilters(withTurnover, { ...OFF, minTurnover: 2_000_000 }).map((r) => r.symbol)).toEqual(["A"]);
  });
  it("price bounds keep inclusive boundaries and exclude unavailable prices", () => {
    const withPrices = [
      { ...rows[0], symbol: "LOW", last: 0.99 },
      { ...rows[0], symbol: "MIN", last: 1 },
      { ...rows[0], symbol: "MID", last: 5.25 },
      { ...rows[0], symbol: "MAX", last: 10 },
      { ...rows[0], symbol: "HIGH", last: 10.01 },
      { ...rows[0], symbol: "UNKNOWN", last: null },
    ];
    expect(applyScannerFilters(withPrices, { ...OFF, minPrice: 1, maxPrice: 10 }).map((r) => r.symbol))
      .toEqual(["MIN", "MID", "MAX"]);
    expect(applyScannerFilters(withPrices, OFF).map((r) => r.symbol)).toEqual(withPrices.map((r) => r.symbol));
  });
});

describe("sortByChangeDesc", () => {
  it("highest change first, no-print rows last, without mutating input", () => {
    const input = [row("A", 3, 1, 1), row("B", null, 1, 1), row("C", 42, 1, 1)];
    const out = sortByChangeDesc(input);
    expect(out.map((r) => r.symbol)).toEqual(["C", "A", "B"]);
    expect(input.map((r) => r.symbol)).toEqual(["A", "B", "C"]); // input untouched
  });
});

describe("formatFilterSummary", () => {
  it("formats set fields with human units, omits nulls/zeros", () => {
    expect(formatFilterSummary({ minChangePct: 10, floatCapShares: 20_000_000, minVolume: 100_000, minSessionVolume: 250_000, minTurnover: 12_500_000, minRelativeVolume: 2.5, minPrice: 1.25, maxPrice: 20 }))
      .toBe("change magnitude ≥ 10% · float ≤ 20M · vol ≥ 100k · session vol ≥ 250k · turnover ≥ 12.5M · rel vol ≥ 2.5 · price ≥ $1.25 · price ≤ $20");
    expect(formatFilterSummary({ minChangePct: 5, floatCapShares: null, minVolume: 0, minSessionVolume: 0, minTurnover: 0, minRelativeVolume: 0, minPrice: 0, maxPrice: 0 }))
      .toBe("change magnitude ≥ 5%");
  });
});
