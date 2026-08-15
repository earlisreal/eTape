import { describe, expect, it } from "vitest";
import { planScannerSync, scannerSyncStatusText } from "./scannerSync";

const slots = (symbols: Array<string | undefined>) => symbols.map((symbol, i) => ({ id: `slot-${i}`, symbol }));

describe("planScannerSync", () => {
  it("fills initial slots in stable order", () => {
    const plan = planScannerSync({ slots: slots([undefined, undefined, undefined]), rankedSymbols: ["A", "B", "C"], enabled: true, sourceAvailable: true });
    expect(plan.patches).toEqual([
      { slotId: "slot-0", symbol: "A" },
      { slotId: "slot-1", symbol: "B" },
      { slotId: "slot-2", symbol: "C" },
    ]);
    expect(plan.status).toEqual({ kind: "following", availableCount: 3, targetCount: 3 });
  });

  it("keeps every retained slot through rank swaps", () => {
    const plan = planScannerSync({ slots: slots(["A", "B", "C"]), rankedSymbols: ["C", "A", "B"], enabled: true, sourceAvailable: true });
    expect(plan.patches).toEqual([]);
  });

  it("fills added slots and replaces removed targets without moving retained symbols", () => {
    expect(planScannerSync({ slots: slots(["A", "B", undefined]), rankedSymbols: ["B", "A", "C"], enabled: true, sourceAvailable: true }).patches)
      .toEqual([{ slotId: "slot-2", symbol: "C" }]);
    expect(planScannerSync({ slots: slots(["A", "C"]), rankedSymbols: ["A", "B", "C"], enabled: true, sourceAvailable: true }).patches)
      .toEqual([{ slotId: "slot-1", symbol: "B" }]);
  });

  it("replaces departed symbols in earliest open slots", () => {
    const plan = planScannerSync({ slots: slots(["A", "B", "C", "D"]), rankedSymbols: ["A", "E", "C", "F"], enabled: true, sourceAvailable: true });
    expect(plan.patches).toEqual([
      { slotId: "slot-1", symbol: "E" },
      { slotId: "slot-3", symbol: "F" },
    ]);
  });

  it("restores a manual edit on the next successful plan", () => {
    const plan = planScannerSync({ slots: slots(["MANUAL", "B"]), rankedSymbols: ["A", "B"], enabled: true, sourceAvailable: true });
    expect(plan.patches).toEqual([{ slotId: "slot-0", symbol: "A" }]);
  });

  it("keeps unmatched existing symbols when rows are scarce", () => {
    const plan = planScannerSync({ slots: slots(["A", "B", "C", "D"]), rankedSymbols: ["X", "Y"], enabled: true, sourceAvailable: true });
    expect(plan.patches).toEqual([
      { slotId: "slot-0", symbol: "X" },
      { slotId: "slot-1", symbol: "Y" },
    ]);
    expect(plan.status).toEqual({ kind: "incomplete", availableCount: 2, targetCount: 4 });
  });

  it("does not apply patches while disabled, paused, or without targets", () => {
    expect(planScannerSync({ slots: slots(["A"]), rankedSymbols: ["B"], enabled: false, sourceAvailable: true }).status.kind).toBe("disabled");
    expect(planScannerSync({ slots: slots(["A"]), rankedSymbols: ["B"], enabled: true, sourceAvailable: false }).status.reason).toBe("source");
    expect(planScannerSync({ slots: [], rankedSymbols: ["B"], enabled: true, sourceAvailable: true }).status.reason).toBe("targets");
    expect(planScannerSync({ slots: slots(["A"]), rankedSymbols: [], enabled: true, sourceAvailable: true }).status.reason).toBe("rows");
  });

  it("deduplicates unusable rows before choosing membership", () => {
    const plan = planScannerSync({ slots: slots([undefined, undefined]), rankedSymbols: ["", "A", "A", "B"], enabled: true, sourceAvailable: true });
    expect(plan.patches).toEqual([{ slotId: "slot-0", symbol: "A" }, { slotId: "slot-1", symbol: "B" }]);
  });
});

describe("scannerSyncStatusText", () => {
  it("reports incomplete coverage", () => {
    expect(scannerSyncStatusText({ kind: "incomplete", availableCount: 2, targetCount: 4 })).toBe("Following 2/4");
  });
});
