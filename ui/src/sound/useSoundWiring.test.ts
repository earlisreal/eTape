// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { renderHook } from "@testing-library/react";
import { useSoundWiring } from "./useSoundWiring";
import { FillStore } from "../data/FillStore";
import { ExecStore } from "../data/ExecStore";
import { ScannerStore } from "../data/ScannerStore";
import type { Stores } from "../data/registry";
import type { SoundSink } from "./SoundEngine";

function stubStores() {
  return { fills: new FillStore(), exec: new ExecStore(), scanner: new ScannerStore() } as unknown as Stores;
}
function sink(): SoundSink & { calls: string[] } {
  const s = { calls: [] as string[],
    orderFilled: () => s.calls.push("fill"),
    orderRejected: () => s.calls.push("reject"),
    scannerHit: () => s.calls.push("scanner"),
    unlock: () => s.calls.push("unlock") };
  return s;
}

describe("useSoundWiring", () => {
  it("forwards store events to the engine and unlocks on first gesture", () => {
    const stores = stubStores();
    const engine = sink();
    renderHook(() => useSoundWiring(stores, engine));

    stores.fills.apply({ kind: "delta", topic: "exec.fills", payload: { venue: "alpaca", orderId: "o1", symbol: "AAPL", side: "BUY", qty: 1, price: 1, tsMs: 1 } });
    stores.exec.apply({ kind: "delta", topic: "exec.orders", payload: { venue: "alpaca", id: "o1", symbol: "AAPL", side: "BUY", type: "LIMIT", tif: "DAY", qty: 1, limitPrice: 1, stopPrice: 0, status: "REJECTED", executedQty: 0, leavesQty: 1, avgFillPrice: 0, rejectReason: "", replacesId: "", createdMs: 1, updatedMs: 1 } });
    window.dispatchEvent(new Event("pointerdown"));

    expect(engine.calls).toContain("fill");
    expect(engine.calls).toContain("reject");
    expect(engine.calls).toContain("unlock");
  });

  it("unsubscribes on unmount (no calls after)", () => {
    const stores = stubStores();
    const engine = sink();
    const { unmount } = renderHook(() => useSoundWiring(stores, engine));
    unmount();
    stores.fills.apply({ kind: "delta", topic: "exec.fills", payload: { venue: "alpaca", orderId: "z", symbol: "AAPL", side: "BUY", qty: 1, price: 1, tsMs: 1 } });
    expect(engine.calls).not.toContain("fill");
  });
});
