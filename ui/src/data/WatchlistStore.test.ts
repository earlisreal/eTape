import { describe, expect, it } from "vitest";
import { WatchlistStore } from "./WatchlistStore";
import type { SnapshotMsg, WatchlistRowsPayload } from "../wire/contract";

// SnapshotMsg/DeltaMsg require a `kind` discriminant (gen/wsmsg.ts) — every
// existing store test (BarStore.test.ts, ScannerStore.test.ts) includes it;
// omitting it fails typecheck, not just at runtime.
function msg(payload: WatchlistRowsPayload): SnapshotMsg {
  return { kind: "snapshot", topic: "watchlist.rows", payload };
}

describe("WatchlistStore", () => {
  it("applies a snapshot: symbols, rows map, refreshedAt", () => {
    const s = new WatchlistStore();
    s.apply(
      msg({
        refreshedAt: "2026-07-12T14:00:00.000Z",
        symbols: ["US.AAPL", "US.TSLA"],
        rows: [{ symbol: "US.AAPL", last: 10, changePct: 25, volume: 1000 }],
      }),
    );
    const snap = s.getSnapshot();
    expect(snap.symbols).toEqual(["US.AAPL", "US.TSLA"]);
    expect(snap.refreshedAt).toBe("2026-07-12T14:00:00.000Z");
    expect(snap.rows.get("US.AAPL")?.last).toBe(10);
    expect(snap.rows.has("US.TSLA")).toBe(false); // placeholder: in symbols, absent from rows
  });

  it("has() reflects membership", () => {
    const s = new WatchlistStore();
    expect(s.has("US.AAPL")).toBe(false);
    s.apply(msg({ refreshedAt: null, symbols: ["US.AAPL"], rows: [] }));
    expect(s.has("US.AAPL")).toBe(true);
    expect(s.has("US.NOPE")).toBe(false);
  });

  it("tolerates null slices from an early snapshot", () => {
    const s = new WatchlistStore();
    // A malformed/early payload shouldn't throw.
    s.apply(msg({ refreshedAt: null, symbols: null as never, rows: null as never }));
    expect(s.getSnapshot().symbols).toEqual([]);
    expect(s.has("US.X")).toBe(false);
  });
});
