import { describe, it, expect, vi } from "vitest";
import { sessionBands, sessionAt, buildDaySegment, classify } from "./sessions";

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

// --- buildDaySegment / classify -------------------------------------------
//
// These prove classify(t, buildDaySegment(t)) === sessionAt(t) for every t in a
// grid spanning session boundaries on a normal week AND across both 2026 US DST
// transitions, verified empirically (see the node/Intl scratch check referenced
// in the task report — not guessed):
//   - Spring-forward: Sunday 2026-03-08, 02:00 EST -> 03:00 EDT
//     (2026-03-08T07:00:00Z is the instant the wall clock jumps from 01:59:59 to 03:00:00).
//   - Fall-back: Sunday 2026-11-01, 02:00 EDT -> 01:00 EST
//     (2026-11-01T06:00:00Z is the instant the wall clock repeats from 02:00:00 EDT back to 01:00:00 EST).
// barBucket.test.ts independently confirms 2026-03-08 is the spring-forward Sunday.

const PRE = 240, RTH = 570, POST = 960, CLOSE = 1200; // minutes into the ET day (mirrors sessions.ts)

// A grid of timestamps around every session boundary minute (dayMidnightMs + b),
// 1 minute before/at/1ms-before/1ms-after/1 minute after each boundary, plus one
// point clearly inside each of closed/pre/rth/post/closed-after-close.
function boundaryGrid(dayMidnightMs: number): number[] {
  const points: number[] = [];
  for (const b of [0, PRE, RTH, POST, CLOSE]) {
    for (const deltaMs of [-60_000, -1, 0, 1, 60_000]) {
      points.push(dayMidnightMs + b * 60_000 + deltaMs);
    }
  }
  points.push(dayMidnightMs + 60 * 60_000); // 01:00 ET — closed (before PRE)
  points.push(dayMidnightMs + (PRE + 60) * 60_000); // 05:00 ET — pre
  points.push(dayMidnightMs + (RTH + 60) * 60_000); // 10:30 ET — rth
  points.push(dayMidnightMs + (POST + 60) * 60_000); // 17:00 ET — post
  points.push(dayMidnightMs + (CLOSE + 60) * 60_000); // 21:00 ET — closed
  return points;
}

describe("buildDaySegment + classify — equivalence with sessionAt", () => {
  it("matches sessionAt at every boundary minute and interior point on a normal weekday", () => {
    // 2026-07-06 is a Monday, ET = EDT (UTC-4) in July. Midnight ET = 04:00Z.
    const MON_MIDNIGHT = at("2026-07-06T04:00:00Z");
    for (const t of boundaryGrid(MON_MIDNIGHT)) {
      expect(classify(t, buildDaySegment(t))).toBe(sessionAt(t));
    }
  });

  it("matches sessionAt on Saturday and Sunday (closed all day, both implementations agree)", () => {
    const sat = at("2026-07-04T15:00:00Z"); // Saturday afternoon ET
    const sun = at("2026-07-05T15:00:00Z"); // Sunday afternoon ET
    for (const t of [sat, sun]) {
      expect(sessionAt(t)).toBe("closed");
      expect(classify(t, buildDaySegment(t))).toBe("closed");
    }
  });

  it("spring-forward Sunday 2026-03-08 (02:00 EST -> 03:00 EDT): closed throughout, before/at/after the jump", () => {
    const points = [
      at("2026-03-08T05:00:00Z"), // 00:00 EST, Sunday midnight
      at("2026-03-08T06:30:00Z"), // 01:30 EST, before the jump
      at("2026-03-08T06:59:59Z"), // 01:59:59 EST, 1s before the jump
      at("2026-03-08T07:00:00Z"), // 03:00:00 EDT, the instant of the jump
      at("2026-03-08T07:30:00Z"), // 03:30 EDT, after the jump
      at("2026-03-08T23:00:00Z"), // 19:00 EDT, evening
    ];
    for (const t of points) {
      expect(sessionAt(t)).toBe("closed");
      expect(classify(t, buildDaySegment(t))).toBe("closed");
    }
  });

  it("Monday after spring-forward (2026-03-09, EDT) matches sessionAt at every boundary", () => {
    const MON_AFTER_SPRING = at("2026-03-09T04:00:00Z"); // 00:00 ET (EDT)
    for (const t of boundaryGrid(MON_AFTER_SPRING)) {
      expect(classify(t, buildDaySegment(t))).toBe(sessionAt(t));
    }
  });

  it("fall-back Sunday 2026-11-01 (02:00 EDT -> 01:00 EST): closed throughout, including the repeated hour", () => {
    const points = [
      at("2026-11-01T04:00:00Z"), // 00:00 EDT, Sunday midnight
      at("2026-11-01T05:30:00Z"), // 01:30 EDT, before the jump (1st occurrence of 01:xx)
      at("2026-11-01T05:59:59Z"), // 01:59:59 EDT, 1s before the jump
      at("2026-11-01T06:00:00Z"), // 01:00:00 EST, the instant of the jump (2nd occurrence of 01:xx begins)
      at("2026-11-01T06:59:59Z"), // 01:59:59 EST, end of the repeated hour
      at("2026-11-01T07:00:00Z"), // 02:00:00 EST, normal time resumes
      at("2026-11-01T23:00:00Z"), // 18:00 EST, evening
    ];
    for (const t of points) {
      expect(sessionAt(t)).toBe("closed");
      expect(classify(t, buildDaySegment(t))).toBe("closed");
    }
  });

  it("Monday after fall-back (2026-11-02, EST) matches sessionAt at every boundary", () => {
    const MON_AFTER_FALL = at("2026-11-02T05:00:00Z"); // 00:00 ET (EST)
    for (const t of boundaryGrid(MON_AFTER_FALL)) {
      expect(classify(t, buildDaySegment(t))).toBe(sessionAt(t));
    }
  });
});

// --- Proves the DST reasoning documented in sessions.ts, rather than trusting it ---

describe("buildDaySegment — DST dayEndMs precision", () => {
  it("a weekday's dayEndMs lands exactly on the next real ET midnight (transition never crosses a weekday span)", () => {
    const seg = buildDaySegment(at("2026-03-09T13:00:00Z")); // Monday after spring-forward, EDT
    expect(seg.dayEndMs).toBe(at("2026-03-10T04:00:00Z")); // true Tuesday midnight ET (EDT)
  });

  it("Sunday's dayEndMs is 1h LATE across the spring-forward transition, but classify() still says closed in the gap", () => {
    // Sample Sunday from BEFORE its own 2am jump (still EST): dayStartMs lands on the
    // true pre-transition midnight (00:00 EST = 05:00Z), so +24h overshoots the real
    // next midnight by 1h. (Sampling from after the jump gives a different, exact,
    // result instead — see the "same-day vs different-day" describe block above.)
    const seg = buildDaySegment(at("2026-03-08T06:30:00Z")); // 01:30 EST, before the jump
    const trueMondayMidnight = at("2026-03-09T04:00:00Z");
    expect(seg.dayEndMs - trueMondayMidnight).toBe(60 * 60 * 1000);
    // A timestamp in the imprecise gap [trueMondayMidnight, seg.dayEndMs) is, by wall
    // clock, early Monday morning (00:00-01:00 ET, before the 04:00 PRE threshold).
    // sessionAt says closed on its own merits; classify() using the (stale, if reused)
    // Sunday segment also says closed, because seg.weekend short-circuits first.
    const inGap = trueMondayMidnight + 30 * 60_000; // 00:30 ET Monday
    expect(inGap).toBeLessThan(seg.dayEndMs);
    expect(sessionAt(inGap)).toBe("closed");
    expect(classify(inGap, seg)).toBe("closed");
  });

  it("Sunday's dayEndMs is 1h EARLY across the fall-back transition, same no-misclassification property", () => {
    // Sample Sunday from BEFORE its own 2am jump (still EDT), for the same reason.
    const seg = buildDaySegment(at("2026-11-01T05:00:00Z")); // 01:00 EDT, before the jump
    const trueMondayMidnight = at("2026-11-02T05:00:00Z");
    expect(trueMondayMidnight - seg.dayEndMs).toBe(60 * 60 * 1000);
    // A timestamp in [seg.dayEndMs, trueMondayMidnight) is, by wall clock, still the
    // last hour of Sunday (23:00-24:00 ET) — both functions independently agree closed.
    const inGap = seg.dayEndMs + 30 * 60_000;
    expect(inGap).toBeLessThan(trueMondayMidnight);
    expect(sessionAt(inGap)).toBe("closed");
    expect(classify(inGap, seg)).toBe("closed");
  });
});

describe("buildDaySegment — same-day vs different-day segments (self-correction, no extrapolation)", () => {
  it("two different tsMs on the same calendar day produce identical segments", () => {
    const a = buildDaySegment(at("2026-07-06T05:00:00Z")); // 01:00 ET
    const b = buildDaySegment(at("2026-07-06T23:30:00Z")); // 19:30 ET, same Monday
    expect(a).toEqual(b);
  });

  it("tsMs on different calendar days produce different segments with boundaries shifted by exactly 24h (no transition involved)", () => {
    const mon = buildDaySegment(at("2026-07-06T13:00:00Z"));
    const tue = buildDaySegment(at("2026-07-07T13:00:00Z"));
    expect(tue.dayStartMs).toBe(mon.dayStartMs + 24 * 60 * 60 * 1000);
    expect(tue.preMs).toBe(mon.preMs + 24 * 60 * 60 * 1000);
    expect(tue).not.toEqual(mon);
  });

  it("segments shift by 23h across the spring-forward Sunday->Monday boundary (not a fixed 24h) — proves fresh derivation, not extrapolation from the prior segment", () => {
    // Sample Sunday from BEFORE its own 2am jump (still EST) so dayStartMs lands on
    // the true pre-transition midnight (00:00 EST = 05:00Z). Sampling Sunday from
    // AFTER the jump (EDT) would instead reproduce a 24h-shifted pseudo-midnight —
    // see the "DST dayEndMs precision" tests below for that same-day asymmetry.
    const sun = buildDaySegment(at("2026-03-08T06:30:00Z")); // 01:30 EST, before the jump
    const mon = buildDaySegment(at("2026-03-09T13:00:00Z"));
    expect(sun.dayStartMs).toBe(at("2026-03-08T05:00:00Z"));
    expect(mon.dayStartMs).toBe(at("2026-03-09T04:00:00Z"));
    expect(mon.dayStartMs - sun.dayStartMs).toBe(23 * 60 * 60 * 1000);
  });

  it("segments shift by 25h across the fall-back Sunday->Monday boundary", () => {
    // Sample Sunday from BEFORE its own 2am jump (still EDT) for the same reason.
    const sun = buildDaySegment(at("2026-11-01T05:00:00Z")); // 01:00 EDT, before the jump
    const mon = buildDaySegment(at("2026-11-02T13:00:00Z"));
    expect(sun.dayStartMs).toBe(at("2026-11-01T04:00:00Z"));
    expect(mon.dayStartMs).toBe(at("2026-11-02T05:00:00Z"));
    expect(mon.dayStartMs - sun.dayStartMs).toBe(25 * 60 * 60 * 1000);
  });

  it("sampling the transition Sunday from AFTER its own jump shifts dayStartMs too (not just dayEndMs) — still harmless, since classify() never uses dayStartMs on a weekend day", () => {
    const beforeJump = buildDaySegment(at("2026-03-08T06:30:00Z")); // 01:30 EST -> true midnight 05:00Z
    const afterJump = buildDaySegment(at("2026-03-08T15:00:00Z")); // 11:00 EDT, same Sunday -> 04:00Z
    expect(beforeJump.dayStartMs).not.toBe(afterJump.dayStartMs);
    // afterJump's dayStartMs is 1h EARLIER: subtracting its post-jump wall-clock hour
    // count (h=11) under the post-jump offset lands before the true (pre-jump-offset) midnight.
    expect(beforeJump.dayStartMs - afterJump.dayStartMs).toBe(60 * 60 * 1000);
    // Both are still correctly flagged as the same weekend day, so classify() agrees "closed".
    expect(beforeJump.weekend).toBe(true);
    expect(afterJump.weekend).toBe(true);
  });
});

describe("buildDaySegment — Intl cost, exactly one call", () => {
  it("calls Intl.DateTimeFormat.prototype.formatToParts exactly once per buildDaySegment call", () => {
    const spy = vi.spyOn(Intl.DateTimeFormat.prototype, "formatToParts");
    spy.mockClear();
    buildDaySegment(at("2026-07-06T13:00:00Z"));
    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });
});
