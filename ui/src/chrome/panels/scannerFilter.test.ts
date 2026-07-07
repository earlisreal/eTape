import { describe, it, expect } from "vitest";
import type { ScannerRow } from "../../wire/contract";
import { applyScannerFilters, sortByChangeDesc, formatFilterSummary, type ScannerThresholds } from "./scannerFilter";

const row = (symbol: string, changePct: number | null, floatShares: number | null, volume: number): ScannerRow =>
  ({ symbol, changePct, last: 1, floatShares, volume });

const OFF: ScannerThresholds = { minChangePct: 0, floatCapShares: null, minVolume: 0 };

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
    expect(formatFilterSummary({ minChangePct: 10, floatCapShares: 20_000_000, minVolume: 100_000 }))
      .toBe("change ≥ 10% · float ≤ 20M · vol ≥ 100k");
    expect(formatFilterSummary({ minChangePct: 5, floatCapShares: null, minVolume: 0 }))
      .toBe("change ≥ 5%");
  });
});
