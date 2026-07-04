import { describe, it, expect } from "vitest";
import { bucketStartMs, etParts } from "./barBucket";

// 2026-07-06 is a Monday; ET is EDT (UTC-4) in July.
const at = (iso: string) => Date.parse(iso);

describe("etParts", () => {
  it("converts a UTC instant to ET wall-clock (EDT in July)", () => {
    const p = etParts(at("2026-07-06T13:30:00Z")); // 09:30 ET
    expect([p.h, p.mi]).toEqual([9, 30]);
  });
  it("handles EST in January (UTC-5)", () => {
    const p = etParts(at("2026-01-06T14:30:00Z")); // 09:30 ET
    expect([p.h, p.mi]).toEqual([9, 30]);
  });
});

describe("bucketStartMs — session-anchored at 09:30 ET", () => {
  it("5m buckets align to :30/:35/:40 past the anchor", () => {
    expect(bucketStartMs(at("2026-07-06T13:31:00Z"), "5m")).toBe(at("2026-07-06T13:30:00Z"));
    expect(bucketStartMs(at("2026-07-06T13:34:59Z"), "5m")).toBe(at("2026-07-06T13:30:00Z"));
    expect(bucketStartMs(at("2026-07-06T13:35:00Z"), "5m")).toBe(at("2026-07-06T13:35:00Z"));
  });
  it("10s buckets align within the minute", () => {
    expect(bucketStartMs(at("2026-07-06T13:30:07Z"), "10s")).toBe(at("2026-07-06T13:30:00Z"));
    expect(bucketStartMs(at("2026-07-06T13:30:12Z"), "10s")).toBe(at("2026-07-06T13:30:10Z"));
  });
  it("1m buckets align to the minute", () => {
    expect(bucketStartMs(at("2026-07-06T13:30:45Z"), "1m")).toBe(at("2026-07-06T13:30:00Z"));
  });
  it("D buckets start at 00:00 ET (04:00Z in EDT)", () => {
    expect(bucketStartMs(at("2026-07-06T18:00:00Z"), "D")).toBe(at("2026-07-06T04:00:00Z"));
  });
});
