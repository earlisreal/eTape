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

  it("M buckets are DST-transition-safe: pre- and post-transition ticks in the same ET month resolve to the same instant", () => {
    // 2026-03-08 is the US spring-forward Sunday. A tick before it (EST, UTC-5) and a
    // tick after it (EDT, UTC-4) are both "March 2026" and must bucket to the same
    // month-start epoch: 00:00 ET on 2026-03-01 == 05:00Z (EST, UTC-5, still in effect
    // on March 1st).
    const pre = bucketStartMs(at("2026-03-05T15:00:00Z"), "M"); // EST
    const post = bucketStartMs(at("2026-03-20T15:00:00Z"), "M"); // EDT
    expect(pre).toBe(at("2026-03-01T05:00:00Z"));
    expect(post).toBe(at("2026-03-01T05:00:00Z"));
    expect(pre).toBe(post);
  });

  it("W buckets are self-consistent across the week (no DST transition in practice, but exercised for uniformity)", () => {
    const monday = bucketStartMs(at("2026-07-06T13:31:00Z"), "W"); // Monday itself
    const wednesday = bucketStartMs(at("2026-07-08T18:00:00Z"), "W");
    const friday = bucketStartMs(at("2026-07-10T20:00:00Z"), "W");
    expect(monday).toBe(at("2026-07-06T04:00:00Z"));
    expect(wednesday).toBe(monday);
    expect(friday).toBe(monday);
  });
});
