import { describe, it, expect } from "vitest";
import type { Tick } from "../../wire/contract";
import { adjustAnchor, BLOCK_THRESHOLD, buildTapeRows, liveView, type TapeSource, type TapeView } from "./tapeState";

function mkTick(i: number, over: Partial<Tick> = {}): Tick {
  return {
    symbol: "US.AAPL",
    price: 3.5 + ((i % 5) - 2) * 0.01,
    size: 100 * (1 + (i % 3)), // 100 / 200 / 300
    direction: (["BUY", "SELL", "NEUTRAL"] as const)[i % 3],
    ts: new Date(Date.UTC(2026, 6, 6, 13, 30, i)).toISOString(),
    ...over,
  };
}

/** Array-backed TapeSource: tick k (1-based) has seq k. */
function srcFrom(ticks: Tick[], generation = 1): TapeSource {
  return {
    lastSeq: () => ticks.length,
    oldestSeq: () => 1,
    generation: () => generation,
    tickBySeq: (s) => (s >= 1 && s <= ticks.length ? ticks[s - 1] : undefined),
  };
}

const ticks = Array.from({ length: 30 }, (_, i) => mkTick(i + 1));
const src = srcFrom(ticks);

describe("buildTapeRows", () => {
  it("live view returns the newest rows, newest first", () => {
    const { rows, paused } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 0, maxRows: 5 });
    expect(paused).toBe(false);
    expect(rows).toHaveLength(5);
    expect(rows.map((r) => r.seq)).toEqual([30, 29, 28, 27, 26]);
  });

  it("applies the min-size filter", () => {
    const { rows } = buildTapeRows(src, liveView(src), { symbol: "US.AAPL", minSize: 300, maxRows: 50 });
    // size 300 hits every i where i % 3 === 2 → seqs 2, 5, 8, ..., 29 → 10 ticks
    expect(rows).toHaveLength(10);
    expect(rows.every((r) => r.size === "300")).toBe(true);
  });

  it("filters to the panel's symbol (the ring is shared per window)", () => {
    const mixed = srcFrom([mkTick(1), mkTick(2, { symbol: "US.NVDA" }), mkTick(3)]);
    const { rows } = buildTapeRows(mixed, liveView(mixed), { symbol: "US.AAPL", minSize: 0, maxRows: 10 });
    expect(rows.map((r) => r.seq)).toEqual([3, 1]);
  });

  it("an anchored view is paused and stays put", () => {
    const view: TapeView = { anchorSeq: 20, generation: 1 };
    const { rows, paused } = buildTapeRows(src, view, { symbol: "US.AAPL", minSize: 0, maxRows: 3 });
    expect(paused).toBe(true);
    expect(rows.map((r) => r.seq)).toEqual([20, 19, 18]);
  });

  it("a stale-generation anchor renders live (reconnect honesty)", () => {
    const view: TapeView = { anchorSeq: 20, generation: 0 };
    const { paused, rows } = buildTapeRows(src, view, { symbol: "US.AAPL", minSize: 0, maxRows: 3 });
    expect(paused).toBe(false);
    expect(rows[0].seq).toBe(30);
  });

  it("an anchor that aged out of the retained ring renders live (eviction honesty)", () => {
    // Same generation as the view (not the already-covered stale-generation case) but the
    // ring's oldest retained tick has advanced past the anchor — it was evicted while paused.
    const evicted: TapeSource = { ...src, oldestSeq: () => 10 };
    const view: TapeView = { anchorSeq: 5, generation: 1 };
    const { paused, rows } = buildTapeRows(evicted, view, { symbol: "US.AAPL", minSize: 0, maxRows: 3 });
    expect(paused).toBe(false);
    expect(rows[0].seq).toBe(src.lastSeq());
  });

  it("formats rows: ET time, uniform decimals, grouped sizes", () => {
    const one = srcFrom([mkTick(1, { price: 3.5, size: 1428, ts: "2026-07-06T13:30:05Z" })]);
    const { rows } = buildTapeRows(one, liveView(one), { symbol: "US.AAPL", minSize: 0, maxRows: 1 });
    expect(rows[0]).toMatchObject({ time: "09:30:05", price: "3.50", size: "1,428" });
  });

  it("is empty (not crashing) on an empty ring", () => {
    const empty = srcFrom([]);
    const { rows, paused } = buildTapeRows(empty, liveView(empty), { symbol: "US.AAPL", minSize: 0, maxRows: 5 });
    expect(rows).toEqual([]);
    expect(paused).toBe(false);
  });

  it("flags prints >= the block threshold", () => {
    expect(BLOCK_THRESHOLD).toBe(10_000);
    const blocky = srcFrom([mkTick(1, { size: 12_500 }), mkTick(2, { size: 400 })]);
    const { rows } = buildTapeRows(blocky, liveView(blocky), { symbol: "US.AAPL", minSize: 0, maxRows: 10 });
    // newest first: seq 2 (400 shares) then seq 1 (12,500 shares)
    expect(rows.map((r) => r.isBlock)).toEqual([false, true]);
  });
});

describe("adjustAnchor", () => {
  const opts = { symbol: "US.AAPL", minSize: 0 };

  it("scrolling up from live pauses N rows back", () => {
    const v = adjustAnchor(src, liveView(src), -3, opts);
    expect(v).toEqual({ anchorSeq: 27, generation: 1 });
  });

  it("scrolling down toward the newest tick resumes live", () => {
    const v = adjustAnchor(src, { anchorSeq: 28, generation: 1 }, 5, opts);
    expect(v.anchorSeq).toBeNull();
  });

  it("steps in FILTERED row space so one wheel row is one on-screen row", () => {
    // minSize 300 keeps seqs 2, 5, 8, ..., 29; from live, 2 rows up lands on 26
    const v = adjustAnchor(src, liveView(src), -2, { symbol: "US.AAPL", minSize: 300 });
    expect(v.anchorSeq).toBe(26);
  });

  it("clamps at the retained tail instead of walking off", () => {
    const v = adjustAnchor(src, { anchorSeq: 3, generation: 1 }, -100, opts);
    expect(v.anchorSeq).toBe(1);
  });

  it("treats a stale-generation anchor as live before applying the delta", () => {
    const v = adjustAnchor(src, { anchorSeq: 5, generation: 0 }, -1, opts);
    expect(v).toEqual({ anchorSeq: 29, generation: 1 });
  });

  it("stays live on an empty ring", () => {
    const empty = srcFrom([]);
    expect(adjustAnchor(empty, liveView(empty), -3, opts).anchorSeq).toBeNull();
  });
});
