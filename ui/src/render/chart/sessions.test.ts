import { describe, it, expect } from "vitest";
import { sessionBands } from "./sessions";

const at = (iso: string) => Date.parse(iso);

describe("sessionBands", () => {
  it("segments one ET trading day into pre / rth / post / closed", () => {
    // 2026-07-06 (EDT): pre 04:00–09:30 ET = 08:00–13:30Z; rth 09:30–16:00 = 13:30–20:00Z;
    // post 16:00–20:00 = 20:00–24:00Z.
    const bands = sessionBands(at("2026-07-06T04:00:00Z"), at("2026-07-07T04:00:00Z"));
    const rth = bands.find((b) => b.session === "rth")!;
    expect(rth.startMs).toBe(at("2026-07-06T13:30:00Z"));
    expect(rth.endMs).toBe(at("2026-07-06T20:00:00Z"));
    const pre = bands.find((b) => b.session === "pre")!;
    expect(pre.endMs).toBe(at("2026-07-06T13:30:00Z"));
    const post = bands.find((b) => b.session === "post")!;
    expect(post.startMs).toBe(at("2026-07-06T20:00:00Z"));
  });

  it("bands are contiguous and cover the whole range", () => {
    const from = at("2026-07-06T10:00:00Z"), to = at("2026-07-06T22:00:00Z");
    const bands = sessionBands(from, to);
    expect(bands[0].startMs).toBe(from);
    expect(bands[bands.length - 1].endMs).toBe(to);
    for (let i = 1; i < bands.length; i++) expect(bands[i].startMs).toBe(bands[i - 1].endMs);
  });
});
